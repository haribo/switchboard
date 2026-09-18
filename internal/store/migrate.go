package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A migration is one numbered step. Steps are applied in order, each in its own
// transaction, and the database's `user_version` is set inside that same
// transaction — so a database is never half-migrated.
//
// Never edit a migration that has shipped: it has already run on a database
// somewhere, and changing it would make two databases claim the same version
// while holding different schemas. Add a new one instead.
type migration struct {
	version int
	name    string
	stmts   []string
}

// migrations is the whole history. Index n-1 holds version n.
var migrations = []migration{
	{
		version: 1,
		name:    "initial schema",
		stmts: []string{
			`CREATE TABLE sessions (
				name       TEXT PRIMARY KEY,
				status     TEXT    NOT NULL,
				issue      TEXT    NOT NULL DEFAULT '',
				detail     TEXT    NOT NULL DEFAULT '',
				since_at   INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			)`,
			`CREATE TABLE events (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				author     TEXT    NOT NULL,
				role       TEXT    NOT NULL DEFAULT 'dev',
				kind       TEXT    NOT NULL,
				title      TEXT    NOT NULL,
				-- Sanitized HTML, never what a session sent verbatim.
				body       TEXT    NOT NULL DEFAULT '',
				link       TEXT    NOT NULL DEFAULT '',
				issue      TEXT    NOT NULL DEFAULT '',
				audience   TEXT    NOT NULL,
				options    TEXT    NOT NULL DEFAULT '[]',
				state      TEXT    NOT NULL DEFAULT 'open',
				created_at INTEGER NOT NULL,
				-- When this event (re)started waiting on somebody. Equal to
				-- created_at until an answer is undone, which puts it back on
				-- the plate.
				actionable_at INTEGER NOT NULL DEFAULT 0,
				closed_at  INTEGER
			)`,
			`CREATE INDEX events_open ON events(state, audience, kind)`,
			`CREATE TABLE replies (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				event_id   INTEGER NOT NULL REFERENCES events(id),
				author     TEXT    NOT NULL,
				text       TEXT    NOT NULL DEFAULT '',
				option     TEXT    NOT NULL DEFAULT '',
				created_at INTEGER NOT NULL,
				seen_at    INTEGER
			)`,
			`CREATE INDEX replies_event ON replies(event_id)`,
			`CREATE TABLE wake (
				audience         TEXT PRIMARY KEY,
				signaled_at      INTEGER,
				signaled_through INTEGER
			)`,
		},
	},
	{
		version: 2,
		name:    "record who withdrew an event",
		stmts: []string{
			// Who closed an event without answering it. Someone has to be
			// accountable for a card vanishing from under the reader.
			`ALTER TABLE events ADD COLUMN closed_by TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 3,
		name:    "carry a request to put an ask in plain words",
		stmts: []string{
			// Set when the PO asks for the ask to be reworded, cleared when the
			// plain-words version arrives. The event stays open throughout: it
			// is still waiting for an answer, just not in those terms.
			`ALTER TABLE events ADD COLUMN explain_pending INTEGER NOT NULL DEFAULT 0`,
			// The plain-words version, sanitized like any other body. Kept
			// alongside the original, never replacing it.
			`ALTER TABLE events ADD COLUMN explanation TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 4,
		name:    "let a session name what it is waiting on",
		stmts: []string{
			// The session whose work this one is paused on, and the issue it is
			// paused on. Both or neither: waiting on somebody with nothing to
			// point at is the freely-declared flag this design refuses.
			`ALTER TABLE sessions ADD COLUMN waiting_on TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE sessions ADD COLUMN waiting_for TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 5,
		name:    "record when an explanation was asked for",
		stmts: []string{
			// The flag alone said nothing about when: the batching measures a
			// delay from the moment an item started waiting, and had nothing
			// to measure.
			`ALTER TABLE events ADD COLUMN explain_asked_at INTEGER`,
		},
	},
}

// SchemaTarget is the schema version this binary expects.
func SchemaTarget() int { return migrations[len(migrations)-1].version }

// ErrSchemaFromTheFuture is returned when the database was written by a newer
// binary. Carrying on would write old-shaped rows into a new-shaped database,
// quietly — so the service refuses to start instead.
var ErrSchemaFromTheFuture = errors.New("database is newer than this binary")

// ErrUnversioned is returned for a database that holds tables but carries no
// version stamp. Those were written before schema versions existed, during
// development. Their shape cannot be established by looking, so they are
// refused rather than guessed at.
var ErrUnversioned = errors.New("database has tables but no schema version")

// backupsKept is how many pre-migration copies stay on disk. Enough to undo a
// bad upgrade, few enough not to fill the disk with an ever-growing history.
const backupsKept = 5

// SchemaVersion reads the version stamped in the database. Zero means empty.
func SchemaVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

// Pending lists the migrations a database at `from` still needs.
func Pending(from int) []migration {
	var out []migration
	for _, m := range migrations {
		if m.version > from {
			out = append(out, m)
		}
	}
	return out
}

// PendingNames says what migrating would apply, for `db migrate --dry-run`.
func PendingNames(from int) []string {
	var out []string
	for _, m := range Pending(from) {
		out = append(out, fmt.Sprintf("%d — %s", m.version, m.name))
	}
	return out
}

// migrateOptions lets a caller (the dry run, the tests) change what migrate does
// without changing what it decides.
type migrateOptions struct {
	// dbPath is where the database lives, needed to place a backup beside it.
	// Empty skips the backup — used by tests on throwaway databases.
	dbPath string
	// now stamps the backup file name.
	now func() time.Time
}

// migrate brings the database up to SchemaTarget. It reports the versions it
// applied and the backup it took, so a caller can say what happened.
func migrate(db *sql.DB, opts migrateOptions) (applied []int, backup string, err error) {
	from, err := SchemaVersion(db)
	if err != nil {
		return nil, "", err
	}
	if from > SchemaTarget() {
		return nil, "", fmt.Errorf(
			"%w: database is at schema %d, this binary knows up to %d — install a newer switchboard, or restore a backup from %s",
			ErrSchemaFromTheFuture, from, SchemaTarget(), backupDir(opts.dbPath))
	}
	// A version of zero means one of two very different things: an empty file
	// waiting for its first schema, or a database written before versions were
	// stamped at all. Only the second has tables in it.
	if from == 0 {
		populated, err := hasUserTables(db)
		if err != nil {
			return nil, "", err
		}
		if populated {
			return nil, "", fmt.Errorf(
				"%w: it predates schema versioning and its shape cannot be established — "+
					"move %s aside and let switchboard create a new one, or restore a backup",
				ErrUnversioned, opts.dbPath)
		}
	}

	todo := Pending(from)
	if len(todo) == 0 {
		return nil, "", nil
	}

	// A database with something in it is worth copying before it is reshaped.
	// A brand new one has nothing to lose.
	if from > 0 && opts.dbPath != "" {
		backup, err = Backup(db, backupPath(opts.dbPath, from, opts.now()))
		if err != nil {
			return nil, "", fmt.Errorf("backup before migrating: %w", err)
		}
		if err := pruneBackups(backupDir(opts.dbPath), backupsKept); err != nil {
			return nil, "", err
		}
	}

	for _, m := range todo {
		if err := applyOne(db, m); err != nil {
			return applied, backup, fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		applied = append(applied, m.version)
	}
	return applied, backup, nil
}

// hasUserTables reports whether the database holds anything of ours.
func hasUserTables(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
	return n > 0, err
}

// applyOne runs a migration and stamps its version in the same transaction: the
// database is either fully at that version or still at the previous one.
func applyOne(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range m.stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	// PRAGMA takes no bound parameter; m.version is an int from this file.
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// Backup writes a consistent copy of an open database to dest.
//
// VACUUM INTO, not a file copy: under WAL the file on disk is missing whatever
// sits in the write-ahead log, so copying it yields a database that is silently
// behind — the worst kind of backup.
func Backup(db *sql.DB, dest string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("%s already exists", dest)
	}
	if _, err := db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// BackupDir is where copies of a database live: beside it, in one folder.
func BackupDir(dbPath string) string { return backupDir(dbPath) }

func backupDir(dbPath string) string {
	if dbPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dbPath), "backups")
}

func backupPath(dbPath string, version int, at time.Time) string {
	base := filepath.Base(dbPath)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	return filepath.Join(backupDir(dbPath),
		fmt.Sprintf("%s-v%d-%s.db", base, version, at.Format("20060102-150405")))
}

// pruneBackups keeps the newest `keep` copies and removes the rest.
func pruneBackups(dir string, keep int) error {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".db" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	// The names carry a sortable timestamp, so name order is age order.
	sort.Strings(files)
	for i := 0; i < len(files)-keep; i++ {
		if err := os.Remove(files[i]); err != nil {
			return err
		}
	}
	return nil
}
