package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func version(t *testing.T, path string) int {
	t.Helper()
	db := openRaw(t, path)
	v, err := SchemaVersion(db)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	return v
}

func TestAFreshDatabaseLandsOnTheCurrentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "switchboard.db")
	s, err := Open(path, func() time.Time { return base })
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Close()

	if got := version(t, path); got != SchemaTarget() {
		t.Fatalf("schema = %d, want %d", got, SchemaTarget())
	}
	// Nothing existed, so nothing was worth copying.
	if entries, err := os.ReadDir(BackupDir(path)); err == nil && len(entries) > 0 {
		t.Fatalf("a fresh database left %d backup(s) behind", len(entries))
	}
}

func TestMigratingIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "switchboard.db")
	for i := 0; i < 3; i++ {
		s, err := Open(path, nil)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		s.Close()
	}
	if got := version(t, path); got != SchemaTarget() {
		t.Fatalf("schema = %d, want %d", got, SchemaTarget())
	}
}

// The case the whole mechanism exists for: a populated database from the last
// version, opened by a binary that knows one more. It must be copied first,
// migrated, and keep every row.
func TestAnOlderDatabaseIsBackedUpThenMigrated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "switchboard.db")

	s, err := Open(path, func() time.Time { return base })
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.AddEvent(Event{Author: "acme-dev1", Kind: KindQuestion, Title: "Which label?"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s.Close()
	atVersion := SchemaTarget()

	// A future migration, added the way a real one would be.
	withExtraMigration(t, migration{
		version: atVersion + 1,
		name:    "add a column",
		stmts:   []string{`ALTER TABLE events ADD COLUMN explained_at INTEGER`},
	})

	reopened, err := Open(path, func() time.Time { return base.Add(time.Hour) })
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer reopened.Close()

	if got := version(t, path); got != atVersion+1 {
		t.Fatalf("schema = %d, want %d", got, atVersion+1)
	}
	// The row survived the reshaping.
	events, err := reopened.OpenEvents(AudienceManager)
	if err != nil || len(events) != 1 || events[0].Title != "Which label?" {
		t.Fatalf("events after migrating = %+v (%v)", events, err)
	}
	// And a copy of the old shape was taken first.
	entries, err := os.ReadDir(BackupDir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v (%v), want exactly one", entries, err)
	}
	if !strings.Contains(entries[0].Name(), "-v"+itoa(atVersion)+"-") {
		t.Fatalf("backup %q does not name the version it came from", entries[0].Name())
	}
}

// A migration that fails leaves the database exactly as it was.
func TestAFailedMigrationChangesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "switchboard.db")
	s, err := Open(path, func() time.Time { return base })
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.AddEvent(Event{Author: "acme-dev1", Kind: KindInfo, Title: "gate green"})
	s.Close()
	atVersion := SchemaTarget()

	withExtraMigration(t, migration{
		version: atVersion + 1,
		name:    "broken step",
		stmts: []string{
			`ALTER TABLE events ADD COLUMN fine INTEGER`,
			`ALTER TABLE nothing_here ADD COLUMN boom INTEGER`,
		},
	})

	if _, err := Open(path, func() time.Time { return base.Add(time.Hour) }); err == nil {
		t.Fatal("a broken migration opened cleanly")
	}
	if got := version(t, path); got != atVersion {
		t.Fatalf("version = %d, want it left at %d", got, atVersion)
	}
	db := openRaw(t, path)
	// The half-applied column must be gone with the transaction.
	if _, err := db.Exec(`SELECT fine FROM events LIMIT 1`); err == nil {
		t.Fatal("a column from the failed migration is still there")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events = %d (%v), want the row untouched", n, err)
	}
}

// A database written before versions existed cannot be reshaped by guesswork.
func TestAnUnversionedDatabaseIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "switchboard.db")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatalf("wind back: %v", err)
	}
	s.Close()

	_, err = Open(path, nil)
	if !errors.Is(err, ErrUnversioned) {
		t.Fatalf("err = %v, want ErrUnversioned", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("the message does not name the file to move aside: %v", err)
	}
	if got := version(t, path); got != 0 {
		t.Fatalf("version = %d, want it untouched", got)
	}
}

