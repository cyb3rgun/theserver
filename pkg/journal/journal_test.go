package journal_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/cyb3rgun/theserver/pkg/journal"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// draft is an event before the journal numbers it, as a device hands one in.
func draft(t *testing.T, kind string, ctl string, cseq uint64) journal.Draft {
	t.Helper()
	data, err := cbor.Marshal(protocol.ShotData{Ctl: ctl, Cseq: cseq})
	if err != nil {
		t.Fatal(err)
	}
	return journal.Draft{Kind: kind, Data: data}
}

func TestJournalSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	j, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
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

	// The process ends here; a new one opens the same directory.
	reopened, err := journal.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.LastSeq() != 5 {
		t.Errorf("last seq after the restart is %d, want 5", reopened.LastSeq())
	}
	replay := reopened.After(2)
	if len(replay) != 3 {
		t.Fatalf("the replay after the restart holds %d events, want 3", len(replay))
	}
	for i, event := range replay {
		want := written[i+2]
		if event.ID != want.ID || event.Seq != want.Seq || event.K != want.K || event.Ts != want.Ts || !bytes.Equal(event.D, want.D) {
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

func TestJournalRefusesACorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, journal.FileName), []byte("not cbor"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Open(dir); err == nil {
		t.Error("a corrupt journal was opened")
	}
}

func TestJournalEpoch(t *testing.T) {
	dir := t.TempDir()
	j, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
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

	reopened, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Epoch() != 2 || reopened.LastSeq() != 0 || reopened.Pending() != 0 {
		t.Fatalf("after the reset the journal is epoch %d, last %d, %d pending", reopened.Epoch(), reopened.LastSeq(), reopened.Pending())
	}
	next, err := reopened.Append(draft(t, protocol.KindShot, "ctl-01", 5), 2)
	if err != nil || next.Seq != 1 {
		t.Errorf("the first event of epoch 2 has seq %d, %v; want 1", next.Seq, err)
	}
	if err := reopened.Acked(1); err != nil || reopened.Epoch() != 2 {
		t.Errorf("an ack moved the epoch to %d, %v", reopened.Epoch(), err)
	}
}
