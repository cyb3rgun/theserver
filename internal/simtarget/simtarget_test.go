package simtarget

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/journal"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// TestGeneratorProducesValidFrames checks every generated event against the
// protocol codec and the rules of the simulated target.
func TestGeneratorProducesValidFrames(t *testing.T) {
	gen := NewGenerator(3, 42, time.Now().Add(-time.Minute))
	journal, err := journal.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	points := map[string]int64{"head": 100, "torso": 50, "arm": 25, "leg": 25}
	var shot protocol.ShotData
	shots, hits, misses := 0, 0, 0
	used := map[string]bool{}

	for i := range 2000 {
		event, err := journal.Append(gen.Next(), time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		frame, err := protocol.Encode(event)
		if err != nil {
			t.Fatalf("event %d does not encode: %v", i, err)
		}
		decoded, err := protocol.Decode(frame)
		if err != nil {
			t.Fatalf("event %d does not decode: %v", i, err)
		}
		ev := decoded.(protocol.Event)

		switch {
		case i%2 == 0:
			if ev.K != protocol.KindShot {
				t.Fatalf("event %d is %s, want a shot", i, ev.K)
			}
			if err := protocol.DecodeData(ev.D, &shot); err != nil {
				t.Fatal(err)
			}
			used[shot.Ctl] = true
			shots++
		case ev.K == protocol.KindHit:
			var hit protocol.HitData
			if err := protocol.DecodeData(ev.D, &hit); err != nil {
				t.Fatal(err)
			}
			if hit.Ctl != shot.Ctl || hit.Cseq != shot.Cseq {
				t.Fatalf("hit %+v does not belong to shot %+v", hit, shot)
			}
			if want, ok := points[hit.Zone]; !ok || hit.Pts != want {
				t.Fatalf("hit in zone %q scores %d", hit.Zone, hit.Pts)
			}
			if hit.X < 0 || hit.X > 1 || hit.Y < 0 || hit.Y > 1 {
				t.Fatalf("hit at %v, %v is off the face", hit.X, hit.Y)
			}
			hits++
		case ev.K == protocol.KindMiss:
			var miss protocol.ShotData
			if err := protocol.DecodeData(ev.D, &miss); err != nil {
				t.Fatal(err)
			}
			if miss != shot {
				t.Fatalf("miss %+v does not belong to shot %+v", miss, shot)
			}
			misses++
		default:
			t.Fatalf("event %d is %s, want hit or miss after a shot", i, ev.K)
		}
	}

	if shots != 1000 || hits+misses != 1000 {
		t.Errorf("%d shots, %d hits, %d misses", shots, hits, misses)
	}
	if share := float64(hits) / 1000; share < 0.65 || share > 0.75 {
		t.Errorf("%.0f percent of shots hit, want about 70", share*100)
	}
	if !slices.Equal(sortedKeys(used), gen.Controllers()) {
		t.Errorf("controllers used %v, want all of %v", sortedKeys(used), gen.Controllers())
	}

	health := gen.Health(time.Now(), []protocol.Holding{{ID: "night-range", Ver: 1}})
	var data protocol.HealthData
	if health.Kind != protocol.KindHealth || protocol.DecodeData(health.Data, &data) != nil || data.Up < 59 ||
		len(data.Scn) != 1 || data.Scn[0].ID != "night-range" {
		t.Errorf("health draft is %s with %+v", health.Kind, data)
	}
}

func TestAnswer(t *testing.T) {
	tests := []struct {
		name    string
		ok      bool
		restart bool
	}{
		{protocol.CommandTimeMark, true, false},
		{protocol.CommandSessionStart, true, false},
		{protocol.CommandSessionStop, true, false},
		{protocol.CommandReboot, true, true},
		{protocol.CommandSetConfig, false, false},
		{"launch", false, false},
	}
	for _, tt := range tests {
		result, restart := answer(protocol.Command{ID: 9, N: tt.name})
		if result.ID != 9 || result.OK != tt.ok || restart != tt.restart {
			t.Errorf("%s answered %+v, restart %v", tt.name, result, restart)
		}
		if !tt.ok && result.E == "" {
			t.Errorf("%s was refused without a reason", tt.name)
		}
	}
}

// TestRunAgainstTheServer is the whole path in small: the simulated target
// talks to the real link and store, drops its connection deliberately several
// times, and in the end the server holds exactly what the target generated.
func TestRunAgainstTheServer(t *testing.T) {
	if testing.Short() {
		t.Skip("runs for a few seconds")
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token, err := store.NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDevice(context.Background(), store.Device{
		ID: "tgt-01", Kind: store.KindTarget, Status: store.StatusApproved, TokenHash: store.HashToken(token),
	}); err != nil {
		t.Fatal(err)
	}

	var serverLog lockedBuffer
	cfg := link.DefaultConfig()
	cfg.AckInterval = 20 * time.Millisecond
	deviceLink := link.New(st, cfg, slog.New(slog.NewTextHandler(&serverLog, nil)))
	mux := http.NewServeMux()
	mux.Handle(link.Path, deviceLink)
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	defer deviceLink.Close(context.Background())

	commands := make(chan error, 1)
	go func() {
		// The device drops its connection on purpose, so the command is
		// retried until it finds the device online.
		time.Sleep(300 * time.Millisecond)
		deadline := time.Now().Add(2 * time.Second)
		for {
			result, err := deviceLink.SendCommand(context.Background(), "tgt-01", protocol.CommandTimeMark, map[string]any{"now": time.Now().UnixMilli()})
			if errors.Is(err, link.ErrDeviceOffline) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if err == nil && !result.OK {
				err = io.ErrUnexpectedEOF
			}
			commands <- err
			return
		}
	}()

	stats, err := Run(context.Background(), Options{
		Server:      "wss" + strings.TrimPrefix(srv.URL, "https"),
		DeviceID:    "tgt-01",
		Token:       token,
		Insecure:    true,
		Rate:        60,
		Controllers: 3,
		DropEvery:   600 * time.Millisecond,
		Duration:    3 * time.Second,
		Reconnect:   150 * time.Millisecond,
		Health:      time.Second,
		Seed:        1,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, mustJournal(t))
	if err != nil {
		t.Fatalf("Run: %v\nserver log:\n%s", err, serverLog.String())
	}
	if err := <-commands; err != nil {
		t.Errorf("time_mark: %v", err)
	}

	events, err := st.ListEvents(context.Background(), store.Filter{DeviceID: "tgt-01", Limit: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("generated %d, stored %d, frames %d, replayed %d in %d replays, drops %d",
		stats.Generated, len(events), stats.FramesSent, stats.Replayed, stats.Replays, stats.Drops)

	if stats.Unacked != 0 {
		t.Errorf("%d events were never acknowledged", stats.Unacked)
	}
	if uint64(len(events)) != stats.Generated || stats.LastSeq != stats.Generated {
		t.Fatalf("generated %d with last seq %d, the server stored %d", stats.Generated, stats.LastSeq, len(events))
	}
	for i, event := range events {
		if event.Seq != uint64(i+1) {
			t.Fatalf("stored seq %d at position %d: the journal has a gap or a duplicate", event.Seq, i)
		}
	}
	if stats.Drops < 3 {
		t.Errorf("only %d deliberate drops in the run", stats.Drops)
	}
	if stats.Connections != stats.Drops+1 {
		t.Errorf("%d connections for %d drops", stats.Connections, stats.Drops)
	}
	if stats.Commands != 1 {
		t.Errorf("%d commands answered, want 1", stats.Commands)
	}
}

func TestRunStopsOnRefusedToken(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	deviceLink := link.New(st, link.DefaultConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.Handle(link.Path, deviceLink)
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = Run(ctx, Options{
		Server:   "wss" + strings.TrimPrefix(srv.URL, "https"),
		DeviceID: "tgt-01",
		Token:    "wrong",
		Insecure: true,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, mustJournal(t))
	if err == nil || !strings.Contains(err.Error(), ErrUnauthorized.Error()) {
		t.Errorf("Run returned %v, want ErrUnauthorized", err)
	}
	if ctx.Err() != nil {
		t.Error("Run kept retrying a refused token")
	}
}

func mustJournal(t *testing.T) *journal.Journal {
	t.Helper()
	journal, err := journal.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// lockedBuffer collects a log written from many goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRunThroughAReset resets the device in the middle of a run: the target
// learns the new epoch from welcome, starts over at seq 1, and the server holds
// a contiguous journal in the new epoch.
func TestRunThroughAReset(t *testing.T) {
	if testing.Short() {
		t.Skip("runs for a few seconds")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token, err := store.NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDevice(ctx, store.Device{
		ID: "tgt-02", Kind: store.KindTarget, Status: store.StatusApproved, TokenHash: store.HashToken(token),
	}); err != nil {
		t.Fatal(err)
	}

	cfg := link.DefaultConfig()
	cfg.AckInterval = 20 * time.Millisecond
	deviceLink := link.New(st, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.Handle(link.Path, deviceLink)
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	defer deviceLink.Close(ctx)

	go func() {
		time.Sleep(time.Second)
		if _, err := st.ResetDevice(ctx, "tgt-02"); err == nil {
			deviceLink.Disconnect("tgt-02", link.Reset)
		}
	}()

	stats, err := Run(ctx, Options{
		Server:    "wss" + strings.TrimPrefix(srv.URL, "https"),
		DeviceID:  "tgt-02",
		Token:     token,
		Insecure:  true,
		Rate:      50,
		Duration:  2500 * time.Millisecond,
		Reconnect: 100 * time.Millisecond,
		Health:    time.Hour,
		Seed:      2,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, mustJournal(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Logf("%+v", stats)

	if stats.Epoch != 2 || stats.EpochResets != 1 || stats.Unacked != 0 {
		t.Fatalf("the run ended in epoch %d after %d resets with %d unacknowledged", stats.Epoch, stats.EpochResets, stats.Unacked)
	}
	events, err := st.ListEvents(ctx, store.Filter{DeviceID: "tgt-02", Limit: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	var inTwo []uint64
	inOne := 0
	for _, e := range events {
		switch e.SeqEpoch {
		case 1:
			inOne++
		case 2:
			inTwo = append(inTwo, e.Seq)
		default:
			t.Fatalf("event in epoch %d", e.SeqEpoch)
		}
	}
	slices.Sort(inTwo)
	if uint64(len(inTwo)) != stats.LastSeq {
		t.Fatalf("epoch 2 holds %d events, the target counted to %d", len(inTwo), stats.LastSeq)
	}
	for i, seq := range inTwo {
		if seq != uint64(i+1) {
			t.Fatalf("epoch 2 has seq %d at position %d", seq, i)
		}
	}
	if inOne == 0 || len(inTwo) == 0 {
		t.Errorf("epoch 1 holds %d and epoch 2 holds %d events; both should hold some", inOne, len(inTwo))
	}
	if got := uint64(inOne+len(inTwo)) + uint64(stats.Dropped); got != stats.Generated {
		t.Errorf("stored %d plus dropped %d is not the %d generated", inOne+len(inTwo), stats.Dropped, stats.Generated)
	}
}
