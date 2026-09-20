package journal_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/cyb3rgun/theserver/pkg/journal"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// appends is how many events the timing test writes. The measurement of the
// briefing is 10,000, which takes about half a minute with synchronous FULL;
// the default keeps the suite quick, and the report runs
// go test ./pkg/journal -run Timing -v -appends=10000.
var appends = flag.Int("appends", 1000, "how many events the timing test appends")

// draft is an event before the journal numbers it, as a device hands one in.
func draft(t *testing.T, kind string, ctl string, cseq uint64) journal.Draft {
	t.Helper()
	data, err := cbor.Marshal(protocol.ShotData{Ctl: ctl, Cseq: cseq})
	if err != nil {
		t.Fatal(err)
	}
	return journal.Draft{Kind: kind, Data: data}
}

// open opens a journal and closes it when the test ends.
func open(t *testing.T, dir string, opts ...journal.Option) *journal.Journal {
	t.Helper()
	j, err := journal.Open(dir, opts...)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

func TestJournalSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	j := open(t, dir)
	if j.LastSeq() != 0 || j.Pending() != 0 {
		t.Fatalf("a new journal has last %d and %d pending", j.LastSeq(), j.Pending())
	}

	var written []protocol.Event
	for i := range uint64(5) {
		event, err := j.Append(draft(t, protocol.KindShot, "ctl-01", i+1), time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, event)
	}
	if err := j.Acked(2); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The process ends here; a new one opens the same directory.
	reopened := open(t, dir)
	if reopened.LastSeq() != 5 {
		t.Errorf("last seq after the restart is %d, want 5", reopened.LastSeq())
	}
	replay := reopened.After(2)
	if len(replay) != 3 {
		t.Fatalf("the replay after the restart holds %d events, want 3", len(replay))
	}
	for i, event := range replay {
		want := written[i+2]
		if event.ID != want.ID || event.Seq != want.Seq || event.K != want.K || event.Ts != want.Ts ||
			!bytes.Equal(event.D, want.D) || event.T != want.T {
			t.Errorf("replayed event %d is %+v, want %+v", i, event, want)
		}
	}

	// The next event continues the sequence.
	next, err := reopened.Append(draft(t, protocol.KindShot, "ctl-01", 6), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if next.Seq != 6 {
		t.Errorf("the first event after the restart has seq %d, want 6", next.Seq)
	}
	if err := reopened.Acked(6); err != nil {
		t.Fatal(err)
	}
	if reopened.Pending() != 0 {
		t.Errorf("%d events pending after acking everything", reopened.Pending())
	}
}

// A journal that was never closed has its events on disk all the same: the
// commit of Append leaves the process, which is what a killed target needs
// (D-054).
func TestJournalKeepsEventsWithoutAClose(t *testing.T) {
	dir := t.TempDir()
	j, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var written []protocol.Event
	for i := range uint64(40) {
		event, err := j.Append(draft(t, protocol.KindShot, "ctl-01", i+1), int64(i))
		if err != nil {
			t.Fatal(err)
		}
		written = append(written, event)
	}
	// No Close, no checkpoint: the handle is dropped as a killed process
	// drops it, and another journal opens the same files.
	t.Cleanup(func() { j.Close() })

	reopened := open(t, dir)
	if reopened.LastSeq() != 40 || reopened.Pending() != 40 {
		t.Fatalf("after the kill the journal has last %d and %d pending, want 40 and 40",
			reopened.LastSeq(), reopened.Pending())
	}
	for i, event := range reopened.After(0) {
		if event.ID != written[i].ID || event.Seq != written[i].Seq || !bytes.Equal(event.D, written[i].D) {
			t.Fatalf("event %d came back as %+v, want %+v", i, event, written[i])
		}
	}
}

// The image of a machine that lost power, database and write ahead log as
// they lay, opens with every event that was appended (D-054).
func TestJournalSurvivesACopiedWalFile(t *testing.T) {
	dir := t.TempDir()
	j, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for i := range uint64(25) {
		if _, err := j.Append(draft(t, protocol.KindHealth, "ctl-01", i+1), int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Acked(5); err != nil {
		t.Fatal(err)
	}

	// The copy is taken while the journal is open and nothing was
	// checkpointed, so the write ahead log carries most of the events.
	image := t.TempDir()
	wal := filepath.Join(dir, journal.FileName+"-wal")
	if info, err := os.Stat(wal); err != nil || info.Size() == 0 {
		t.Fatalf("there is no write ahead log to copy: %v", err)
	}
	for _, name := range []string{journal.FileName, journal.FileName + "-wal", journal.FileName + "-shm"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(image, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	restored := open(t, image)
	if restored.LastSeq() != 25 {
		t.Errorf("the image counts to %d, want 25", restored.LastSeq())
	}
	if restored.Pending() != 20 {
		t.Errorf("the image holds %d unacknowledged events, want 20", restored.Pending())
	}
	for i, event := range restored.After(0) {
		if event.Seq != uint64(i+6) || event.K != protocol.KindHealth {
			t.Fatalf("event %d of the image is %+v", i, event)
		}
	}
}

// A journal of an earlier version is read once, its events are kept, and the
// file is renamed so that it is not read again (D-053).
func TestJournalImportsTheCborFile(t *testing.T) {
	dir := t.TempDir()
	legacy := struct {
		Epoch   uint64           `cbor:"epoch,omitempty"`
		LastSeq uint64           `cbor:"last"`
		Pending []protocol.Event `cbor:"pending"`
	}{Epoch: 3, LastSeq: 9}
	for seq := uint64(7); seq <= 9; seq++ {
		id, err := protocol.NewEventID()
		if err != nil {
			t.Fatal(err)
		}
		data, err := cbor.Marshal(protocol.ShotData{Ctl: "ctl-02", Cseq: seq})
		if err != nil {
			t.Fatal(err)
		}
		legacy.Pending = append(legacy.Pending, protocol.Event{
			T: protocol.TypeEvent, ID: id, Seq: seq, K: protocol.KindShot, Ts: int64(seq), D: data,
		})
	}
	data, err := cbor.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, journal.LegacyFileName)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}

	j := open(t, dir)
	if j.Epoch() != 3 || j.LastSeq() != 9 || j.Pending() != 3 {
		t.Fatalf("the imported journal is epoch %d, last %d, %d pending", j.Epoch(), j.LastSeq(), j.Pending())
	}
	for i, event := range j.After(0) {
		want := legacy.Pending[i]
		if event.ID != want.ID || event.Seq != want.Seq || event.K != want.K || event.Ts != want.Ts ||
			!bytes.Equal(event.D, want.D) {
			t.Errorf("imported event %d is %+v, want %+v", i, event, want)
		}
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the imported file is still there: %v", err)
	}
	if _, err := os.Stat(file + ".imported"); err != nil {
		t.Errorf("the imported file was not renamed: %v", err)
	}

	// The next event continues where the old journal stopped, and a second
	// open imports nothing again.
	next, err := j.Append(draft(t, protocol.KindShot, "ctl-02", 10), 10)
	if err != nil || next.Seq != 10 {
		t.Errorf("the first event after the import has seq %d, %v", next.Seq, err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	again := open(t, dir)
	if again.LastSeq() != 10 || again.Pending() != 4 {
		t.Errorf("after reopening the journal is last %d with %d pending", again.LastSeq(), again.Pending())
	}
}

func TestJournalRefusesWhatItCannotRead(t *testing.T) {
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, journal.FileName), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if j, err := journal.Open(broken); err == nil {
		j.Close()
		t.Error("a journal that is not a database was opened")
	}

	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, journal.LegacyFileName), []byte("not cbor"), 0o600); err != nil {
		t.Fatal(err)
	}
	if j, err := journal.Open(legacy); err == nil {
		j.Close()
		t.Error("a legacy journal that is not CBOR was imported")
	}
}

func TestJournalEpoch(t *testing.T) {
	dir := t.TempDir()
	j := open(t, dir)
	if j.Epoch() != 1 {
		t.Errorf("a new journal counts in epoch %d, want 1", j.Epoch())
	}
	for i := range uint64(4) {
		if _, err := j.Append(draft(t, protocol.KindShot, "ctl-01", i+1), 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Acked(1); err != nil {
		t.Fatal(err)
	}

	dropped, err := j.Reset(2)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 3 {
		t.Errorf("Reset dropped %d events, want the 3 unacknowledged", dropped)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := open(t, dir)
	if reopened.Epoch() != 2 || reopened.LastSeq() != 0 || reopened.Pending() != 0 {
		t.Fatalf("after the reset the journal is epoch %d, last %d, %d pending",
			reopened.Epoch(), reopened.LastSeq(), reopened.Pending())
	}
	next, err := reopened.Append(draft(t, protocol.KindShot, "ctl-01", 5), 2)
	if err != nil || next.Seq != 1 {
		t.Errorf("the first event of epoch 2 has seq %d, %v; want 1", next.Seq, err)
	}
	if err := reopened.Acked(1); err != nil || reopened.Epoch() != 2 {
		t.Errorf("an ack moved the epoch to %d, %v", reopened.Epoch(), err)
	}
}

// A journal says which device it belongs to and refuses the journal of
// another one, which is the mistake a shared directory makes.
func TestJournalDeviceID(t *testing.T) {
	dir := t.TempDir()
	j := open(t, dir, journal.WithDeviceID("tgt-01"))
	if j.DeviceID() != "tgt-01" {
		t.Errorf("the journal belongs to %q", j.DeviceID())
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if again, err := journal.Open(dir, journal.WithDeviceID("tgt-02")); err == nil {
		again.Close()
		t.Error("the journal of another device was opened")
	}
	// Without an id the journal opens as it always did.
	open(t, dir)
}

func TestJournalRefusesChangesAfterClose(t *testing.T) {
	dir := t.TempDir()
	j, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(draft(t, protocol.KindShot, "ctl-01", 1), 1); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Errorf("closing twice gave %v", err)
	}
	if _, err := j.Append(draft(t, protocol.KindShot, "ctl-01", 2), 2); err == nil {
		t.Error("a closed journal took an event")
	}
	if err := j.Acked(1); err == nil {
		t.Error("a closed journal took an ack")
	}
	if _, err := j.Reset(2); err == nil {
		t.Error("a closed journal was reset")
	}
}

// The timing of 10,000 appends on this machine, in both modes. It is
// reported, not asserted: a build machine is not a target, and the number is
// here so that theclient can see what durability costs.
func TestJournalAppendTiming(t *testing.T) {
	count := *appends
	if testing.Short() {
		count = 200
	}
	for _, mode := range []journal.Sync{journal.SyncFull, journal.SyncNormal} {
		dir := t.TempDir()
		j := open(t, dir, journal.WithSync(mode))
		d := draft(t, protocol.KindShot, "ctl-01", 1)
		started := time.Now()
		for range count {
			if _, err := j.Append(d, 1); err != nil {
				t.Fatal(err)
			}
		}
		took := time.Since(started)
		info, err := os.Stat(filepath.Join(dir, journal.FileName))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("synchronous=%s: %d appends in %s, %.3f ms each, database %d bytes",
			mode, count, took.Round(time.Millisecond), float64(took.Microseconds())/float64(count)/1000, info.Size())
		if j.Pending() != count {
			t.Errorf("%d events pending after %d appends", j.Pending(), count)
		}
	}
}
