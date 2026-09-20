// Package journal is the device side journal of the link protocol: the
// sequence number a device hands out, the epoch it counts in, the events the
// server has not acknowledged yet, and the replay after a dropped
// connection. The simulated target runs on it and so does theclient, which
// is why it is public (D-048).
//
// It stores in SQLite (D-052): one file, journal.db, in the directory it is
// given, in WAL mode with foreign keys on and synchronous FULL, so that an
// event is on the card before Append returns and a power cut cannot take it
// back. A target on an SD card is the case this is written for; a simulator
// that cares more about speed than about a power cut can ask for
// SyncNormal. A journal written by an earlier version, journal.cbor, is
// imported once and renamed (D-053).
package journal

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
	_ "modernc.org/sqlite"

	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// FileName is the name of the journal database inside its directory.
const FileName = "journal.db"

// LegacyFileName is the journal of the passes before S01-B10: one CBOR file
// rewritten on every change. Open imports it once and renames it to
// journal.cbor.imported (D-053).
const LegacyFileName = "journal.cbor"

// importedSuffix is what an imported legacy journal is renamed with.
const importedSuffix = ".imported"

// schemaVersion is the shape of the database this version writes. A journal
// of a newer version is refused instead of being read wrongly.
const schemaVersion = 1

// DefaultBusyTimeout is how long a statement waits for a locked journal.
// One process writes a journal; another one is a mistake, and waiting a
// moment reports it instead of losing an event.
const DefaultBusyTimeout = 5 * time.Second

// Sync says how far a commit is pushed before Append returns.
type Sync string

const (
	// SyncFull writes every commit through to the card before Append
	// returns: a power cut keeps every event the device handed out. It is
	// the default, because a target must not lose hits (D-052).
	SyncFull Sync = "FULL"
	// SyncNormal leaves the writing to the operating system: a killed
	// process keeps its events, a power cut may take the last ones. It is
	// for a simulator, not for a target.
	SyncNormal Sync = "NORMAL"
)

// ErrClosed says the journal was closed.
var ErrClosed = errors.New("journal is closed")

// An Option changes how Open sets the journal up.
type Option func(*settings)

type settings struct {
	sync     Sync
	deviceID string
}

// WithSync chooses how far a commit is pushed before Append returns; the
// default is SyncFull.
func WithSync(mode Sync) Option {
	return func(s *settings) {
		if mode == SyncFull || mode == SyncNormal {
			s.sync = mode
		}
	}
}

// WithDeviceID writes the device id into the journal and refuses to open a
// journal that belongs to another device. A journal found on a card then
// says whose events it holds.
func WithDeviceID(id string) Option {
	return func(s *settings) {
		s.deviceID = id
	}
}

// A Draft is an event before the journal gives it a sequence number and an
// id.
type Draft struct {
	Kind string
	Data cbor.RawMessage
}

// A Journal is the persisted memory of a device: the last sequence number it
// handed out and every event the server has not acknowledged yet. The
// database is the truth; what is in memory mirrors it, so a replay does not
// wait for a query.
type Journal struct {
	path string
	db   *sql.DB

	mu      sync.Mutex
	closed  bool
	epoch   uint64
	lastSeq uint64
	pending []protocol.Event
}

// Open loads the journal in dir, or starts an empty one. A journal.cbor of
// an earlier version is imported and renamed.
func Open(dir string, opts ...Option) (*Journal, error) {
	set := settings{sync: SyncFull}
	for _, opt := range opts {
		opt(&set)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("journal directory %s: %w", dir, err)
	}
	file := filepath.Join(dir, FileName)
	legacy := filepath.Join(dir, LegacyFileName)
	fresh := !exists(file)

	db, err := sql.Open("sqlite", dsn(file, set.sync))
	if err != nil {
		return nil, fmt.Errorf("journal %s: %w", file, err)
	}
	// One device writes one journal; one connection keeps the order plain
	// and the WAL small.
	db.SetMaxOpenConns(1)
	j := &Journal{path: file, db: db}
	if err := j.prepare(set, fresh, legacy); err != nil {
		db.Close()
		return nil, err
	}
	return j, nil
}

// prepare brings the database up, imports a legacy journal into a fresh one
// and reads the state into memory.
func (j *Journal) prepare(set settings, fresh bool, legacy string) error {
	if err := j.db.Ping(); err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	if _, err := j.db.Exec(schema); err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	version, err := j.meta("schema")
	if err != nil {
		return err
	}
	switch {
	case version == "":
		if err := j.setMeta(nil, "schema", strconv.Itoa(schemaVersion)); err != nil {
			return err
		}
	case version != strconv.Itoa(schemaVersion):
		return fmt.Errorf("journal %s: schema %s, this build writes %d", j.path, version, schemaVersion)
	}
	if err := j.keepDeviceID(set.deviceID); err != nil {
		return err
	}
	if fresh && exists(legacy) {
		if err := j.importLegacy(legacy); err != nil {
			return err
		}
	}
	return j.load()
}

