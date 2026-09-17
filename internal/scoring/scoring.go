// Package scoring computes rankings from the event journal (D-023). A ranking
// is never stored: every call reads the journal again, so it can never
// disagree with it.
//
// It is the only package outside the device link that looks into event
// payloads (D-024), and it reads nothing from them but pts.
package scoring

import (
	"cmp"
	"context"
	"slices"

	"github.com/fxamacker/cbor/v2"

	"github.com/cyb3rgun/theserver/internal/protocol"
	"github.com/cyb3rgun/theserver/internal/store"
)

// Scope selects the events of a ranking. An empty SessionID means all
// sessions, including events that belong to none.
type Scope struct {
	SessionID string
}

// An Entry is one controller in a ranking. Points is the sum of pts of its
// hits, Hits and Misses count its hit and miss events, and LastHit is the
// device time of its latest hit in unix milliseconds, 0 without a hit.
type Entry struct {
	ControllerID string `json:"controller_id"`
	Hits         int    `json:"hits"`
	Misses       int    `json:"misses"`
	Points       int64  `json:"points"`
	LastHit      int64  `json:"last_hit"`
}

// Journal is the part of the store a ranking reads.
type Journal interface {
	EachEvent(ctx context.Context, f store.Filter, fn func(store.Event) error) error
}

var _ Journal = (*store.Store)(nil)

// Ranking computes the ranking of scope, ordered by points, then hits, both
// descending, then controller id. Events without a controller are left out,
// since they cannot be credited to anyone. A hit without pts counts as a hit
// worth nothing. A replayed event is stored once, so it is counted once.
func Ranking(ctx context.Context, journal Journal, scope Scope) ([]Entry, error) {
	entries := map[string]*Entry{}
	entry := func(controller string) *Entry {
		e, ok := entries[controller]
		if !ok {
			e = &Entry{ControllerID: controller}
			entries[controller] = e
		}
		return e
	}

	hits := store.Filter{SessionID: scope.SessionID, Kind: protocol.KindHit}
	err := journal.EachEvent(ctx, hits, func(ev store.Event) error {
		if ev.ControllerID == "" {
			return nil
		}
		e := entry(ev.ControllerID)
		e.Hits++
		if pts, ok := PointsOf(ev.Payload); ok {
			e.Points += int64(pts)
		}
		e.LastHit = max(e.LastHit, ev.TsDevice)
		return nil
	})
	if err != nil {
		return nil, err
	}

	misses := store.Filter{SessionID: scope.SessionID, Kind: protocol.KindMiss}
	err = journal.EachEvent(ctx, misses, func(ev store.Event) error {
		if ev.ControllerID != "" {
			entry(ev.ControllerID).Misses++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	ranking := make([]Entry, 0, len(entries))
	for _, e := range entries {
		ranking = append(ranking, *e)
	}
	slices.SortFunc(ranking, func(a, b Entry) int {
		return cmp.Or(
			cmp.Compare(b.Points, a.Points),
			cmp.Compare(b.Hits, a.Hits),
			cmp.Compare(a.ControllerID, b.ControllerID),
		)
	})
	return ranking, nil
}

// pointsOnly is the one field a ranking needs from a payload.
type pointsOnly struct {
	Pts *int64 `cbor:"pts"`
}

// PointsOf reads pts from a CBOR payload. It reports false when the payload
// has no integer pts or is not a CBOR map.
func PointsOf(payload []byte) (int, bool) {
	var p pointsOnly
	if err := protocol.DecodeData(cbor.RawMessage(payload), &p); err != nil || p.Pts == nil {
		return 0, false
	}
	return int(*p.Pts), true
}
