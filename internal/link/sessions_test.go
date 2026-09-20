package link

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fxamacker/cbor/v2"

	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// playing makes a running session with a published scenario version and the
// devices in it, as the API does before it starts one. The session is left
// in the state the caller asks for.
func (h *harness) playing(sessionID, scenarioID string, version int, running bool, devices ...string) store.Session {
	h.t.Helper()
	ctx := context.Background()
	if err := h.store.CreateSession(ctx, store.Session{ID: sessionID, Room: "hall"}); err != nil {
		h.t.Fatal(err)
	}
	for _, device := range devices {
		if err := h.store.AddSessionDevice(ctx, sessionID, device); err != nil {
			h.t.Fatal(err)
		}
	}
	h.assign(sessionID, scenarioID, version)
	if running {
		if err := h.store.StartSession(ctx, sessionID); err != nil {
			h.t.Fatal(err)
		}
	}
	session, err := h.store.GetSession(ctx, sessionID)
	if err != nil {
		h.t.Fatal(err)
	}
	return session
}

// assign publishes a scenario version and gives it to a session, which a
// session needs before it can start (D-058).
func (h *harness) assign(sessionID, scenarioID string, version int) {
	h.t.Helper()
	ctx := context.Background()
	sc := store.Scenario{
		ID: scenarioID, Version: version, Tier: "video",
		Title: map[string]string{"en": scenarioID, "de": scenarioID}, AgeRating: "12",
		ManifestHash: strings.Repeat("a", 64), Size: 1024, UploadedBy: "tests",
	}
	if _, err := h.store.GetScenario(ctx, scenarioID, version); err != nil {
		if err := h.store.PutScenario(ctx, sc); err != nil {
			h.t.Fatal(err)
		}
		if _, err := h.store.PublishScenario(ctx, scenarioID, version); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := h.store.AssignScenario(ctx, sessionID, scenarioID, version); err != nil {
		h.t.Fatal(err)
	}
}

// holds records that a device has a scenario version, as its health report
// would.
func (h *harness) holds(deviceID, scenarioID string, version int) {
	h.t.Helper()
	err := h.store.RecordDeviceScenarios(context.Background(), deviceID,
		[]store.Holding{{ScenarioID: scenarioID, Version: version}})
	if err != nil {
		h.t.Fatal(err)
	}
}

// answers reads frames in the background, answers every command with ok and
// hands the commands to the test.
func (c *client) answers(t *testing.T) <-chan protocol.Command {
	t.Helper()
	seen := make(chan protocol.Command, 8)
	go func() {
		for {
			msg, err := c.read(10 * time.Second)
			if err != nil {
				return
			}
			cmd, ok := msg.(protocol.Command)
			if !ok {
				continue
			}
			frame, _ := protocol.Encode(protocol.Result{ID: cmd.ID, OK: true})
			if err := c.ws.Write(context.Background(), websocket.MessageBinary, frame); err != nil {
				return
			}
			seen <- cmd
		}
	}()
	return seen
}

// waitFor takes commands until one has the name, or the test fails.
func waitFor(t *testing.T, seen <-chan protocol.Command, name string) protocol.Command {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case cmd := <-seen:
			if cmd.N == name {
				return cmd
			}
		case <-deadline:
			t.Fatalf("no %s command arrived", name)
		}
	}
}

// scenarioOf reads the scn argument of a session_start command.
func scenarioOf(t *testing.T, cmd protocol.Command) protocol.Holding {
	t.Helper()
	var args protocol.SessionStart
	if err := protocol.DecodeArgs(cmd.A, &args); err != nil {
		t.Fatalf("session_start arguments %v: %v", cmd.A, err)
	}
	return args.Scn
}

// The welcome says which session runs and what it plays, so a device knows
// what to load without guessing (D-056).
func TestWelcomeCarriesTheSessionScenario(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	alone := h.addDevice("tgt-09")

	c := h.dial(alone)
	welcome := c.hello("tgt-09", 0)
	if welcome.Ses != nil || welcome.Scn != nil {
		t.Errorf("a device without a session got ses %v and scn %v", welcome.Ses, welcome.Scn)
	}
	c.ws.Close(websocket.StatusNormalClosure, "done")

	session := h.playing("evening", "night-range", 2, true, "tgt-01")
	c = h.dial(token)
	welcome = c.hello("tgt-01", 0)
	switch {
	case welcome.Ses == nil || *welcome.Ses != session.ID:
		t.Fatalf("the welcome names the session %v", welcome.Ses)
	case welcome.Scn == nil:
		t.Fatal("the welcome carries no scenario")
	case welcome.Scn.ID != "night-range" || welcome.Scn.Ver != 2:
		t.Errorf("the welcome carries %+v", *welcome.Scn)
	}
}

