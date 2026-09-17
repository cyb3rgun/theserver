package store

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// testClock returns a clock that stands still, so timestamps are predictable.
func testClock(at time.Time) Clock {
	return func() time.Time { return at }
}

// latestMigration is the version a fresh database ends up at.
func latestMigration(t *testing.T) (version, count int) {
	t.Helper()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	return migrations[len(migrations)-1].version, len(migrations)
}

// openTest opens a store in a fresh directory with a standing clock.
func openTest(t *testing.T, opts ...Option) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	return openTestDir(t, dir, opts...)
}

func openTestDir(t *testing.T, dir string, opts ...Option) *Store {
	t.Helper()
	opts = append([]Option{WithClock(testClock(time.UnixMilli(1_700_000_000_000).UTC()))}, opts...)
	s, err := Open(t.Context(), dir, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesDirectoryAndSchema(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing", "data")
	s := openTestDir(t, dir)

	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("database file: %v", err)
	}
	if got, want := s.Path(), filepath.Join(dir, FileName); got != want {
		t.Errorf("Path is %q, want %q", got, want)
	}

	names, err := s.tableNames(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"admin_tokens", "devices", "events", "schema_migrations", "session_devices", "sessions"}
	if !slices.Equal(names, want) {
		t.Errorf("tables are %v, want %v", names, want)
	}

	version, err := s.SchemaVersion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if latest, _ := latestMigration(t); version != latest {
		t.Errorf("schema version is %d, want %d", version, latest)
	}
	if err := s.Ping(t.Context()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestOpenAgainAppliesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	first := openTestDir(t, dir)

	var appliedAt int64
	row := first.db.QueryRowContext(t.Context(), "SELECT applied_at FROM schema_migrations WHERE version = 1")
	if err := row.Scan(&appliedAt); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// A different clock would show up in applied_at if the migration ran again.
	second := openTestDir(t, dir, WithClock(testClock(time.UnixMilli(1_900_000_000_000).UTC())))

	var rows int
	if err := second.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if _, count := latestMigration(t); rows != count {
		t.Errorf("schema_migrations holds %d rows, want %d", rows, count)
	}
	var again int64
	row = second.db.QueryRowContext(t.Context(), "SELECT applied_at FROM schema_migrations WHERE version = 1")
	if err := row.Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != appliedAt {
		t.Errorf("applied_at changed from %d to %d, so the migration ran twice", appliedAt, again)
	}
}

func TestPragmasOnEveryConnection(t *testing.T) {
	s := openTest(t, WithBusyTimeout(7*time.Second))

	// Hold two connections at the same time, so the checks run on different
	// connections of the pool and not twice on the same one.
	first, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	checks := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"synchronous", "1"},
		{"busy_timeout", "7000"},
	}
	for _, c := range checks {
		var onFirst, onSecond string
		if err := first.QueryRowContext(t.Context(), "PRAGMA "+c.pragma).Scan(&onFirst); err != nil {
			t.Fatalf("PRAGMA %s on the first connection: %v", c.pragma, err)
		}
		if err := second.QueryRowContext(t.Context(), "PRAGMA "+c.pragma).Scan(&onSecond); err != nil {
			t.Fatalf("PRAGMA %s on the second connection: %v", c.pragma, err)
		}
		if onFirst != c.want || onSecond != c.want {
			t.Errorf("PRAGMA %s is %q and %q, want %q on every connection", c.pragma, onFirst, onSecond, c.want)
		}
	}
}

func TestInfoReportsEmptyDatabase(t *testing.T) {
	s := openTest(t)

	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	latest, count := latestMigration(t)
	if info.SchemaVersion != latest {
		t.Errorf("schema version is %d, want %d", info.SchemaVersion, latest)
	}
	if info.JournalMode != "wal" {
		t.Errorf("journal mode is %q, want wal", info.JournalMode)
	}
	if !info.ForeignKeys {
		t.Error("foreign keys are off")
	}
	if info.BusyTimeoutMs != int(DefaultBusyTimeout.Milliseconds()) {
		t.Errorf("busy timeout is %d, want %d", info.BusyTimeoutMs, DefaultBusyTimeout.Milliseconds())
	}
	want := map[string]int64{"admin_tokens": 0, "devices": 0, "events": 0, "schema_migrations": int64(count), "session_devices": 0, "sessions": 0}
	if len(info.Tables) != len(want) {
		t.Fatalf("Info lists %d tables, want %d", len(info.Tables), len(want))
	}
	for _, table := range info.Tables {
		if rows, ok := want[table.Name]; !ok || rows != table.Rows {
			t.Errorf("table %s holds %d rows, want %d", table.Name, table.Rows, want[table.Name])
		}
	}
}

func TestLoadMigrationsAreOrderedAndNamed(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migration is embedded")
	}
	for i, m := range migrations {
		if i > 0 && m.version <= migrations[i-1].version {
			t.Errorf("migration %s is out of order", m.name)
		}
		if m.sql == "" {
			t.Errorf("migration %s is empty", m.name)
		}
	}
	if migrations[0].version != 1 {
		t.Errorf("the first migration is version %d, want 1", migrations[0].version)
	}
}
