package link

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// isOf sets the target type of a device, as the API does.
func (h *harness) isOf(deviceID, typeID string) {
	h.t.Helper()
	if err := h.store.SetDeviceTargetType(context.Background(), deviceID, typeID); err != nil {
		h.t.Fatal(err)
	}
}

// stands puts a device into a room of a site, making both when they are new.
func (h *harness) stands(deviceID, roomID string, slot int) store.Room {
	h.t.Helper()
	ctx := context.Background()
	if _, err := h.store.GetSite(ctx, "hall-1"); err != nil {
		if _, err := h.store.CreateSite(ctx, store.Site{ID: "hall-1", Name: "Cinema One"}); err != nil {
			h.t.Fatal(err)
		}
	}
	room, err := h.store.GetRoom(ctx, roomID)
	if err != nil {
		if room, err = h.store.CreateRoom(ctx, store.Room{
			ID: roomID, SiteID: "hall-1", Name: strings.ToUpper(roomID[:1]) + roomID[1:], PeriodMS: 120, Slots: 6,
		}); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := h.store.PlaceDevice(ctx, deviceID, store.Placement{RoomID: roomID, Slot: slot}); err != nil {
		h.t.Fatal(err)
	}
	return room
}

// settings reads the set_config commands a client answered, by key.
func settingsOf(t *testing.T, seen <-chan protocol.Command, count int) map[string]string {
	t.Helper()
	out := map[string]string{}
	for range count {
		cmd := waitFor(t, seen, protocol.CommandSetConfig)
		var setting protocol.SetConfig
		if err := protocol.DecodeArgs(cmd.A, &setting); err != nil {
			t.Fatalf("set_config arguments %v: %v", cmd.A, err)
		}
		out[setting.K] = setting.V
	}
	return out
}

// A device that connects is told the points of its type, the rectangle
// around them and the plan of its room with its own slot (D-063, D-068).
func TestSetupOnConnect(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	h.isOf("tgt-01", "bar-12")
	h.stands("tgt-01", "arena", 3)

	c := h.dial(token)
	c.hello("tgt-01", 0)
	seen := c.answers(t)

	got := settingsOf(t, seen, 5)
	want := map[string]string{
		"calib.pts":        "0,-138,-340 1,1618,-340 2,1618,660 3,-138,660",
		"calib.rect":       "-138,-340 1618,-340 1618,660 -138,660",
		"beacon.period_ms": "120",
		"beacon.slots":     "6",
		"beacon.slot":      "3",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("the device was told %s = %q, want %q", key, got[key], value)
		}
	}
}

// A target with six clusters is described as fully as one with four, and a
// change of its type reaches it (D-068).
func TestSetupOfASixClusterTarget(t *testing.T) {
	h := newHarness(t, testConfig())
	ctx := context.Background()
	token := h.addDevice("tgt-06")
	six := store.TargetType{
		ID: "hex-24", Name: map[string]string{"en": "Hex target"}, Class: store.ClassPi,
		DisplayW: 600, DisplayH: 300, ResW: 1200, ResH: 600,
		Orientation: store.Landscape, Sound: store.SoundNone,
		Beacons: []store.Beacon{
			{X: 0, Y: 0, Ch: 0}, {X: 300, Y: -20, Ch: 1}, {X: 600, Y: 0, Ch: 2},
			{X: 600, Y: 300, Ch: 3}, {X: 300, Y: 320, Ch: 4}, {X: 0, Y: 300, Ch: 5},
		},
	}
	if _, err := h.store.CreateTargetType(ctx, six); err != nil {
		t.Fatal(err)
	}
	h.isOf("tgt-06", "hex-24")
	h.stands("tgt-06", "arena", 1)

	c := h.dial(token)
	c.hello("tgt-06", 0)
	seen := c.answers(t)

	got := settingsOf(t, seen, 5)
	if got["calib.pts"] != "0,0,0 1,600,-40 2,1200,0 3,1200,600 4,600,640 5,0,600" {
		t.Errorf("the six clusters came out as %q", got["calib.pts"])
	}
	if got["calib.rect"] != "0,-40 1200,-40 1200,640 0,640" {
		t.Errorf("the box around them is %q", got["calib.rect"])
	}

	// The layout moves, and the device is told again.
	six.Beacons[1].Y = -10
	if _, err := h.store.UpdateTargetType(ctx, six); err != nil {
		t.Fatal(err)
	}
	setup, sent, err := h.link.SendSetup(ctx, "tgt-06")
	if err != nil || !sent {
		t.Fatalf("the change gave %v, %v", sent, err)
	}
	if setup.Calib.PointsValue() != "0,0,0 1,600,-20 2,1200,0 3,1200,600 4,600,640 5,0,600" {
		t.Errorf("after the change the points are %q", setup.Calib.PointsValue())
	}
	again := settingsOf(t, seen, 5)
	if again["calib.pts"] != setup.Calib.PointsValue() {
		t.Errorf("the device was told %q", again["calib.pts"])
	}
}

