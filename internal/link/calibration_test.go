package link

import (
	"context"
	"errors"
	"strings"
	"testing"

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

// A device of a target type is told where its beacons sit as soon as it
// connects, with set_config and the key calib.rect (D-063).
func TestCalibrationOnConnect(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	h.isOf("tgt-01", "bar-12")

	c := h.dial(token)
	c.hello("tgt-01", 0)
	seen := c.answers(t)

	cmd := waitFor(t, seen, protocol.CommandSetConfig)
	var setting protocol.SetConfig
	if err := protocol.DecodeArgs(cmd.A, &setting); err != nil {
		t.Fatalf("set_config arguments %v: %v", cmd.A, err)
	}
	if setting.K != protocol.ConfigCalibRect {
		t.Errorf("the device was told the setting %q", setting.K)
	}
	if setting.V != "-138,-340 1618,-340 1618,660 -138,660" {
		t.Errorf("the device was told the rectangle %q", setting.V)
	}
}

// A device without a type, and one whose type has no rectangle, are told
// nothing; an offline device is told when it connects.
func TestCalibrationSaysWhenThereIsNothingToSend(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-02")
	ctx := context.Background()

	if calib, sent, err := h.link.SendCalibration(ctx, "tgt-02"); err != nil || sent || calib.TargetType != "" {
		t.Errorf("a device without a type gave %+v, %v, %v", calib, sent, err)
	}

	// A type whose clusters sit on one line spans no rectangle.
	flat, err := h.store.CreateTargetType(ctx, store.TargetType{
		ID: "flat-1", Name: map[string]string{"en": "Flat"}, Class: store.ClassPi,
		DisplayW: 100, DisplayH: 50, ResW: 800, ResH: 400,
		Orientation: store.Landscape, Sound: store.SoundNone,
		Beacons: []store.Beacon{{X: 0, Y: 0}, {X: 0, Y: 50}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.isOf("tgt-02", flat.ID)
	if _, sent, err := h.link.SendCalibration(ctx, "tgt-02"); err != nil || sent {
		t.Errorf("a type without a rectangle gave %v, %v", sent, err)
	}

	// A device of a usable type that is not connected is told later.
	h.isOf("tgt-02", "board-10")
	calib, sent, err := h.link.SendCalibration(ctx, "tgt-02")
	if !errors.Is(err, ErrDeviceOffline) || sent {
		t.Fatalf("an offline device gave %v, %v", sent, err)
	}
	if calib.Value() != "0,0 1280,0 1280,800 0,800" {
		t.Errorf("the rectangle of board-10 is %q", calib.Value())
	}

	c := h.dial(token)
	c.hello("tgt-02", 0)
	seen := c.answers(t)
	cmd := waitFor(t, seen, protocol.CommandSetConfig)
	if value, _ := cmd.A["v"].(string); !strings.HasPrefix(value, "0,0 1280,0") {
		t.Errorf("the device that connected was told %v", cmd.A)
	}
}
