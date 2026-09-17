package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// An EventID is the 16 byte UUID of an event (D-013). Devices generate it;
// the store never invents one.
type EventID [16]byte

// NewEventID draws a version 4 UUID from crypto/rand, with the layout of
// RFC 9562 and without a uuid dependency. The store does not call it; it is
// here for the simulated target, for tools and for tests.
func NewEventID() (EventID, error) {
	var id EventID
	if _, err := rand.Read(id[:]); err != nil {
		return EventID{}, fmt.Errorf("new event id: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id, nil
}

// IsZero reports the empty id, which is never a valid event id.
func (id EventID) IsZero() bool {
	return id == EventID{}
}

// String is the canonical 8-4-4-4-12 hexadecimal form.
func (id EventID) String() string {
	hexed := hex.EncodeToString(id[:])
	var b strings.Builder
	for i, part := range []int{8, 4, 4, 4, 12} {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(hexed[:part])
		hexed = hexed[part:]
	}
	return b.String()
}

// An Event is one entry of the journal, as the device sent it. TsDevice is
// unix milliseconds on the device; TsServer is stamped by the store on insert.
// ControllerID is pulled out of the payload by the caller, so that rankings do
// not have to decode payloads. Payload is the CBOR d map of the protocol,
// stored as received.
//
// An empty SessionID lets the store attribute the event by device time: to the
// session of the device whose start and end cover TsDevice (D-021).
type Event struct {
	ID           EventID
	Seq          uint64
	Kind         string
	ControllerID string
	SessionID    string
	TsDevice     int64
	TsServer     int64
	Payload      []byte
}

// An AppendResult reports what a batch did. AckSeq is the highest sequence
// number such that every sequence number from 1 to AckSeq is stored for that
// device, which is what the device link acknowledges.
type AppendResult struct {
	Stored     int
	Duplicates int
	AckSeq     uint64
}

var (
	// ErrSeqConflict says that the device reused a sequence number for a
	// different event. The batch is rolled back; the operator resolves it.
	ErrSeqConflict = errors.New("sequence number already used by another event")

	// ErrEventConflict says that an event id is already stored under another
	// device or sequence number. The batch is rolled back.
	ErrEventConflict = errors.New("event id already stored for another device or sequence number")
)

// A ConflictError names the event of a batch that contradicted the journal.
// It unwraps to ErrSeqConflict or ErrEventConflict.
type ConflictError struct {
	Seq     uint64
	EventID EventID
	Err     error
	Detail  string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("seq %d: %v: %s", e.Seq, e.Err, e.Detail)
}

func (e *ConflictError) Unwrap() error {
	return e.Err
}

// AppendEvents writes a batch of events of one device in a single
// transaction, and is safe to call again with the same batch: an event whose
// id is already stored counts as a duplicate and is not written twice.
//
// A sequence number that is already taken by a different event, or an event id
// that is already stored under a different sequence number, is a conflict: the
// whole batch is rolled back and nothing is stored. Events are immutable, so
// the journal never updates a row (D-012).
func (s *Store) AppendEvents(ctx context.Context, deviceID string, events []Event) (AppendResult, error) {
	if deviceID == "" {
		return AppendResult{}, errors.New("append events: device id must not be empty")
	}
	for i, e := range events {
		if err := validateEvent(e); err != nil {
			return AppendResult{}, fmt.Errorf("append events for %s: event %d: %w", deviceID, i, err)
		}
	}

	defer s.writing()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AppendResult{}, err
	}
	defer tx.Rollback()

	tsServer := s.nowMilli()
	var result AppendResult
	for _, e := range events {
		stored, err := appendEvent(ctx, tx, deviceID, e, tsServer)
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			return AppendResult{}, fmt.Errorf("append events for %s: %w", deviceID, err)
		}
		if err != nil {
			return AppendResult{}, fmt.Errorf("append events for %s: seq %d: %w", deviceID, e.Seq, err)
		}
		if stored {
			result.Stored++
		} else {
			result.Duplicates++
		}
	}

	ack, err := ackSeq(ctx, tx, deviceID)
	if err != nil {
		return AppendResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AppendResult{}, err
	}
	result.AckSeq = ack
	return result, nil
}