// Starting a session tells every device what it plays; one that holds the
// scenario is told at once (D-057).
func TestSessionStartTellsTheDeviceWhatItPlays(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	session := h.playing("evening", "night-range", 1, false, "tgt-01")
	h.holds("tgt-01", "night-range", 1)

	c := h.dial(token)
	c.hello("tgt-01", 0)
	seen := c.answers(t)

	ctx := context.Background()
	if err := h.store.StartSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	session, _ = h.store.GetSession(ctx, session.ID)
	states, err := h.link.StartSession(ctx, session)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	cmd := waitFor(t, seen, protocol.CommandSessionStart)
	if cmd.A["ses"] != "evening" {
		t.Errorf("session_start carries the session %v", cmd.A["ses"])
	}
	if scn := scenarioOf(t, cmd); scn.ID != "night-range" || scn.Ver != 1 {
		t.Errorf("session_start carries the scenario %+v", scn)
	}
	if len(states) != 1 || states[0].DeviceID != "tgt-01" || states[0].State != StateStarted {
		t.Fatalf("the start came to %+v", states)
	}
	if again := h.link.SessionStates("evening"); len(again) != 1 || again[0].State != StateStarted {
		t.Errorf("the link remembers %+v", again)
	}

	// Stopping the session tells the device and forgets the state.
	if err := h.link.StopSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	stop := waitFor(t, seen, protocol.CommandSessionStop)
	if stop.A["ses"] != "evening" {
		t.Errorf("session_stop carries %v", stop.A)
	}
	if states := h.link.SessionStates("evening"); len(states) != 0 {
		t.Errorf("after the stop the link still holds %+v", states)
	}
}

// A device that connects while the session runs is told right after the
// welcome (D-057).
func TestLateConnectIsToldWhatItPlays(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-02")
	h.playing("evening", "night-range", 1, true, "tgt-02")
	h.holds("tgt-02", "night-range", 1)

	c := h.dial(token)
	welcome := c.hello("tgt-02", 0)
	if welcome.Scn == nil || welcome.Scn.ID != "night-range" {
		t.Fatalf("the welcome of the late device carries %v", welcome.Scn)
	}
	seen := c.answers(t)
	cmd := waitFor(t, seen, protocol.CommandSessionStart)
	if scn := scenarioOf(t, cmd); scn.ID != "night-range" || scn.Ver != 1 {
		t.Errorf("the late device was told %+v", scn)
	}
	waitState(t, h, "evening", "tgt-02", StateStarted)
}

// A device that does not hold the scenario is announced to first and told to
// start when it reports the version installed; until then it is not ready,
// and after the install timeout it says so (D-059).
func TestSessionStartWaitsForTheInstall(t *testing.T) {
	cfg := testConfig()
	cfg.InstallTimeout = 150 * time.Millisecond
	h := newHarness(t, cfg)
	token := h.addDevice("tgt-03")
	session := h.playing("evening", "night-range", 1, true, "tgt-03")

	c := h.dial(token)
	c.hello("tgt-03", 0)
	seen := c.answers(t)

	ctx := context.Background()
	if _, err := h.link.StartSession(ctx, session); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	announced := waitFor(t, seen, protocol.CommandContentAvailable)
	if announced.A["id"] != "night-range" {
		t.Errorf("the announcement carries %v", announced.A)
	}
	states := h.link.SessionStates("evening")
	if len(states) != 1 || states[0].State != StateWaiting {
		t.Fatalf("while the device installs the link says %+v", states)
	}

	// Long enough without the install and the page calls the device not
	// ready, while the link keeps waiting.
	time.Sleep(200 * time.Millisecond)
	if states := h.link.SessionStates("evening"); len(states) != 1 || states[0].State != StateTimeout {
		t.Fatalf("after the install timeout the link says %+v", states)
	}

	// The device installs the package and reports it; the start follows.
	payload, err := cbor.Marshal(protocol.ContentData{ID: "night-range", Ver: 1, St: protocol.ContentInstalled})
	if err != nil {
		t.Fatal(err)
	}
	id, err := protocol.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	c.send(protocol.Event{ID: id, Seq: 1, K: protocol.KindContent, Ts: time.Now().UnixMilli(), D: payload})

	cmd := waitFor(t, seen, protocol.CommandSessionStart)
	if scn := scenarioOf(t, cmd); scn.ID != "night-range" || scn.Ver != 1 {
		t.Errorf("after the install the device was told %+v", scn)
	}
	waitState(t, h, "evening", "tgt-03", StateStarted)
}

// A device that is offline when the session starts is not lost: the link
// says so, and the device is told when it connects.
func TestSessionStartOfAnOfflineDevice(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-04")
	session := h.playing("evening", "night-range", 1, true, "tgt-04")
	h.holds("tgt-04", "night-range", 1)

	ctx := context.Background()
	states, err := h.link.StartSession(ctx, session)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if len(states) != 1 || states[0].State != StateOffline {
		t.Fatalf("the start of an offline device came to %+v", states)
	}

	c := h.dial(token)
	c.hello("tgt-04", 0)
	seen := c.answers(t)
	waitFor(t, seen, protocol.CommandSessionStart)
	waitState(t, h, "evening", "tgt-04", StateStarted)
}

// waitState waits until the link says a device of a session is in state.
func waitState(t *testing.T, h *harness, sessionID, deviceID, state string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range h.link.SessionStates(sessionID) {
			if s.DeviceID == deviceID && s.State == state {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never reached %s: %+v", deviceID, state, h.link.SessionStates(sessionID))
}
