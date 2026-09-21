package simtarget

import (
	"context"
	"log/slog"
	"strings"
	"testing"
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
