package store

import (
	"errors"
	"testing"
	"time"
)

// eventID builds a readable, unique id without drawing randomness, so a test
// can say which event it means.
func eventID(n byte) EventID {
	var id EventID
	id[15] = n
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func testEvent(seq uint64, n byte) Event {
	return Event{
		ID:       eventID(n),
		Seq:      seq,
		Kind:     "hit",
		TsDevice: 1_700_000_000_000 + int64(seq),
		Payload:  []byte{0xa0},
	}
}

// journalStore opens a store that already knows the device t-1.
func journalStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	s := openTest(t, opts...)
	if err := s.UpsertDevice(t.Context(), testDevice("t-1")); err != nil {
		t.Fatal(err)
	}
	return s
}

func countEvents(t *testing.T, s *Store) int {
	t.Helper()
	var rows int
	if err := s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM events").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAppendEventsStoresBatch(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	batch := []Event{testEvent(1, 1), testEvent(2, 2), testEvent(3, 3)}
	got, err := s.AppendEvents(ctx, "t-1", batch)
	if err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	want := AppendResult{Stored: 3, Duplicates: 0, AckSeq: 3}
	if got != want {
		t.Errorf("AppendEvents returned %+v, want %+v", got, want)
	}
	if rows := countEvents(t, s); rows != 3 {
		t.Errorf("the journal holds %d events, want 3", rows)
	}

	stored, err := s.ListEvents(ctx, Filter{DeviceID: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 {
		t.Fatalf("ListEvents returned %d events, want 3", len(stored))
	}
	// ts_server comes from the store clock, ts_device from the device.
	for i, e := range stored {
		if e.TsServer != 1_700_000_000_000 {
			t.Errorf("event %d has ts_server %d, want the clock of the store", i, e.TsServer)
		}
		if e.TsDevice != batch[i].TsDevice {
			t.Errorf("event %d has ts_device %d, want %d", i, e.TsDevice, batch[i].TsDevice)
		}
		if e.ID != batch[i].ID {
			t.Errorf("event %d has id %s, want %s", i, e.ID, batch[i].ID)
		}
	}
}

// TestAppendEventsIsIdempotent is the replay case: the device sends the batch
// again after a dropped connection.
func TestAppendEventsIsIdempotent(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	batch := []Event{testEvent(1, 1), testEvent(2, 2), testEvent(3, 3)}
	first, err := s.AppendEvents(ctx, "t-1", batch)
	if err != nil {
		t.Fatal(err)
	}

	again, err := s.AppendEvents(ctx, "t-1", batch)
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	want := AppendResult{Stored: 0, Duplicates: 3, AckSeq: first.AckSeq}
	if again != want {
		t.Errorf("the replayed batch returned %+v, want %+v", again, want)
	}
	if rows := countEvents(t, s); rows != 3 {
		t.Errorf("the journal holds %d events after the replay, want 3", rows)
	}
}

// TestAppendEventsGapHoldsBackAck is the dropped packet case: 1, 2 and 4 are
// there, so only 1 and 2 may be acknowledged until 3 arrives.
func TestAppendEventsGapHoldsBackAck(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	withGap, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 1), testEvent(2, 2), testEvent(4, 4)})
	if err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	if withGap.Stored != 3 {
		t.Errorf("stored %d events, want 3", withGap.Stored)
	}
	if withGap.AckSeq != 2 {
		t.Errorf("AckSeq is %d, want 2 while seq 3 is missing", withGap.AckSeq)
	}
	last, err := s.LastSeq(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if last != 2 {
		t.Errorf("LastSeq is %d, want 2", last)
	}

	filled, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(3, 3)})
	if err != nil {
		t.Fatalf("filling the gap: %v", err)
	}
	if filled.AckSeq != 4 {
		t.Errorf("AckSeq is %d after the gap was filled, want 4", filled.AckSeq)
	}
	last, err = s.LastSeq(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if last != 4 {
		t.Errorf("LastSeq is %d, want 4", last)
	}
}

