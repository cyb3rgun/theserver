package store

import (
	"database/sql"
	"errors"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"
)

// TestResetStartsANewEpoch covers D-026: after a reset the device starts at
// seq 1 again, the ack is counted in the new epoch, and the old events stay.
func TestResetStartsANewEpoch(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	first, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 1), testEvent(2, 2), testEvent(3, 3)})
	if err != nil {
		t.Fatal(err)
	}
	if first.SeqEpoch != 1 || first.AckSeq != 3 {
		t.Fatalf("first batch returned %+v", first)
	}

	epoch, err := s.ResetDevice(ctx, "t-1")
	if err != nil || epoch != 2 {
		t.Fatalf("ResetDevice returned %d, %v", epoch, err)
	}
	gotEpoch, ack, err := s.SeqState(ctx, "t-1")
	if err != nil || gotEpoch != 2 || ack != 0 {
		t.Fatalf("SeqState after the reset is epoch %d ack %d, %v; want 2 and 0", gotEpoch, ack, err)
	}

	// The same sequence numbers again, with new event ids, as a device that
	// lost its counter sends them.
	again, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 11), testEvent(2, 12)})
	if err != nil {
		t.Fatalf("seq 1 and 2 in epoch 2: %v", err)
	}
	if again.SeqEpoch != 2 || again.Stored != 2 || again.AckSeq != 2 {
		t.Errorf("second batch returned %+v", again)
	}

	events, err := s.ListEvents(ctx, Filter{DeviceID: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("the journal holds %d events, want 5", len(events))
	}
	perEpoch := map[uint64]int{}
	for _, e := range events {
		perEpoch[e.SeqEpoch]++
		if e.DeviceID != "t-1" {
			t.Errorf("event %s reads device %q", e.ID, e.DeviceID)
		}
	}
	if perEpoch[1] != 3 || perEpoch[2] != 2 {
		t.Errorf("events per epoch are %v, want 3 in epoch 1 and 2 in epoch 2", perEpoch)
	}
}

func TestEpochConflictsAndDuplicates(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()
	if _, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetDevice(ctx, "t-1"); err != nil {
		t.Fatal(err)
	}

	// An event of epoch 1 sent again in epoch 2 is not a duplicate there.
	if _, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 1)}); !errors.Is(err, ErrEventConflict) {
		t.Errorf("an old event id in the new epoch returned %v, want ErrEventConflict", err)
	}

	// Inside epoch 2 the rules are the ones of B02.
	if _, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 21)}); err != nil {
		t.Fatal(err)
	}
	dup, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 21)})
	if err != nil || dup.Duplicates != 1 {
		t.Errorf("a replay in epoch 2 returned %+v, %v", dup, err)
	}
	if _, err := s.AppendEvents(ctx, "t-1", []Event{testEvent(1, 22)}); !errors.Is(err, ErrSeqConflict) {
		t.Errorf("a reused seq in epoch 2 returned %v, want ErrSeqConflict", err)
	}
}

func TestAppendEventsInEpoch(t *testing.T) {
	s := journalStore(t)
	ctx := t.Context()

	if _, err := s.AppendEventsInEpoch(ctx, "t-1", 1, []Event{testEvent(1, 1)}); err != nil {
		t.Fatalf("append in the current epoch: %v", err)
	}
	if _, err := s.ResetDevice(ctx, "t-1"); err != nil {
		t.Fatal(err)
	}
	// A connection that still believes in epoch 1 flushes late.
	_, err := s.AppendEventsInEpoch(ctx, "t-1", 1, []Event{testEvent(2, 2)})
	if !errors.Is(err, ErrEpochChanged) {
		t.Fatalf("a late flush returned %v, want ErrEpochChanged", err)
	}
	if n := countEvents(t, s); n != 1 {
		t.Errorf("the journal holds %d events, want 1: nothing of the late flush", n)
	}
	if _, err := s.AppendEventsInEpoch(ctx, "t-1", 0, []Event{testEvent(2, 2)}); err == nil {
		t.Error("epoch 0 was accepted")
	}
	if _, err := s.AppendEventsInEpoch(ctx, "nobody", 1, []Event{testEvent(1, 3)}); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("an unknown device returned %v, want ErrDeviceNotFound", err)
	}
}

// TestMigration3KeepsEvents opens a database that stopped at migration 2 with
// events in it, and checks that migration 3 keeps every event in epoch 1.
func TestMigration3KeepsEvents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Build the state of B03 by hand: migrations 1 and 2 only.
	file := filepath.Join(dir, FileName)
	db, err := sql.Open("sqlite", dsn(file, DefaultBusyTimeout))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_init.sql", "0002_device_token_index.sql"} {
		body, err := migrationFS.ReadFile(path.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	mustExec(`INSERT INTO schema_migrations (version, applied_at) VALUES (1, 1), (2, 2)`)
	mustExec(`INSERT INTO devices (id, kind, created_at, updated_at) VALUES ('t-1', 'target', 1, 1)`)
	for seq := 1; seq <= 3; seq++ {
		id := eventID(byte(seq))
		mustExec(`INSERT INTO events (event_id, device_id, seq, kind, controller_id, ts_device, ts_server, payload)
			VALUES (?, 't-1', ?, 'hit', 'c-1', ?, ?, x'a0')`, id[:], seq, seq, seq)
	}
	db.Close()

	s := openTestDir(t, dir, WithClock(testClock(time.UnixMilli(5000))))
	version, err := s.SchemaVersion(t.Context())
	if err != nil || version < 3 {
		t.Fatalf("schema version is %d, %v; want 3 or higher", version, err)
	}
	events, err := s.ListEvents(t.Context(), Filter{DeviceID: "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("%d events survived the migration, want 3", len(events))
	}
	for i, e := range events {
		if e.SeqEpoch != 1 || e.Seq != uint64(i+1) || e.ControllerID != "c-1" || e.ID != eventID(byte(i+1)) {
			t.Errorf("event %d after the migration is %+v", i, e)
		}
	}
	device, err := s.GetDevice(t.Context(), "t-1")
	if err != nil || device.SeqEpoch != 1 {
		t.Errorf("the device reads epoch %d, %v; want 1", device.SeqEpoch, err)
	}
	if ack, err := s.LastSeq(t.Context(), "t-1"); err != nil || ack != 3 {
		t.Errorf("LastSeq after the migration is %d, %v; want 3", ack, err)
	}
	for _, index := range []string{"events_session_controller", "events_device_ts"} {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name); err != nil {
			t.Errorf("index %s is missing after the rebuild: %v", index, err)
		}
	}
}