func validateEvent(e Event) error {
	if e.ID.IsZero() {
		return errors.New("event id must not be empty")
	}
	if e.Seq == 0 {
		return errors.New("sequence numbers start at 1")
	}
	if e.Kind == "" {
		return errors.New("kind must not be empty")
	}
	if e.Payload == nil {
		return errors.New("payload must not be nil")
	}
	return nil
}

// insertEvent stores one event. Without a session from the caller, the
// session is the one that holds the device and whose start and end cover the
// device time of the event, the latest started if several do; a session that
// is still running has no end yet.
const insertEvent = `
INSERT INTO events (
  event_id, device_id, seq, kind, controller_id, session_id, ts_device, ts_server, payload
) VALUES (?1, ?2, ?3, ?4, ?5,
  COALESCE(?6, (
    SELECT s.id
      FROM sessions s
      JOIN session_devices sd ON sd.session_id = s.id
     WHERE sd.device_id = ?2
       AND s.started_at IS NOT NULL
       AND s.started_at <= ?7
       AND (s.ended_at IS NULL OR ?7 < s.ended_at)
     ORDER BY s.started_at DESC, s.id
     LIMIT 1
  )),
  ?7, ?8, ?9)
ON CONFLICT(event_id) DO NOTHING`

// appendEvent inserts one event and reports whether it was stored. An id that
// is already in the journal is left alone, which makes a replay cheap, but
// only when it carries the same device and sequence number.
func appendEvent(ctx context.Context, tx *sql.Tx, deviceID string, e Event, tsServer int64) (bool, error) {
	result, err := tx.ExecContext(ctx, insertEvent,
		e.ID[:], deviceID, int64(e.Seq), e.Kind, nullString(e.ControllerID), nullString(e.SessionID),
		e.TsDevice, tsServer, e.Payload,
	)
	if err != nil {
		// The unique index on (device_id, seq) is the expected reason, and
		// asking the journal is safer than reading the driver message.
		taken, lookupErr := seqTakenBy(ctx, tx, deviceID, e.Seq)
		if lookupErr != nil {
			return false, err
		}
		if taken != nil && *taken != e.ID {
			return false, &ConflictError{
				Seq: e.Seq, EventID: e.ID, Err: ErrSeqConflict,
				Detail: "held by event " + taken.String(),
			}
		}
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 1 {
		return true, nil
	}

	// The id was there already. It is a duplicate only if it stands for the
	// same device and sequence number.
	var storedDevice string
	var storedSeq int64
	err = tx.QueryRowContext(ctx,
		`SELECT device_id, seq FROM events WHERE event_id = ?`, e.ID[:],
	).Scan(&storedDevice, &storedSeq)
	if err != nil {
		return false, err
	}
	if storedDevice != deviceID || uint64(storedSeq) != e.Seq {
		return false, &ConflictError{
			Seq: e.Seq, EventID: e.ID, Err: ErrEventConflict,
			Detail: fmt.Sprintf("event %s is stored for device %s seq %d", e.ID, storedDevice, storedSeq),
		}
	}
	return false, nil
}

// seqTakenBy returns the id of the event that holds this sequence number, or
// nil when the sequence number is free.
func seqTakenBy(ctx context.Context, q querier, deviceID string, seq uint64) (*EventID, error) {
	var raw []byte
	err := q.QueryRowContext(ctx,
		`SELECT event_id FROM events WHERE device_id = ? AND seq = ?`, deviceID, int64(seq),
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) != len(EventID{}) {
		return nil, fmt.Errorf("event id of %s seq %d is %d bytes", deviceID, seq, len(raw))
	}
	var id EventID
	copy(id[:], raw)
	return &id, nil
}

// LastSeq is the highest sequence number of a device such that no sequence
// number below it is missing. It is 0 when the device has no events or when
// its first event has not arrived yet.
func (s *Store) LastSeq(ctx context.Context, deviceID string) (uint64, error) {
	return ackSeq(ctx, s.db, deviceID)
}

func ackSeq(ctx context.Context, q querier, deviceID string) (uint64, error) {
	// The run has to start at 1, otherwise nothing is acknowledged.
	var one int
	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM events WHERE device_id = ? AND seq = 1`, deviceID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	// The end of the first run is the smallest stored sequence number whose
	// successor is missing.
	var end sql.NullInt64
	err = q.QueryRowContext(ctx, `
SELECT MIN(e.seq)
  FROM events e
 WHERE e.device_id = ?1
   AND NOT EXISTS (
       SELECT 1 FROM events n WHERE n.device_id = ?1 AND n.seq = e.seq + 1
   )`, deviceID).Scan(&end)
	if err != nil {
		return 0, err
	}
	if !end.Valid {
		return 0, nil
	}
	return uint64(end.Int64), nil
}

// A Filter narrows ListEvents. Empty fields do not filter. From and To are
// unix milliseconds of ts_server, From inclusive and To exclusive. A Limit of
// zero or less takes DefaultListLimit.
type Filter struct {
	DeviceID     string
	SessionID    string
	Kind         string
	ControllerID string
	From         int64
	To           int64
	Limit        int
	Offset       int
}

// DefaultListLimit caps a ListEvents call that does not ask for a limit.
const DefaultListLimit = 100

// ListEvents reads the journal in the order the server received it.
func (s *Store) ListEvents(ctx context.Context, f Filter) ([]Event, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	var events []Event
	err := s.eachEvent(ctx, f, limit, max(f.Offset, 0), func(e Event) error {
		events = append(events, e)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

// EachEvent calls fn for every event the filter selects, in the order of
// ListEvents but without a page limit; Limit and Offset are ignored. An error
// from fn stops the walk and is returned.
func (s *Store) EachEvent(ctx context.Context, f Filter, fn func(Event) error) error {
	if err := s.eachEvent(ctx, f, -1, 0, fn); err != nil {
		return fmt.Errorf("read events: %w", err)
	}
	return nil
}

// eachEvent runs the filtered query; a limit of -1 means no limit.
func (s *Store) eachEvent(ctx context.Context, f Filter, limit, offset int, fn func(Event) error) error {
	query := strings.Builder{}
	query.WriteString(`
SELECT event_id, seq, kind, controller_id, session_id, ts_device, ts_server, payload
  FROM events
 WHERE 1 = 1`)
	var args []any

	add := func(clause string, value any) {
		query.WriteString(clause)
		args = append(args, value)
	}
	if f.DeviceID != "" {
		add(" AND device_id = ?", f.DeviceID)
	}
	if f.SessionID != "" {
		add(" AND session_id = ?", f.SessionID)
	}
	if f.Kind != "" {
		add(" AND kind = ?", f.Kind)
	}
	if f.ControllerID != "" {
		add(" AND controller_id = ?", f.ControllerID)
	}
	if f.From != 0 {
		add(" AND ts_server >= ?", f.From)
	}
	if f.To != 0 {
		add(" AND ts_server < ?", f.To)
	}
	query.WriteString(" ORDER BY ts_server, device_id, seq LIMIT ? OFFSET ?")
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scanEvent(row rowScanner) (Event, error) {
	var (
		e                       Event
		raw                     []byte
		seq                     int64
		controller, sessionText sql.NullString
	)
	if err := row.Scan(&raw, &seq, &e.Kind, &controller, &sessionText, &e.TsDevice, &e.TsServer, &e.Payload); err != nil {
		return Event{}, err
	}
	if len(raw) != len(EventID{}) {
		return Event{}, fmt.Errorf("event id is %d bytes, want 16", len(raw))
	}
	copy(e.ID[:], raw)
	e.Seq = uint64(seq)
	e.ControllerID = controller.String
	e.SessionID = sessionText.String
	return e, nil
}

// querier is the part of *sql.DB and *sql.Tx the reads need.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