// TestAppendEventsSeqConflict is the broken device case: the same sequence
// number comes back carrying a different event.
func TestAppendEventsSeqConflict(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	if _, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 1), testEvent(2, 2)}); err != nil {
		t.Fatal(err)
	}

	conflict := testEvent(2, 99) // seq 2 again, different event id
	got, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(3, 3), conflict})
	if !errors.Is(err, ErrSeqConflict) {
		t.Fatalf("AppendEvents returned %v, want ErrSeqConflict", err)
	}
	if got != (AppendResult{}) {
		t.Errorf("a refused batch returned %+v, want the zero result", got)
	}
	if rows := countEvents(t, s); rows != 2 {
		t.Errorf("the journal holds %d events, want 2: the whole batch rolls back", rows)
	}
	last, err := s.LastSeq(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if last != 2 {
		t.Errorf("LastSeq is %d, want 2", last)
	}
}

// TestAppendEventsEventIDConflict is the mirror case: a known event id comes
// back under another sequence number.
func TestAppendEventsEventIDConflict(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	if _, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 1)}); err != nil {
		t.Fatal(err)
	}

	moved := testEvent(2, 1) // seq 2, but the event id of seq 1
	if _, err := s.AppendEvents(ctx, "t-1", []Event{moved}); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("AppendEvents returned %v, want ErrEventConflict", err)
	}
	if rows := countEvents(t, s); rows != 1 {
		t.Errorf("the journal holds %d events, want 1", rows)
	}

	// The same event id under another device is a conflict as well.
	if err := s.UpsertDevice(ctx, testDevice("t-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvents(ctx, "t-2", []Event{testEvent(1, 1)}); !errors.Is(err, ErrEventConflict) {
		t.Errorf("the id of another device returned %v, want ErrEventConflict", err)
	}
}

func TestAppendEventsRejectsBadInput(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	zeroID := testEvent(1, 1)
	zeroID.ID = EventID{}
	noKind := testEvent(1, 1)
	noKind.Kind = ""
	noPayload := testEvent(1, 1)
	noPayload.Payload = nil

	tests := []struct {
		name     string
		deviceID string
		event    Event
	}{
		{name: "no device", deviceID: "", event: testEvent(1, 1)},
		{name: "seq zero", deviceID: "t-1", event: testEvent(0, 1)},
		{name: "empty event id", deviceID: "t-1", event: zeroID},
		{name: "empty kind", deviceID: "t-1", event: noKind},
		{name: "nil payload", deviceID: "t-1", event: noPayload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.AppendEvents(ctx, tt.deviceID, []Event{tt.event}); err == nil {
				t.Error("AppendEvents accepted it")
			}
		})
	}
	if rows := countEvents(t, s); rows != 0 {
		t.Errorf("the journal holds %d events, want none", rows)
	}
}

// TestAppendEventsUnknownDevice proves the foreign key is enforced.
func TestAppendEventsUnknownDevice(t *testing.T) {
	s := journalStore(t)
	if _, err := s.AppendEvents(t.Context(), "nobody", []Event{testEvent(1, 1)}); err == nil {
		t.Error("AppendEvents wrote an event for a device that does not exist")
	}
}

func TestAppendEventsEmptyBatch(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	if _, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 1)}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AppendEvents(ctx, "t-1", nil)
	if err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	want := AppendResult{AckSeq: 1}
	if got != want {
		t.Errorf("empty batch returned %+v, want %+v", got, want)
	}
}

func TestLastSeqWithoutEvents(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	last, err := s.LastSeq(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if last != 0 {
		t.Errorf("LastSeq is %d for a device without events, want 0", last)
	}

	// A journal that starts at 2 acknowledges nothing.
	if _, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(2, 2)}); err != nil {
		t.Fatal(err)
	}
	last, err = s.LastSeq(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if last != 0 {
		t.Errorf("LastSeq is %d while seq 1 is missing, want 0", last)
	}
}

