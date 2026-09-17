// Package simtarget is a simulated target: it journals events the way a
// target does, speaks the device link, replays after a dropped connection and
// answers commands, so the whole path can be tested without firmware.
package simtarget

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/cyb3rgun/theserver/internal/protocol"
)

// journalFile is the name of the journal inside its directory.
const journalFile = "journal.cbor"

// A Journal is the persisted memory of the simulated target: the last
// sequence number it handed out and every event the server has not
// acknowledged yet. Each change is written to a temporary file and renamed
// over the journal, so a killed process leaves either the old or the new
// state. It does not fsync; a simulator does not need to survive a power cut.
type Journal struct {
	path string

	mu    sync.Mutex
	state journalState
}

type journalState struct {
	// Epoch is the sequence epoch the journal counts in. A journal written
	// before epochs existed has none and counts in epoch 1.
	Epoch   uint64           `cbor:"epoch,omitempty"`
	LastSeq uint64           `cbor:"last"`
	Pending []protocol.Event `cbor:"pending"`
}

// OpenJournal loads the journal in dir, or starts an empty one.
func OpenJournal(dir string) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("journal directory %s: %w", dir, err)
	}
	j := &Journal{path: filepath.Join(dir, journalFile)}
	data, err := os.ReadFile(j.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return j, nil
	case err != nil:
		return nil, err
	}
	if err := cbor.Unmarshal(data, &j.state); err != nil {
		return nil, fmt.Errorf("journal %s: %w", j.path, err)
	}
	for i, e := range j.state.Pending {
		if e.Seq == 0 || e.Seq > j.state.LastSeq || (i > 0 && e.Seq <= j.state.Pending[i-1].Seq) {
			return nil, fmt.Errorf("journal %s: pending event %d has seq %d", j.path, i, e.Seq)
		}
	}
	return j, nil
}

// Append gives the draft the next sequence number and a new event id,
// persists it and returns the event to send.
func (j *Journal) Append(d Draft, ts int64) (protocol.Event, error) {
	id, err := protocol.NewEventID()
	if err != nil {
		return protocol.Event{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	event := protocol.Event{
		T:   protocol.TypeEvent,
		ID:  id,
		Seq: j.state.LastSeq + 1,
		K:   d.Kind,
		Ts:  ts,
		D:   d.Data,
	}
	next := journalState{
		Epoch:   j.state.Epoch,
		LastSeq: event.Seq,
		Pending: append(slices.Clip(j.state.Pending), event),
	}
	if err := j.save(next); err != nil {
		return protocol.Event{}, err
	}
	j.state = next
	return event, nil
}

// Acked drops every pending event up to seq.
func (j *Journal) Acked(seq uint64) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	keep := 0
	for keep < len(j.state.Pending) && j.state.Pending[keep].Seq <= seq {
		keep++
	}
	if keep == 0 {
		return nil
	}
	next := journalState{
		Epoch:   j.state.Epoch,
		LastSeq: j.state.LastSeq,
		Pending: slices.Clone(j.state.Pending[keep:]),
	}
	if err := j.save(next); err != nil {
		return err
	}
	j.state = next
	return nil
}

// After returns the pending events above seq, in order.
func (j *Journal) After(seq uint64) []protocol.Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	var events []protocol.Event
	for _, e := range j.state.Pending {
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
	return max(j.state.Epoch, 1)
}

// Reset starts the journal over in a new epoch, as a target does when the
// server announces an epoch it does not know: the counter goes back to 0 and
// the unacknowledged events are dropped. It returns how many were dropped.
func (j *Journal) Reset(epoch uint64) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	dropped := len(j.state.Pending)
	next := journalState{Epoch: epoch}
	if err := j.save(next); err != nil {
		return 0, err
	}
	j.state = next
	return dropped, nil
}

// LastSeq is the highest sequence number the journal handed out in its epoch;
// hello carries it as last.
func (j *Journal) LastSeq() uint64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state.LastSeq
}

// Pending is the number of events the server has not acknowledged.
func (j *Journal) Pending() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.state.Pending)
}

func (j *Journal) save(state journalState) error {
	data, err := cbor.Marshal(state)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	// On Windows a scanner or indexer may hold the file for a moment, so the
	// rename gets a few tries.
	for attempt := 1; ; attempt++ {
		err = os.Rename(tmp, j.path)
		if err == nil {
			return nil
		}
		if attempt == 5 {
			return fmt.Errorf("journal: %w", err)
		}
		time.Sleep(time.Duration(attempt) * 10 * time.Millisecond)
	}
}
