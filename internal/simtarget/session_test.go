package simtarget

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/protocol"
	"github.com/cyb3rgun/theserver/pkg/scenario"
	"github.com/cyb3rgun/theserver/pkg/scenario/scenariotest"
)

// The session side end to end (D-056, D-057, D-059): the real link and store
// on one side, the simulated target on the other. What the target plays is
// what the server told it, never a guess.

// plays publishes the scenario version of a package, puts tgt-01 into a
// session that plays it and starts the session in the store. The session is
// returned as the link gets it.
func (h *contentHarness) plays(sessionID string, pkg []byte) store.Session {
	h.t.Helper()
	ctx := context.Background()
	read, err := scenario.ReadZip(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		h.t.Fatal(err)
	}
	manifest := read.Manifest.Scenario
	err = h.store.PutScenario(ctx, store.Scenario{
		ID: manifest.ID, Version: manifest.Version, Tier: manifest.Tier,
		Title: manifest.Title, AgeRating: manifest.AgeRating,
		ManifestHash: scenario.Hash(read.Manifest), Size: int64(len(pkg)), UploadedBy: "tests",
	})
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.store.PublishScenario(ctx, manifest.ID, manifest.Version); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.CreateSession(ctx, store.Session{ID: sessionID, Room: "hall"}); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.AddSessionDevice(ctx, sessionID, "tgt-01"); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.AssignScenario(ctx, sessionID, manifest.ID, manifest.Version); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.StartSession(ctx, sessionID); err != nil {
		h.t.Fatal(err)
	}
	session, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		h.t.Fatal(err)
	}
	return session
}

// state is what the link says about tgt-01 in a session, or the empty state
// while it says nothing.
func (h *contentHarness) state(sessionID string) link.SessionState {
	h.t.Helper()
	for _, state := range h.link.SessionStates(sessionID) {
		if state.DeviceID == "tgt-01" {
			return state
		}
	}
	return link.SessionState{}
}

// logLine is the first line of a log that holds every part, for the report.
func logLine(t *testing.T, logs *lockedBuffer, parts ...string) string {
	t.Helper()
	for _, line := range strings.Split(logs.String(), "\n") {
		found := true
		for _, part := range parts {
			if !strings.Contains(line, part) {
				found = false
				break
			}
		}
		if found {
			return line
		}
	}
	t.Fatalf("no log line holds %v:\n%s", parts, logs.String())
	return ""
}

// A target that connects while the session runs is told what it plays right
// after the welcome, and the welcome already carries it (D-056, D-057).
func TestSimulatorIsToldWhatItPlaysOnLateConnect(t *testing.T) {
	h := newContentHarness(t)
	video := scenariotest.Zip(t, scenariotest.Video)
	h.packages.add("night-range/1", video, true, false)
	session := h.plays("evening", video)
	if err := h.store.RecordDeviceScenarios(context.Background(), "tgt-01",
		[]store.Holding{{ScenarioID: "night-range", Version: 1}}); err != nil {
		t.Fatal(err)
	}

	// The session starts while the target is away: nothing is delivered.
	states, err := h.link.StartSession(context.Background(), session)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if len(states) != 1 || states[0].State != link.StateOffline {
		t.Fatalf("the start of the session came to %+v", states)
	}

	var simLog lockedBuffer
	stop := h.run(t.TempDir(), Options{
		Holdings:   []protocol.Holding{{ID: "night-range", Ver: 1}},
		ContentDir: filepath.Join(t.TempDir(), "content"),
		Logger:     slog.New(slog.NewTextHandler(&simLog, nil)),
	})
	h.waitFor("the start of the late target", func() bool { return h.state("evening").State == link.StateStarted })
	stats := stop()

	if stats.Session != "evening" || stats.Playing != (protocol.Holding{ID: "night-range", Ver: 1}) {
		t.Errorf("the target plays %s %+v", stats.Session, stats.Playing)
	}
	t.Log("welcome:       " + logLine(t, &simLog, "connected", "session=evening", "scenario=night-range"))
	t.Log("session_start: " + logLine(t, &simLog, "msg=playing", "session=evening", "scenario=night-range", "version=1"))
}