func TestListEventsFilters(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	if err := s.UpsertDevice(ctx, testDevice("t-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, scenario, room, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"s-1", "range", "hall", "running", 1, 1); err != nil {
		t.Fatal(err)
	}

	// Two devices, two controllers, two kinds, one session.
	first := testEvent(1, 1)
	first.ControllerID = "c-1"
	first.SessionID = "s-1"
	second := testEvent(2, 2)
	second.Kind = "miss"
	second.ControllerID = "c-2"
	second.SessionID = "s-1"
	third := testEvent(3, 3)
	third.ControllerID = "c-1"
	if _, err := s.AppendEvents(ctx, "t-1", []Event{first, second, third}); err != nil {
		t.Fatal(err)
	}

	other := testEvent(1, 10)
	other.ControllerID = "c-1"
	later := s.now().Add(time.Second)
	s.now = testClock(later)
	if _, err := s.AppendEvents(ctx, "t-2", []Event{other}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		filter Filter
		want   []EventID
	}{
		{name: "everything", filter: Filter{}, want: []EventID{eventID(1), eventID(2), eventID(3), eventID(10)}},
		{name: "by device", filter: Filter{DeviceID: "t-2"}, want: []EventID{eventID(10)}},
		{name: "by session", filter: Filter{SessionID: "s-1"}, want: []EventID{eventID(1), eventID(2)}},
		{name: "by kind", filter: Filter{Kind: "miss"}, want: []EventID{eventID(2)}},
		{name: "by controller", filter: Filter{ControllerID: "c-1"}, want: []EventID{eventID(1), eventID(3), eventID(10)}},
		{name: "by device and controller", filter: Filter{DeviceID: "t-1", ControllerID: "c-1"}, want: []EventID{eventID(1), eventID(3)}},
		{name: "from the later timestamp", filter: Filter{From: later.UnixMilli()}, want: []EventID{eventID(10)}},
		{name: "before the later timestamp", filter: Filter{To: later.UnixMilli()}, want: []EventID{eventID(1), eventID(2), eventID(3)}},
		{name: "limit", filter: Filter{Limit: 2}, want: []EventID{eventID(1), eventID(2)}},
		{name: "limit and offset", filter: Filter{Limit: 2, Offset: 2}, want: []EventID{eventID(3), eventID(10)}},
		{name: "offset past the end", filter: Filter{Offset: 10}, want: nil},
		{name: "nothing matches", filter: Filter{Kind: "health"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := s.ListEvents(ctx, tt.filter)
			if err != nil {
				t.Fatalf("ListEvents: %v", err)
			}
			if len(events) != len(tt.want) {
				t.Fatalf("ListEvents returned %d events, want %d", len(events), len(tt.want))
			}
			for i, want := range tt.want {
				if events[i].ID != want {
					t.Errorf("event %d is %s, want %s", i, events[i].ID, want)
				}
			}
		})
	}
}

func TestNewEventIDIsVersion4AndUnique(t *testing.T) {
	seen := map[EventID]bool{}
	for range 1000 {
		id, err := NewEventID()
		if err != nil {
			t.Fatalf("NewEventID: %v", err)
		}
		if id.IsZero() {
			t.Fatal("NewEventID returned the empty id")
		}
		if id[6]&0xf0 != 0x40 {
			t.Errorf("id %s does not carry version 4", id)
		}
		if id[8]&0xc0 != 0x80 {
			t.Errorf("id %s does not carry the RFC 9562 variant", id)
		}
		if seen[id] {
			t.Fatalf("NewEventID returned %s twice", id)
		}
		seen[id] = true
	}
}

func TestEventIDString(t *testing.T) {
	id := eventID(0xab)
	want := "00000000-0000-4000-8000-0000000000ab"
	if got := id.String(); got != want {
		t.Errorf("String is %q, want %q", got, want)
	}
}

// TestAppendTenThousandEvents measures one transaction of 10,000 events. It
// reports the time and asserts nothing about it, because the number belongs to
// the machine, not to the code.
func TestAppendTenThousandEvents(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	const count = 10_000
	batch := make([]Event, 0, count)
	for i := 1; i <= count; i++ {
		var id EventID
		id[0] = byte(i >> 24)
		id[1] = byte(i >> 16)
		id[2] = byte(i >> 8)
		id[3] = byte(i)
		id[6] = (id[6] & 0x0f) | 0x40
		id[8] = (id[8] & 0x3f) | 0x80
		batch = append(batch, Event{
			ID:       id,
			Seq:      uint64(i),
			Kind:     "shot",
			Payload:  []byte{0xa1, 0x63, 0x63, 0x74, 0x6c, 0x61, 0x78},
			TsDevice: int64(i),
		})
	}

	start := time.Now()
	result, err := s.AppendEvents(ctx, "t-1", batch)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	if result.Stored != count || result.AckSeq != count {
		t.Errorf("result is %+v, want %d stored and AckSeq %d", result, count, count)
	}
	t.Logf("appended %d events in one transaction in %s (%.0f events per second)",
		count, elapsed.Round(time.Millisecond), float64(count)/elapsed.Seconds())
}