// schema is the shape of the journal: what the device knows about itself,
// and every event the server has not acknowledged.
const schema = `
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  seq       INTEGER PRIMARY KEY,
  epoch     INTEGER NOT NULL,
  event_id  BLOB NOT NULL,
  kind      TEXT NOT NULL,
  ts_device INTEGER NOT NULL,
  payload   BLOB
);`

// keepDeviceID writes the device id on the first open and refuses a journal
// of another device.
func (j *Journal) keepDeviceID(id string) error {
	if id == "" {
		return nil
	}
	known, err := j.meta("device_id")
	if err != nil {
		return err
	}
	switch {
	case known == "":
		return j.setMeta(nil, "device_id", id)
	case known != id:
		return fmt.Errorf("journal %s belongs to the device %s, not to %s", j.path, known, id)
	}
	return nil
}

// load reads the state of the journal into memory.
func (j *Journal) load() error {
	epoch, err := j.metaUint("epoch")
	if err != nil {
		return err
	}
	lastSeq, err := j.metaUint("last_seq")
	if err != nil {
		return err
	}
	rows, err := j.db.Query(`SELECT seq, event_id, kind, ts_device, payload FROM events ORDER BY seq`)
	if err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	defer rows.Close()
	var pending []protocol.Event
	for rows.Next() {
		var (
			event   protocol.Event
			id      []byte
			payload []byte
		)
		if err := rows.Scan(&event.Seq, &id, &event.K, &event.Ts, &payload); err != nil {
			return fmt.Errorf("journal %s: %w", j.path, err)
		}
		if len(id) != len(event.ID) {
			return fmt.Errorf("journal %s: event %d has an id of %d bytes", j.path, event.Seq, len(id))
		}
		event.T = protocol.TypeEvent
		copy(event.ID[:], id)
		event.D = cbor.RawMessage(payload)
		pending = append(pending, event)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	if len(pending) > 0 && pending[len(pending)-1].Seq > lastSeq {
		return fmt.Errorf("journal %s: event %d above the last sequence %d", j.path, pending[len(pending)-1].Seq, lastSeq)
	}
	j.epoch, j.lastSeq, j.pending = epoch, lastSeq, pending
	return nil
}

// importLegacy reads a journal.cbor of an earlier version into the database
// and renames it, so that a device that is updated keeps its events (D-053).
func (j *Journal) importLegacy(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("journal %s: %w", path, err)
	}
	var state legacyState
	if err := cbor.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("journal %s: %w", path, err)
	}
	for i, e := range state.Pending {
		if e.Seq == 0 || e.Seq > state.LastSeq || (i > 0 && e.Seq <= state.Pending[i-1].Seq) {
			return fmt.Errorf("journal %s: pending event %d has seq %d", path, i, e.Seq)
		}
	}
	tx, err := j.db.Begin()
	if err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	defer tx.Rollback()
	for _, e := range state.Pending {
		if err := insertEvent(tx, max(state.Epoch, 1), e); err != nil {
			return fmt.Errorf("journal %s: %w", j.path, err)
		}
	}
	if err := j.setMeta(tx, "epoch", strconv.FormatUint(state.Epoch, 10)); err != nil {
		return err
	}
	if err := j.setMeta(tx, "last_seq", strconv.FormatUint(state.LastSeq, 10)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	if err := os.Rename(path, path+importedSuffix); err != nil {
		return fmt.Errorf("journal %s: %w", path, err)
	}
	return nil
}

// legacyState is the CBOR file of the passes before S01-B10.
type legacyState struct {
	Epoch   uint64           `cbor:"epoch,omitempty"`
	LastSeq uint64           `cbor:"last"`
	Pending []protocol.Event `cbor:"pending"`
}