// A device without a type and without a room is told nothing; one that is
// offline is told when it connects.
func TestSetupSaysWhenThereIsNothingToSend(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-02")
	ctx := context.Background()

	setup, sent, err := h.link.SendSetup(ctx, "tgt-02")
	if err != nil || sent || len(setup.Settings()) != 0 {
		t.Errorf("a bare device gave %+v, %v, %v", setup.Settings(), sent, err)
	}

	// A type whose clusters sit on one line has points and no rectangle.
	flat, err := h.store.CreateTargetType(ctx, store.TargetType{
		ID: "flat-1", Name: map[string]string{"en": "Flat"}, Class: store.ClassPi,
		DisplayW: 100, DisplayH: 50, ResW: 800, ResH: 400,
		Orientation: store.Landscape, Sound: store.SoundNone,
		Beacons: []store.Beacon{{X: 0, Y: 0, Ch: 0}, {X: 0, Y: 50, Ch: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.isOf("tgt-02", flat.ID)
	setup, sent, err = h.link.SendSetup(ctx, "tgt-02")
	if !errors.Is(err, ErrDeviceOffline) || sent {
		t.Fatalf("an offline device gave %v, %v", sent, err)
	}
	if got := setup.Settings(); len(got) != 1 || got[0].Key != store.KeyCalibPoints {
		t.Errorf("a line is sent as %+v", got)
	}

	// It connects and is told what was waiting.
	c := h.dial(token)
	c.hello("tgt-02", 0)
	seen := c.answers(t)
	got := settingsOf(t, seen, 1)
	if got["calib.pts"] != "0,0,0 1,0,400" {
		t.Errorf("the device that connected was told %q", got["calib.pts"])
	}
}

// A device that is moved to another room is told the plan of that room.
func TestSetupFollowsTheRoom(t *testing.T) {
	h := newHarness(t, testConfig())
	ctx := context.Background()
	token := h.addDevice("tgt-03")
	h.isOf("tgt-03", "board-10")
	h.stands("tgt-03", "arena", 2)

	c := h.dial(token)
	c.hello("tgt-03", 0)
	seen := c.answers(t)
	first := settingsOf(t, seen, 5)
	if first["beacon.slot"] != "2" {
		t.Errorf("the device holds slot %q", first["beacon.slot"])
	}

	room, err := h.store.CreateRoom(ctx, store.Room{ID: "lounge", SiteID: "hall-1", Name: "Lounge", PeriodMS: 250, Slots: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.PlaceDevice(ctx, "tgt-03", store.Placement{RoomID: room.ID, Slot: 1}); err != nil {
		t.Fatal(err)
	}
	if _, sent, err := h.link.SendSetup(ctx, "tgt-03"); err != nil || !sent {
		t.Fatalf("the move gave %v, %v", sent, err)
	}
	moved := settingsOf(t, seen, 5)
	switch {
	case moved["beacon.period_ms"] != "250":
		t.Errorf("the period of the new room is %q", moved["beacon.period_ms"])
	case moved["beacon.slots"] != "2":
		t.Errorf("the slots of the new room are %q", moved["beacon.slots"])
	case moved["beacon.slot"] != "1":
		t.Errorf("the slot in the new room is %q", moved["beacon.slot"])
	}
}

// A device that refuses a setting says so, and the link reports it.
func TestSetupRefused(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-04")
	h.isOf("tgt-04", "bar-12")

	c := h.dial(token)
	c.hello("tgt-04", 0)
	go func() {
		for {
			msg, err := c.read(5 * time.Second)
			if err != nil {
				return
			}
			cmd, ok := msg.(protocol.Command)
			if !ok {
				continue
			}
			frame, _ := protocol.Encode(protocol.Result{ID: cmd.ID, OK: false, E: "no such setting"})
			if err := c.ws.Write(context.Background(), websocket.MessageBinary, frame); err != nil {
				return
			}
		}
	}()
	for deadline := time.Now().Add(5 * time.Second); len(h.link.Online()) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the device never came online")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, sent, err := h.link.SendSetup(context.Background(), "tgt-04"); sent || !errors.Is(err, ErrRefused) {
		t.Errorf("a refused setting gave %v, %v", sent, err)
	}
}
