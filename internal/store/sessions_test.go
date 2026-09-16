package store

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestCreateAndGetSession(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	if err := s.CreateSession(ctx, Session{ID: "s-1", Scenario: "range", Room: "hall"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := s.GetSession(ctx, "s-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.State != SessionCreated || got.Scenario != "range" || got.Room != "hall" {
		t.Errorf("GetSession returned %+v", got)
	}
	if got.StartedAt != 0 || got.EndedAt != 0 {
		t.Errorf("a new session has started_at %d and ended_at %d", got.StartedAt, got.EndedAt)
	}
	if got.CreatedAt == 0 || got.Devices != nil {
		t.Errorf("created_at %d, devices %v", got.CreatedAt, got.Devices)
	}

	if err := s.CreateSession(ctx, Session{ID: "s-1"}); err == nil {
		t.Error("CreateSession accepted an id that exists")
	}
	if err := s.CreateSession(ctx, Session{}); err == nil {
		t.Error("CreateSession accepted an empty id")
	}
	if _, err := s.GetSession(ctx, "nothing"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("GetSession of an unknown id returned %v, want ErrSessionNotFound", err)
	}
}

func TestSessionTransitions(t *testing.T) {
	type step struct {
		name    string
		apply   func(*Store, string) error
		wantErr error
		state   string
	}
	start := func(s *Store, id string) error { return s.StartSession(t.Context(), id) }
	stop := func(s *Store, id string) error { return s.StopSession(t.Context(), id) }

	tests := []struct {
		name  string
		steps []step
	}{
		{name: "created, running, stopped", steps: []step{
			{name: "start", apply: start, state: SessionRunning},
			{name: "stop", apply: stop, state: SessionStopped},
		}},
		{name: "stop before start", steps: []step{
			{name: "stop", apply: stop, wantErr: ErrBadTransition, state: SessionCreated},
		}},
		{name: "start twice", steps: []step{
			{name: "start", apply: start, state: SessionRunning},
			{name: "start again", apply: start, wantErr: ErrBadTransition, state: SessionRunning},
		}},
		{name: "restart a stopped session", steps: []step{
			{name: "start", apply: start, state: SessionRunning},
			{name: "stop", apply: stop, state: SessionStopped},
			{name: "start again", apply: start, wantErr: ErrBadTransition, state: SessionStopped},
			{name: "stop again", apply: stop, wantErr: ErrBadTransition, state: SessionStopped},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTest(t)
			if err := s.CreateSession(t.Context(), Session{ID: "s-1"}); err != nil {
				t.Fatal(err)
			}
			for _, st := range tt.steps {
				err := st.apply(s, "s-1")
				if st.wantErr == nil && err != nil {
					t.Fatalf("%s: %v", st.name, err)
				}
				if st.wantErr != nil && !errors.Is(err, st.wantErr) {
					t.Fatalf("%s returned %v, want %v", st.name, err, st.wantErr)
				}
				got, err := s.GetSession(t.Context(), "s-1")
				if err != nil {
					t.Fatal(err)
				}
				if got.State != st.state {
					t.Fatalf("after %s the state is %q, want %q", st.name, got.State, st.state)
				}
			}
		})
	}

	s := openTest(t)
	if err := s.StartSession(t.Context(), "nothing"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("StartSession of an unknown id returned %v, want ErrSessionNotFound", err)
	}
	if err := s.StopSession(t.Context(), "nothing"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("StopSession of an unknown id returned %v, want ErrSessionNotFound", err)
	}
}

func TestSessionTimestamps(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.CreateSession(ctx, Session{ID: "s-1"}); err != nil {
		t.Fatal(err)
	}

	s.now = testClock(time.UnixMilli(1_700_000_010_000))
	if err := s.StartSession(ctx, "s-1"); err != nil {
		t.Fatal(err)
	}
	s.now = testClock(time.UnixMilli(1_700_000_020_000))
	if err := s.StopSession(ctx, "s-1"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSession(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.StartedAt != 1_700_000_010_000 || got.EndedAt != 1_700_000_020_000 {
		t.Errorf("started_at %d and ended_at %d, want the clock at start and stop", got.StartedAt, got.EndedAt)
	}
	if got.UpdatedAt != 1_700_000_020_000 {
		t.Errorf("updated_at is %d, want the time of the stop", got.UpdatedAt)
	}
}

func TestAddSessionDeviceAndList(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	for _, id := range []string{"t-2", "t-1"} {
		if err := s.UpsertDevice(ctx, testDevice(id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"s-1", "s-2"} {
		if err := s.CreateSession(ctx, Session{ID: id}); err != nil {
			t.Fatal(err)
		}
	}

	for _, add := range [][2]string{{"s-1", "t-2"}, {"s-1", "t-1"}, {"s-1", "t-1"}, {"s-2", "t-1"}} {
		if err := s.AddSessionDevice(ctx, add[0], add[1]); err != nil {
			t.Fatalf("AddSessionDevice %v: %v", add, err)
		}
	}
	if err := s.AddSessionDevice(ctx, "nothing", "t-1"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("an unknown session returned %v, want ErrSessionNotFound", err)
	}
	if err := s.AddSessionDevice(ctx, "s-1", "nobody"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("an unknown device returned %v, want ErrDeviceNotFound", err)
	}

	got, err := s.GetSession(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Devices, []string{"t-1", "t-2"}) {
		t.Errorf("s-1 holds %v, want [t-1 t-2] once each", got.Devices)
	}

	sessions, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].ID != "s-1" || sessions[1].ID != "s-2" {
		t.Fatalf("ListSessions returned %+v", sessions)
	}
	if !slices.Equal(sessions[1].Devices, []string{"t-1"}) {
		t.Errorf("s-2 lists %v, want [t-1]", sessions[1].Devices)
	}
}

func TestListSessionsEmpty(t *testing.T) {
	s := openTest(t)
	sessions, err := s.ListSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("ListSessions returned %d sessions on an empty store", len(sessions))
	}
}

func TestRunningSessionFor(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.UpsertDevice(ctx, testDevice("t-1")); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.RunningSessionFor(ctx, "t-1"); err != nil || ok {
		t.Fatalf("a device without sessions has a running session: %v, %v", ok, err)
	}

	for _, id := range []string{"early", "late", "idle"} {
		if err := s.CreateSession(ctx, Session{ID: id}); err != nil {
			t.Fatal(err)
		}
		if err := s.AddSessionDevice(ctx, id, "t-1"); err != nil {
			t.Fatal(err)
		}
	}
	s.now = testClock(time.UnixMilli(1_000))
	if err := s.StartSession(ctx, "early"); err != nil {
		t.Fatal(err)
	}
	s.now = testClock(time.UnixMilli(2_000))
	if err := s.StartSession(ctx, "late"); err != nil {
		t.Fatal(err)
	}

	id, ok, err := s.RunningSessionFor(ctx, "t-1")
	if err != nil || !ok || id != "late" {
		t.Errorf("RunningSessionFor returned %q, %v, %v, want the latest started", id, ok, err)
	}

	if err := s.StopSession(ctx, "late"); err != nil {
		t.Fatal(err)
	}
	id, ok, err = s.RunningSessionFor(ctx, "t-1")
	if err != nil || !ok || id != "early" {
		t.Errorf("after the stop RunningSessionFor returned %q, %v, %v, want early", id, ok, err)
	}
}

// TestEventsAreAttributedByDeviceTime covers D-021: an event without a session
// belongs to the session of its device that covered its device time, also when
// it arrives late, as a replay does.
func TestEventsAreAttributedByDeviceTime(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()
	if err := s.UpsertDevice(ctx, testDevice("t-2")); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"first", "second"} {
		if err := s.CreateSession(ctx, Session{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddSessionDevice(ctx, "first", "t-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSessionDevice(ctx, "second", "t-1"); err != nil {
		t.Fatal(err)
	}

	// first runs from 10,000 to 20,000, second from 30,000 on.
	s.now = testClock(time.UnixMilli(10_000))
	if err := s.StartSession(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	s.now = testClock(time.UnixMilli(20_000))
	if err := s.StopSession(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	s.now = testClock(time.UnixMilli(30_000))
	if err := s.StartSession(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	// Everything arrives later, as a replay would.
	s.now = testClock(time.UnixMilli(99_000))

	at := func(seq uint64, n byte, ts int64) Event {
		e := testEvent(seq, n)
		e.TsDevice = ts
		return e
	}
	explicit := at(6, 6, 15_000)
	explicit.SessionID = "second"

	events := []Event{
		at(1, 1, 9_999),  // before first
		at(2, 2, 10_000), // first starts, inclusive
		at(3, 3, 19_999), // inside first
		at(4, 4, 20_000), // first has ended, exclusive
		at(5, 5, 45_000), // second is still running
		explicit,         // the caller's session wins
	}
	if _, err := s.AppendEvents(ctx, "t-1", events); err != nil {
		t.Fatal(err)
	}
	// A device that is in no session gets none, whatever the time.
	if _, err := s.AppendEvents(ctx, "t-2", []Event{at(1, 21, 15_000)}); err != nil {
		t.Fatal(err)
	}

	stored, err := s.ListEvents(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[EventID]string{
		eventID(1): "", eventID(2): "first", eventID(3): "first", eventID(4): "",
		eventID(5): "second", eventID(6): "second", eventID(21): "",
	}
	if len(stored) != len(want) {
		t.Fatalf("stored %d events, want %d", len(stored), len(want))
	}
	for _, e := range stored {
		if e.SessionID != want[e.ID] {
			t.Errorf("event %s at device time %d has session %q, want %q", e.ID, e.TsDevice, e.SessionID, want[e.ID])
		}
	}
}
