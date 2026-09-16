// Package store is the memory of theserver: the SQLite connection with its
// pragmas, the versioned schema, the device registry and the event journal.
//
// One database file, theserver.db, lives in the data directory. Every
// connection of the pool is opened in WAL mode with synchronous NORMAL,
// foreign keys on and a busy timeout (D-011), and the schema is brought up to
// date by the embedded migrations (D-010).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// FileName is the name of the database file inside the data directory.
const FileName = "theserver.db"

// DefaultBusyTimeout is how long a statement waits for a locked database
// before it gives up.
const DefaultBusyTimeout = 5 * time.Second

// A Clock reports the current time. The store stamps ts_server and applied_at
// with it, so tests can make time deterministic.
type Clock func() time.Time

// Store owns the database connection and every statement that touches it.
type Store struct {
	db      *sql.DB
	path    string
	now     Clock
	writeMu sync.Mutex
}

// An Option changes how Open sets the database up.
type Option func(*settings)

type settings struct {
	busyTimeout time.Duration
	now         Clock
}

// WithBusyTimeout sets the busy_timeout pragma. A value of zero or less keeps
// DefaultBusyTimeout.
func WithBusyTimeout(d time.Duration) Option {
	return func(s *settings) {
		if d > 0 {
			s.busyTimeout = d
		}
	}
}

// WithClock replaces the clock the store stamps its timestamps with.
func WithClock(now Clock) Option {
	return func(s *settings) {
		if now != nil {
			s.now = now
		}
	}
}

// Open creates dataDir if it is missing, opens the database inside it and
// applies every migration that has not run yet. Opening an up to date
// database changes nothing.
func Open(ctx context.Context, dataDir string, opts ...Option) (*Store, error) {
	set := settings{busyTimeout: DefaultBusyTimeout, now: time.Now}
	for _, opt := range opts {
		opt(&set)
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", dataDir, err)
	}
	file := filepath.Join(dataDir, FileName)

	db, err := sql.Open("sqlite", dsn(file, set.busyTimeout))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", file, err)
	}

	s := &Store{db: db, path: file, now: set.now}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// dsn builds the connection string. The driver hands a file URI to SQLite,
// which applies the _pragma parameters on every connection the pool opens.
func dsn(file string, busyTimeout time.Duration) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	uri := url.URL{Path: filepath.ToSlash(file)}
	return "file:" + uri.EscapedPath() + "?" + q.Encode()
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the connection for packages that build on the store.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Path is the database file.
func (s *Store) Path() string {
	return s.path
}

// Ping proves that the database answers.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return fmt.Errorf("SELECT 1 returned %d", one)
	}
	return nil
}

// writing serializes the writes of this process and returns the unlock.
// SQLite takes one writer at a time anyway; waiting here instead of in its busy
// handler keeps many device connections from backing off against each other.
// Another process, such as a device command run beside the server, still
// meets the busy timeout.
func (s *Store) writing() func() {
	s.writeMu.Lock()
	return s.writeMu.Unlock
}

// nowMilli is the clock as unix milliseconds in UTC (D-014).
func (s *Store) nowMilli() int64 {
	return s.now().UTC().UnixMilli()
}

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded files, whose names start with the version,
// for example 0001_init.sql, and returns them in ascending order.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var migrations []migration
	seen := map[int]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		name := entry.Name()
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: the name must start with the version and an underscore", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		if version < 1 {
			return nil, fmt.Errorf("migration %s: version must be 1 or higher", name)
		}
		if other, duplicate := seen[version]; duplicate {
			return nil, fmt.Errorf("migration %s: version %d is also used by %s", name, version, other)
		}
		seen[version] = name
		body, err := migrationFS.ReadFile(path.Join("migrations", name))
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, migration{version: version, name: name, sql: string(body)})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	return migrations, nil
}

// migrate applies every migration that is not recorded yet, each in its own
// transaction together with its schema_migrations row.
func (s *Store) migrate(ctx context.Context) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m migration) error {
	defer s.writing()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
		m.version, s.nowMilli(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// appliedVersions reads schema_migrations. A database without that table has
// no migration applied yet, because migration 0001 creates it.
func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	exists, err := s.tableExists(ctx, "schema_migrations")
	if err != nil {
		return nil, err
	}
	applied := map[int]bool{}
	if !exists {
		return applied, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

func (s *Store) tableExists(ctx context.Context, name string) (bool, error) {
	var found string
	err := s.db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// SchemaVersion is the highest applied migration version, 0 on an empty
// database.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		return 0, err
	}
	return int(version.Int64), nil
}

// TableCount is the number of rows in one table.
type TableCount struct {
	Name string
	Rows int64
}

// Info is the state of the database, as --db-info reports it.
type Info struct {
	Path          string
	SchemaVersion int
	JournalMode   string
	ForeignKeys   bool
	BusyTimeoutMs int
	Tables        []TableCount
}

// Info collects where the database is, how far its schema has come, how the
// connection is set up and how many rows each table holds.
func (s *Store) Info(ctx context.Context) (Info, error) {
	info := Info{Path: s.path}

	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return Info{}, err
	}
	info.SchemaVersion = version

	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&info.JournalMode); err != nil {
		return Info{}, err
	}
	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return Info{}, err
	}
	info.ForeignKeys = foreignKeys == 1
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&info.BusyTimeoutMs); err != nil {
		return Info{}, err
	}

	names, err := s.tableNames(ctx)
	if err != nil {
		return Info{}, err
	}
	for _, name := range names {
		var rows int64
		// The name comes from sqlite_master and is quoted here, so it is not
		// an injection point.
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+name+`"`).Scan(&rows); err != nil {
			return Info{}, err
		}
		info.Tables = append(info.Tables, TableCount{Name: name, Rows: rows})
	}
	return info, nil
}

func (s *Store) tableNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