// Append gives the draft the next sequence number and a new event id, writes
// it and returns the event to send. It returns after the event is on disk.
func (j *Journal) Append(d Draft, ts int64) (protocol.Event, error) {
	id, err := protocol.NewEventID()
	if err != nil {
		return protocol.Event{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return protocol.Event{}, ErrClosed
	}

	event := protocol.Event{
		T:   protocol.TypeEvent,
		ID:  id,
		Seq: j.lastSeq + 1,
		K:   d.Kind,
		Ts:  ts,
		D:   d.Data,
	}
	tx, err := j.db.Begin()
	if err != nil {
		return protocol.Event{}, fmt.Errorf("journal %s: %w", j.path, err)
	}
	defer tx.Rollback()
	if err := insertEvent(tx, max(j.epoch, 1), event); err != nil {
		return protocol.Event{}, fmt.Errorf("journal %s: %w", j.path, err)
	}
	if err := j.setMeta(tx, "last_seq", strconv.FormatUint(event.Seq, 10)); err != nil {
		return protocol.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Event{}, fmt.Errorf("journal %s: %w", j.path, err)
	}
	j.lastSeq = event.Seq
	j.pending = append(j.pending, event)
	return event, nil
}

func insertEvent(tx *sql.Tx, epoch uint64, event protocol.Event) error {
	id := event.ID
	_, err := tx.Exec(
		`INSERT INTO events (seq, epoch, event_id, kind, ts_device, payload) VALUES (?, ?, ?, ?, ?, ?)`,
		event.Seq, epoch, id[:], event.K, event.Ts, []byte(event.D))
	return err
}

// Acked drops every event up to seq, which the server has stored.
func (j *Journal) Acked(seq uint64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrClosed
	}

	keep := 0
	for keep < len(j.pending) && j.pending[keep].Seq <= seq {
		keep++
	}
	if keep == 0 {
		return nil
	}
	tx, err := j.db.Begin()
	if err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM events WHERE seq <= ?`, seq); err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	if err := j.setMeta(tx, "acked_seq", strconv.FormatUint(seq, 10)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	j.pending = slices.Clone(j.pending[keep:])
	return nil
}

// After returns the pending events above seq, in order.
func (j *Journal) After(seq uint64) []protocol.Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	var events []protocol.Event
	for _, e := range j.pending {
		if e.Seq > seq {
			events = append(events, e)
		}
	}
	return events
}

// Epoch is the sequence epoch the journal counts in.
func (j *Journal) Epoch() uint64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return max(j.epoch, 1)
}

// Reset starts the journal over in a new epoch, as a target does when the
// server announces an epoch it does not know: the counter goes back to 0 and
// the unacknowledged events are dropped. It returns how many were dropped.
func (j *Journal) Reset(epoch uint64) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return 0, ErrClosed
	}

	dropped := len(j.pending)
	tx, err := j.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("journal %s: %w", j.path, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM events`); err != nil {
		return 0, fmt.Errorf("journal %s: %w", j.path, err)
	}
	for key, value := range map[string]string{
		"epoch":     strconv.FormatUint(epoch, 10),
		"last_seq":  "0",
		"acked_seq": "0",
	} {
		if err := j.setMeta(tx, key, value); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("journal %s: %w", j.path, err)
	}
	j.epoch, j.lastSeq, j.pending = epoch, 0, nil
	return dropped, nil
}

// LastSeq is the highest sequence number the journal handed out in its
// epoch; hello carries it as last.
func (j *Journal) LastSeq() uint64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.lastSeq
}

// Pending is the number of events the server has not acknowledged.
func (j *Journal) Pending() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.pending)
}

// DeviceID is the device this journal belongs to, empty when none was given.
func (j *Journal) DeviceID() string {
	id, _ := j.meta("device_id")
	return id
}

// Path is the database file.
func (j *Journal) Path() string {
	return j.path
}

// Close closes the database. A journal that is closed refuses to change.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.db.Close()
}

// meta reads one value, empty when it is not there.
func (j *Journal) meta(key string) (string, error) {
	var value string
	err := j.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("journal %s: %w", j.path, err)
	}
	return value, nil
}

func (j *Journal) metaUint(key string) (uint64, error) {
	value, err := j.meta(key)
	if err != nil || value == "" {
		return 0, err
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("journal %s: %s is %q", j.path, key, value)
	}
	return n, nil
}

// setMeta writes one value, inside tx when there is one.
func (j *Journal) setMeta(tx *sql.Tx, key, value string) error {
	const query = `INSERT INTO meta (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`
	var err error
	if tx != nil {
		_, err = tx.Exec(query, key, value)
	} else {
		_, err = j.db.Exec(query, key, value)
	}
	if err != nil {
		return fmt.Errorf("journal %s: %w", j.path, err)
	}
	return nil
}

// dsn builds the connection string; the driver applies the pragmas on every
// connection it opens (D-052).
func dsn(file string, mode Sync) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", DefaultBusyTimeout.Milliseconds()))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous("+string(mode)+")")
	q.Add("_pragma", "foreign_keys(ON)")
	uri := url.URL{Path: filepath.ToSlash(file)}
	return "file:" + uri.EscapedPath() + "?" + q.Encode()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}
