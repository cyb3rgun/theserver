package simtarget

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/store"
)

// A device that is set to a target type is told where the beacons of that
// type sit as soon as it connects, and the simulated target writes what it
// was told into its log and its stats (D-061, D-063).
func TestSimulatorIsCalibratedOnConnect(t *testing.T) {
	h := newContentHarness(t)
	if err := h.store.SetDeviceTargetType(context.Background(), "tgt-01", "bar-12"); err != nil {
		t.Fatal(err)
	}

	var simLog lockedBuffer
	stop := h.run(t.TempDir(), Options{Logger: slog.New(slog.NewTextHandler(&simLog, nil))})
	want := "-138,-340 1618,-340 1618,660 -138,660"
	h.waitFor("the calibration of the target", func() bool { return strings.Contains(simLog.String(), want) })
	stats := stop()

	if stats.Calib != want {
		t.Errorf("the target holds the rectangle %q, want %q", stats.Calib, want)
	}
	if got := stats.Config["calib.rect"]; got != want {
		t.Errorf("the target holds the setting %q", got)
	}
	t.Log("set_config: " + logLine(t, &simLog, "msg=configured", "key=calib.rect"))
}

// A target with six clusters in a room gets its own point list and its own
// slot, and writes both into its log (D-068).
func TestSimulatorTakesPointsAndPlan(t *testing.T) {
	h := newContentHarness(t)
	ctx := context.Background()
	if _, err := h.store.CreateSite(ctx, store.Site{ID: "hall-1", Name: "Cinema One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CreateRoom(ctx, store.Room{
		ID: "arena", SiteID: "hall-1", Name: "Arena", PeriodMS: 120, Slots: 6,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CreateTargetType(ctx, store.TargetType{
		ID: "hex-24", Name: map[string]string{"en": "Hex target"}, Class: store.ClassPi,
		DisplayW: 600, DisplayH: 300, ResW: 1200, ResH: 600,
		Orientation: store.Landscape, Sound: store.SoundNone,
		Beacons: []store.Beacon{
			{X: 0, Y: 0, Ch: 0}, {X: 300, Y: -20, Ch: 1}, {X: 600, Y: 0, Ch: 2},
			{X: 600, Y: 300, Ch: 3}, {X: 300, Y: 320, Ch: 4}, {X: 0, Y: 300, Ch: 5},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetDeviceTargetType(ctx, "tgt-01", "hex-24"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.PlaceDevice(ctx, "tgt-01", store.Placement{RoomID: "arena", Slot: 4}); err != nil {
		t.Fatal(err)
	}

	var simLog lockedBuffer
	stop := h.run(t.TempDir(), Options{Logger: slog.New(slog.NewTextHandler(&simLog, nil))})
	want := "0,0,0 1,600,-40 2,1200,0 3,1200,600 4,600,640 5,0,600"
	h.waitFor("the point list of the target", func() bool { return strings.Contains(simLog.String(), want) })
	h.waitFor("the slot of the target", func() bool { return strings.Contains(simLog.String(), "beacon.slot") })
	stats := stop()

	settings := map[string]string{
		"calib.pts":        want,
		"calib.rect":       "0,-40 1200,-40 1200,640 0,640",
		"beacon.period_ms": "120",
		"beacon.slots":     "6",
		"beacon.slot":      "4",
	}
	for key, value := range settings {
		if stats.Config[key] != value {
			t.Errorf("the target holds %s = %q, want %q", key, stats.Config[key], value)
		}
	}
	for _, key := range []string{"calib.pts", "beacon.slot"} {
		t.Log("set_config: " + logLine(t, &simLog, "msg=configured", "key="+key))
	}
}

// A device without a target type is told nothing.
func TestSimulatorWithoutATargetTypeIsNotCalibrated(t *testing.T) {
	h := newContentHarness(t)
	var simLog lockedBuffer
	stop := h.run(t.TempDir(), Options{Logger: slog.New(slog.NewTextHandler(&simLog, nil))})
	h.waitFor("the target online", func() bool { return len(h.link.Online()) == 1 })
	stats := stop()

	if stats.Calib != "" || len(stats.Config) != 0 {
		t.Errorf("a target without a type holds %q and %v", stats.Calib, stats.Config)
	}
	if strings.Contains(simLog.String(), "calib.rect") {
		t.Errorf("the target was calibrated anyway:\n%s", simLog.String())
	}
}