// withExtraMigration appends one migration for the duration of a test.
func withExtraMigration(t *testing.T, m migration) {
	t.Helper()
	original := migrations
	migrations = append(append([]migration{}, original...), m)
	t.Cleanup(func() { migrations = original })
}

// An old binary opening a new database must not write into it.
func TestADatabaseFromTheFutureIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "switchboard.db")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.SaveSession("acme-dev1", StatusActive, "142", "")
	ahead := SchemaTarget() + 3
	if _, err := s.db.Exec("PRAGMA user_version = " + itoa(ahead)); err != nil {
		t.Fatalf("stamp ahead: %v", err)
	}
	s.Close()

	_, err = Open(path, nil)
	if !errors.Is(err, ErrSchemaFromTheFuture) {
		t.Fatalf("err = %v, want ErrSchemaFromTheFuture", err)
	}
	// The message has to tell the operator what to do about it.
	for _, want := range []string{"newer", "backup"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q does not mention %q", err, want)
		}
	}
	// Nothing was touched: same version, same rows.
	if got := version(t, path); got != ahead {
		t.Fatalf("version = %d, want it untouched at %d", got, ahead)
	}
}

func TestPendingListsWhatIsLeftToDo(t *testing.T) {
	if got := len(Pending(SchemaTarget())); got != 0 {
		t.Fatalf("an up-to-date database has %d migration(s) pending", got)
	}
	if got := len(Pending(0)); got != SchemaTarget() {
		t.Fatalf("an empty database has %d pending, want %d", got, SchemaTarget())
	}
	names := PendingNames(0)
	if len(names) == 0 || !strings.Contains(names[0], "initial schema") {
		t.Fatalf("names = %v", names)
	}
}

// A backup taken while the service is running must carry the rows that are only
// in the write-ahead log — a plain file copy would not.
func TestABackupOfALiveDatabaseHoldsEverything(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "switchboard.db")
	s, err := Open(path, func() time.Time { return base })
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	for i := 0; i < 5; i++ {
		if _, err := s.AddEvent(Event{Author: "acme-dev1", Kind: KindInfo, Title: "gate green"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	dest := filepath.Join(dir, "copy.db")
	if _, err := Backup(s.DB(), dest); err != nil {
		t.Fatalf("backup: %v", err)
	}

	copied := openRaw(t, dest)
	var n int
	if err := copied.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if n != 5 {
		t.Fatalf("copy holds %d events, want 5 — the WAL was left behind", n)
	}
	var v int
	if err := copied.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaTarget() {
		t.Fatalf("copy schema = %d (%v), want %d", v, err, SchemaTarget())
	}
}

func TestABackupNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "switchboard.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	dest := filepath.Join(dir, "copy.db")
	if _, err := Backup(s.DB(), dest); err != nil {
		t.Fatalf("first backup: %v", err)
	}
	if _, err := Backup(s.DB(), dest); err == nil {
		t.Fatal("a second backup silently replaced the first")
	}
}

func TestOnlyTheNewestBackupsAreKept(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	// Names carry a sortable timestamp, so name order is age order.
	var made []string
	for i := 1; i <= 8; i++ {
		name := filepath.Join(backups, "switchboard-v1-2026091"+itoa(i)+"-120000.db")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		made = append(made, name)
	}
	// Something that is not a backup must be left alone.
	other := filepath.Join(backups, "notes.txt")
	os.WriteFile(other, []byte("keep me"), 0o644)

	if err := pruneBackups(backups, backupsKept); err != nil {
		t.Fatalf("prune: %v", err)
	}
	for _, gone := range made[:len(made)-backupsKept] {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s should have been pruned", filepath.Base(gone))
		}
	}
	for _, kept := range made[len(made)-backupsKept:] {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s should have been kept", filepath.Base(kept))
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("pruning removed a file that was not a backup")
	}
}

func TestBackupPathSitsBesideTheDatabase(t *testing.T) {
	got := backupPath("/home/x/.local/share/switchboard/switchboard.db", 2,
		time.Date(2026, 9, 17, 16, 42, 5, 0, time.UTC))
	want := "/home/x/.local/share/switchboard/backups/switchboard-v2-20260917-164205.db"
	if got != want {
		t.Fatalf("backup path = %q, want %q", got, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