// A target that does not hold the version gets the announcement first and
// the start after it reports the package installed; until then the link
// says it is not ready (D-059).
func TestSimulatorGetsTheContentBeforeTheStart(t *testing.T) {
	h := newContentHarness(t)
	video := scenariotest.Zip(t, scenariotest.Video)
	h.packages.add("night-range/1", video, true, false)
	session := h.plays("evening", video)

	var simLog lockedBuffer
	stop := h.run(t.TempDir(), Options{
		ContentDir: filepath.Join(t.TempDir(), "content"),
		Logger:     slog.New(slog.NewTextHandler(&simLog, nil)),
	})
	h.waitFor("the target online", func() bool { return len(h.link.Online()) == 1 })

	if _, err := h.link.StartSession(context.Background(), session); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// The target holds nothing, so the start waits for the install.
	if state := h.state("evening"); state.State != link.StateWaiting {
		t.Fatalf("a target without the content is %+v", state)
	}
	h.waitFor("the installed package", func() bool { return h.held() == "night-range@1" })
	h.waitFor("the start after the install", func() bool { return h.state("evening").State == link.StateStarted })
	stats := stop()

	if stats.Installs != 1 || stats.Refused != 0 {
		t.Errorf("the target installed %d versions and refused %d starts", stats.Installs, stats.Refused)
	}
	if stats.Playing != (protocol.Holding{ID: "night-range", Ver: 1}) {
		t.Errorf("the target plays %+v", stats.Playing)
	}
	t.Log("content_available: " + logLine(t, &simLog, "content installed", "night-range"))
	t.Log("session_start:     " + logLine(t, &simLog, "msg=playing", "session=evening", "scenario=night-range", "version=1"))

	// The stop reaches the target and the link forgets the session.
	if err := h.link.StopSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if states := h.link.SessionStates("evening"); len(states) != 0 {
		t.Errorf("after the stop the link holds %+v", states)
	}
}

// A start for content the target does not hold is refused, and the link
// says why (D-059).
func TestSimulatorRefusesAStartWithoutTheContent(t *testing.T) {
	h := newContentHarness(t)
	video := scenariotest.Zip(t, scenariotest.Video)
	session := h.plays("evening", video)

	var simLog lockedBuffer
	stop := h.run(t.TempDir(), Options{Logger: slog.New(slog.NewTextHandler(&simLog, nil))})
	h.waitFor("the target online", func() bool { return len(h.link.Online()) == 1 })

	// The server thinks the target holds the version, so it starts it
	// without an announcement.
	if err := h.store.RecordDeviceScenarios(context.Background(), "tgt-01",
		[]store.Holding{{ScenarioID: "night-range", Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.link.StartSession(context.Background(), session); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	state := h.state("evening")
	if state.State != link.StateFailed || !strings.Contains(state.Error, "night-range") {
		t.Errorf("the refused start is %+v", state)
	}
	stats := stop()
	if stats.Refused != 1 || stats.Playing != (protocol.Holding{}) {
		t.Errorf("the target refused %d starts and plays %+v", stats.Refused, stats.Playing)
	}
	t.Log("refused: " + logLine(t, &simLog, "session_start for content the target does not hold"))
}

// A target that has to wait longer than the install timeout is not ready,
// and the link keeps waiting for it (D-059).
func TestSimulatorTooSlowToInstallIsNotReady(t *testing.T) {
	h := newContentHarness(t)
	cfg := h.link.Config()
	cfg.InstallTimeout = 100 * time.Millisecond
	h.link.SetConfig(cfg)

	video := scenariotest.Zip(t, scenariotest.Video)
	session := h.plays("evening", video)
	stop := h.run(t.TempDir(), Options{ContentDir: filepath.Join(t.TempDir(), "content")})
	h.waitFor("the target online", func() bool { return len(h.link.Online()) == 1 })

	// The package is not on the server yet, so the install cannot finish.
	if _, err := h.link.StartSession(context.Background(), session); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	h.waitFor("the install timeout", func() bool { return h.state("evening").State == link.StateTimeout })

	// The package appears and the target is told to play after all.
	h.packages.add("night-range/1", video, true, false)
	if err := h.link.Announce(context.Background(), "tgt-01", announcement(t, video)); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the start after the late install", func() bool { return h.state("evening").State == link.StateStarted })
	stop()
}
