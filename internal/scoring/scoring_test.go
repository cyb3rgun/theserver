package scoring

import (
	"context"
	"slices"
	"testing"

	"github.com/cyb3rgun/theserver/internal/protocol"
	"github.com/cyb3rgun/theserver/internal/store"
)

type seed struct {
	st  *store.Store
	t   *testing.T
	seq map[string]uint64
	id  byte
}

func newSeed(t *testing.T) *seed {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	for _, dev := range []string{"t-1", "t-2"} {
		if err := st.UpsertDevice(ctx, store.Device{ID: dev, Kind: store.KindTarget}); err != nil {
			t.Fatal(err)
		}
	}
	for _, ses := range []string{"s-1", "s-2"} {
		if err := st.CreateSession(ctx, store.Session{ID: ses}); err != nil {
			t.Fatal(err)
		}
	}
	return &seed{st: st, t: t, seq: map[string]uint64{}}
}

// event builds the next event of a device.
func (s *seed) event(device, session, kind string, ts int64, data any) store.Event {
	s.t.Helper()
	payload, err := protocol.EncodeData(data)
	if err != nil {
		s.t.Fatal(err)
	}
	s.seq[device]++
	s.id++
	var id store.EventID
	id[6], id[8], id[15] = 0x40, 0x80, s.id
	e := store.Event{
		ID: id, Seq: s.seq[device], Kind: kind, SessionID: session,
		TsDevice: ts, Payload: payload,
	}
	if ctl, ok := protocol.ControllerID(payload); ok {
		e.ControllerID = ctl
	}
	return e
}

func (s *seed) append(device string, events ...store.Event) {
	s.t.Helper()
	if _, err := s.st.AppendEvents(context.Background(), device, events); err != nil {
		s.t.Fatal(err)
	}
}

func hit(ctl string, pts int64) protocol.HitData {
	return protocol.HitData{Ctl: ctl, Cseq: 1, X: 0.5, Y: 0.5, Zone: "torso", Pts: pts}
}

func shot(ctl string) protocol.ShotData {
	return protocol.ShotData{Ctl: ctl, Cseq: 1}
}

// seedJournal writes two controllers over two devices and two sessions,
// with a replayed batch, a hit without pts and an event without controller.
func seedJournal(t *testing.T) *store.Store {
	s := newSeed(t)

	s.append("t-1",
		s.event("t-1", "s-1", protocol.KindShot, 1000, shot("c-1")),
		s.event("t-1", "s-1", protocol.KindHit, 1001, hit("c-1", 100)),
		s.event("t-1", "s-1", protocol.KindShot, 1100, shot("c-2")),
		s.event("t-1", "s-1", protocol.KindMiss, 1101, shot("c-2")),
		s.event("t-1", "s-1", protocol.KindShot, 1200, shot("c-1")),
		s.event("t-1", "s-1", protocol.KindHit, 1201, hit("c-1", 50)),
	)

	batch := []store.Event{
		s.event("t-2", "s-2", protocol.KindShot, 2000, shot("c-2")),
		s.event("t-2", "s-2", protocol.KindHit, 2001, hit("c-2", 25)),
		s.event("t-2", "s-2", protocol.KindShot, 2100, shot("c-1")),
		// A hit that carries no pts: it counts as a hit worth nothing.
		s.event("t-2", "s-2", protocol.KindHit, 2101, shot("c-1")),
		s.event("t-2", "s-2", protocol.KindShot, 2200, shot("c-2")),
		s.event("t-2", "s-2", protocol.KindHit, 2201, hit("c-2", 100)),
		// No controller: health is never credited.
		s.event("t-2", "s-2", protocol.KindHealth, 2300, protocol.HealthData{Up: 1}),
	}
	s.append("t-2", batch...)
	// The device replays the whole batch after a dropped connection.
	result, err := s.st.AppendEvents(context.Background(), "t-2", batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Duplicates != len(batch) || result.Stored != 0 {
		t.Fatalf("the replay stored %d and saw %d duplicates", result.Stored, result.Duplicates)
	}
	return s.st
}

func TestRanking(t *testing.T) {
	st := seedJournal(t)

	tests := []struct {
		name  string
		scope Scope
		want  []Entry
	}{
		{
			name:  "all sessions",
			scope: Scope{},
			want: []Entry{
				{ControllerID: "c-1", Hits: 3, Misses: 0, Points: 150, LastHit: 2101},
				{ControllerID: "c-2", Hits: 2, Misses: 1, Points: 125, LastHit: 2201},
			},
		},
		{
			name:  "session one",
			scope: Scope{SessionID: "s-1"},
			want: []Entry{
				{ControllerID: "c-1", Hits: 2, Misses: 0, Points: 150, LastHit: 1201},
				{ControllerID: "c-2", Hits: 0, Misses: 1, Points: 0, LastHit: 0},
			},
		},
		{
			name:  "session two",
			scope: Scope{SessionID: "s-2"},
			want: []Entry{
				{ControllerID: "c-2", Hits: 2, Misses: 0, Points: 125, LastHit: 2201},
				{ControllerID: "c-1", Hits: 1, Misses: 0, Points: 0, LastHit: 2101},
			},
		},
		{
			name:  "unknown session",
			scope: Scope{SessionID: "nothing"},
			want:  []Entry{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Ranking(context.Background(), st, tt.scope)
			if err != nil {
				t.Fatalf("Ranking: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Ranking:\n got  %+v\n want %+v", got, tt.want)
			}
		})
	}
}

func TestRankingOrder(t *testing.T) {
	s := newSeed(t)
	// c-b and c-a tie on points; c-b has more hits. c-c and c-d tie on both.
	s.append("t-1",
		s.event("t-1", "", protocol.KindHit, 1, hit("c-a", 100)),
		s.event("t-1", "", protocol.KindHit, 2, hit("c-b", 50)),
		s.event("t-1", "", protocol.KindHit, 3, hit("c-b", 50)),
		s.event("t-1", "", protocol.KindHit, 4, hit("c-d", 10)),
		s.event("t-1", "", protocol.KindHit, 5, hit("c-c", 10)),
		s.event("t-1", "", protocol.KindHit, 6, hit("c-e", -5)),
	)
	got, err := Ranking(context.Background(), s.st, Scope{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, e := range got {
		order = append(order, e.ControllerID)
	}
	want := []string{"c-b", "c-a", "c-c", "c-d", "c-e"}
	if !slices.Equal(order, want) {
		t.Errorf("order is %v, want %v", order, want)
	}
	if got[4].Points != -5 {
		t.Errorf("a penalty scored %d, want -5", got[4].Points)
	}
}

func TestPointsOf(t *testing.T) {
	encode := func(v any) []byte {
		raw, err := protocol.EncodeData(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	type floatPoints struct {
		Pts float64 `cbor:"pts"`
	}
	tests := []struct {
		name    string
		payload []byte
		want    int
		ok      bool
	}{
		{"hit", encode(hit("c-1", 100)), 100, true},
		{"zero", encode(hit("c-1", 0)), 0, true},
		{"negative", encode(hit("c-1", -25)), -25, true},
		{"no pts", encode(shot("c-1")), 0, false},
		{"pts not an integer", encode(floatPoints{Pts: 1.5}), 0, false},
		{"not a map", []byte{0x83, 0x01, 0x02, 0x03}, 0, false},
		{"empty", nil, 0, false},
		{"garbage", []byte{0xff}, 0, false},
	}
	for _, tt := range tests {
		got, ok := PointsOf(tt.payload)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: PointsOf returned %d, %v, want %d, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
