package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"switchboard/internal/store"
)

const dbUsage = `switchboard db — look after the database

  status              where it is, what schema it holds, what it contains
  backup [--to PATH]  a consistent copy, taken while the service runs
  migrate [--dry-run] bring the schema up to date

  --db PATH           which database (default: the installed one)
`

func database(args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, dbUsage)
		return errors.New("db needs a subcommand")
	}
	switch args[0] {
	case "status":
		return dbStatus(args[1:])
	case "backup":
		return dbBackup(args[1:])
	case "migrate":
		return dbMigrate(args[1:])
	case "-h", "--help", "help":
		fmt.Print(dbUsage)
		return nil
	default:
		fmt.Fprint(os.Stderr, dbUsage)
		return fmt.Errorf("unknown db subcommand: %s", args[0])
	}
}

func dbFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("db "+name, flag.ExitOnError)
	path := fs.String("db", env("DB", store.DefaultPath()), "SQLite file")
	return fs, path
}

// dbStatus reports on a database without migrating it — including one this
// binary is too old for, which is exactly when somebody needs to be told.
func dbStatus(args []string) error {
	fs, path := dbFlags("status")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}

	info, err := os.Stat(*path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Printf("database  %s\n", *path)
		fmt.Printf("state     not created yet — it appears when the service first starts\n")
		fmt.Printf("schema    this binary provides %d\n", store.SchemaTarget())
		return nil
	}
	if err != nil {
		return err
	}

	db, err := store.OpenForInspection(*path)
	if err != nil {
		return err
	}
	defer db.Close()

	schema, err := store.SchemaVersion(db)
	if err != nil {
		return err
	}

	fmt.Printf("database  %s (%s)\n", *path, humanSize(info.Size()))
	fmt.Printf("schema    %d, this binary provides %d%s\n", schema, store.SchemaTarget(), schemaNote(schema))
	if pending := store.PendingNames(schema); len(pending) > 0 && schema <= store.SchemaTarget() {
		fmt.Printf("pending   %d migration(s):\n", len(pending))
		for _, p := range pending {
			fmt.Printf("            %s\n", p)
		}
	}

	for _, c := range []struct {
		label, query string
	}{
		{"sessions", `SELECT count(*) FROM sessions`},
		{"events", `SELECT count(*) FROM events`},
		{"open", `SELECT count(*) FROM events WHERE state = 'open'`},
		{"replies", `SELECT count(*) FROM replies`},
	} {
		var n int
		if err := db.QueryRow(c.query).Scan(&n); err != nil {
			// A schema this binary does not know may not have the table; say so
			// rather than fail the whole report.
			fmt.Printf("%-9s unreadable at this schema\n", c.label)
			continue
		}
		fmt.Printf("%-9s %d\n", c.label, n)
	}

	printBackups(store.BackupDir(*path))
	return nil
}

func schemaNote(schema int) string {
	switch {
	case schema > store.SchemaTarget():
		return "  — the database is newer than this binary; install a newer switchboard"
	case schema == 0:
		return "  — no version stamp"
	case schema < store.SchemaTarget():
		return "  — migration pending"
	}
	return ""
}

func printBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		fmt.Printf("backups   none in %s\n", dir)
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".db" {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		fmt.Printf("backups   none in %s\n", dir)
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	fmt.Printf("backups   %d in %s, newest %s\n", len(names), dir, names[0])
}

// dbBackup takes a copy on demand. It works on a running service: VACUUM INTO
// reads a consistent snapshot rather than the file on disk.
func dbBackup(args []string) error {
	fs, path := dbFlags("backup")
	to := fs.String("to", "", "where to write the copy (default: beside the database, timestamped)")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}

	db, err := store.OpenForInspection(*path)
	if err != nil {
		return err
	}
	defer db.Close()

	dest := *to
	if dest == "" {
		schema, err := store.SchemaVersion(db)
		if err != nil {
			return err
		}
		dest = filepath.Join(store.BackupDir(*path),
			fmt.Sprintf("switchboard-v%d-%s.db", schema, time.Now().Format("20060102-150405")))
	}
	written, err := store.Backup(db, dest)
	if err != nil {
		return err
	}
	info, err := os.Stat(written)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s)\n", written, humanSize(info.Size()))
	return nil
}

// dbMigrate applies what is pending. The service does this on its own at
// startup; this exists to see what would happen, and to migrate without
// starting the service.
func dbMigrate(args []string) error {
	fs, path := dbFlags("migrate")
	dry := fs.Bool("dry-run", false, "say what would be applied, and apply nothing")
	if err := parseNoArgs(fs, args); err != nil {
		return err
	}

	if *dry {
		db, err := store.OpenForInspection(*path)
		if err != nil {
			return err
		}
		defer db.Close()
		schema, err := store.SchemaVersion(db)
		if err != nil {
			return err
		}
		if schema > store.SchemaTarget() {
			return fmt.Errorf("database is at schema %d, this binary knows up to %d",
				schema, store.SchemaTarget())
		}
		pending := store.PendingNames(schema)
		if len(pending) == 0 {
			fmt.Printf("schema %d, nothing to apply\n", schema)
			return nil
		}
		fmt.Printf("schema %d → %d, %d migration(s) would be applied:\n",
			schema, store.SchemaTarget(), len(pending))
		for _, p := range pending {
			fmt.Printf("  %s\n", p)
		}
		fmt.Printf("a backup would be taken first, in %s\n", store.BackupDir(*path))
		return nil
	}

	// Opening is what migrates; the store takes the backup on the way.
	st, err := store.Open(*path, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	schema, err := store.SchemaVersion(st.DB())
	if err != nil {
		return err
	}
	fmt.Printf("schema %d\n", schema)
	return nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
