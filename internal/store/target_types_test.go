package store

import (
	"context"
	"errors"
	"testing"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The seeds of the migration are there and say what the briefing says
// (D-064).
func TestSeededTargetTypes(t *testing.T) {
	s := openTest(t)
	types, err := s.ListTargetTypes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 3 {
		t.Fatalf("the migration seeded %d types, want 3: %+v", len(types), types)
	}
	want := map[string]struct {
		resW, resH         int
		displayW, displayH float64
		class, sound       string
		beacons            int
	}{
		"bar-12":   {1480, 320, 295, 64, ClassPi, SoundHDMI, 4},
		"board-10": {1280, 800, 217, 136, ClassPi, SoundBuiltin, 4},
		"tv-50":    {1920, 1080, 1107, 623, ClassPC, SoundHDMI, 4},
	}
	for _, got := range types {
		w, ok := want[got.ID]
		if !ok {
			t.Errorf("the migration seeded an unknown type %s", got.ID)
			continue
		}
		switch {
		case got.ResW != w.resW || got.ResH != w.resH:
			t.Errorf("%s has the resolution %d x %d", got.ID, got.ResW, got.ResH)
		case got.DisplayW != w.displayW || got.DisplayH != w.displayH:
			t.Errorf("%s is %g x %g mm", got.ID, got.DisplayW, got.DisplayH)
		case got.Class != w.class || got.Sound != w.sound:
			t.Errorf("%s is class %s with sound %s", got.ID, got.Class, got.Sound)
		case len(got.Beacons) != w.beacons:
			t.Errorf("%s has %d beacon clusters", got.ID, len(got.Beacons))
		case !got.Builtin:
			t.Errorf("%s is not marked builtin", got.ID)
		case got.Name.In("en") == "" || got.Name.In("de") == "" || got.Notes.In("de") == "":
			t.Errorf("%s lacks a text: %+v %+v", got.ID, got.Name, got.Notes)
		case got.Orientation != Landscape:
			t.Errorf("%s is mounted %s", got.ID, got.Orientation)
		}
	}
}

// The rectangle the server sends as calib.rect, computed by hand from the
// seeds (D-063). A millimetre is res/display pixels; the rectangle is the
// bounding box of the clusters, its corners in the order top left, top
// right, bottom right, bottom left.
func TestCalibrationOfTheSeeds(t *testing.T) {
	s := openTest(t)
	cases := []struct {
		id    string
		value string
	}{
		// The clusters sit at the midpoints of the picture edges, so the
		// rectangle is the picture, which is the whole canvas.
		{"board-10", "0,0 1280,0 1280,800 0,800"},
		// A frame of 350 x 200 mm around a picture of 295 x 64 mm: 27.5 mm
		// to the left and right, 68 mm above and below. Across, a
		// millimetre is 1480/295 = 5.0169 px, so 27.5 mm are 138 px and
		// 322.5 mm are 1618 px; down, a millimetre is 320/64 = 5 px, so 68
		// mm are 340 px and 132 mm are 660 px.
		{"bar-12", "-138,-340 1618,-340 1618,660 -138,660"},
		{"tv-50", "0,0 1920,0 1920,1080 0,1080"},
	}
	for _, c := range cases {
		tt, err := s.GetTargetType(t.Context(), c.id)
		if err != nil {
			t.Fatal(err)
		}
		calib, ok := tt.Calibration()
		if !ok {
			t.Errorf("%s has no calibration", c.id)
			continue
		}
		if got := calib.Value(); got != c.value {
			t.Errorf("%s gives %q, want %q", c.id, got, c.value)
		}
		if calib.CanvasW != tt.ResW || calib.CanvasH != tt.ResH || calib.TargetType != c.id {
			t.Errorf("%s gives the canvas %d x %d of %s", c.id, calib.CanvasW, calib.CanvasH, calib.TargetType)
		}
	}
}

// A layout that spans no area, and a type without a size, have no
// calibration; the caller sends nothing then.
func TestCalibrationNeedsARectangle(t *testing.T) {
	base := TargetType{ID: "x", ResW: 100, ResH: 100, DisplayW: 100, DisplayH: 100}
	cases := map[string][]Beacon{
		"one cluster":       {{X: 1, Y: 1}},
		"none":              nil,
		"a vertical line":   {{X: 5, Y: 0}, {X: 5, Y: 90}},
		"a horizontal line": {{X: 0, Y: 5}, {X: 90, Y: 5}},
	}
	for name, beacons := range cases {
		tt := base
		tt.Beacons = beacons
		if _, ok := tt.Calibration(); ok {
			t.Errorf("%s gave a calibration", name)
		}
	}
	sized := base
	sized.Beacons = []Beacon{{X: 0, Y: 0}, {X: 100, Y: 100}}
	sized.DisplayW = 0
	if _, ok := sized.Calibration(); ok {
		t.Error("a type without a display size gave a calibration")
	}
}

// A type is created, read, changed and deleted; a builtin one and one that
// devices are set to stay (D-064).
func TestTargetTypeLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	mine := TargetType{
		ID: "stand-7", Name: scenario.Text{"en": "Phone stand", "de": "Handyhalter"},
		Class: ClassPi, DisplayW: 150, DisplayH: 70, ResW: 1920, ResH: 1080,
		Orientation: Portrait, Sound: SoundUSB,
		Beacons: []Beacon{{X: 0, Y: 0}, {X: 150, Y: 0}, {X: 150, Y: 70}, {X: 0, Y: 70}},
		Notes:   scenario.Text{"en": "A phone in a stand.", "de": "Ein Telefon im Halter."},
		Builtin: true, // ignored: only the migration writes builtin types
	}
	created, err := s.CreateTargetType(ctx, mine)
	if err != nil {
		t.Fatal(err)
	}
	if created.Builtin {
		t.Error("a created type came back builtin")
	}
	if created.CreatedAt == 0 || created.UpdatedAt == 0 {
		t.Errorf("the created type has no times: %+v", created)
	}
	if _, err := s.CreateTargetType(ctx, mine); !errors.Is(err, ErrTargetTypeExists) {
		t.Errorf("a second type with the same id gave %v", err)
	}

	created.Sound = SoundNone
	created.Name = scenario.Text{"en": "Phone stand, quiet", "de": "Handyhalter, leise"}
	updated, err := s.UpdateTargetType(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Sound != SoundNone || updated.Name.In("de") != "Handyhalter, leise" {
		t.Errorf("the update gave %+v", updated)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Error("the update moved created_at")
	}

	// A device is set to the type, so it cannot be deleted.
	if err := s.UpsertDevice(ctx, Device{ID: "tgt-01", Kind: KindTarget}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceTargetType(ctx, "tgt-01", "stand-7"); err != nil {
		t.Fatal(err)
	}
	device, err := s.GetDevice(ctx, "tgt-01")
	if err != nil || device.TargetType != "stand-7" {
		t.Fatalf("the device is %+v, %v", device, err)
	}
	if err := s.DeleteTargetType(ctx, "stand-7"); !errors.Is(err, ErrTargetTypeInUse) {
		t.Errorf("deleting a used type gave %v", err)
	}
	if devices, err := s.DevicesOfTargetType(ctx, "stand-7"); err != nil || len(devices) != 1 || devices[0] != "tgt-01" {
		t.Errorf("the devices of the type are %v, %v", devices, err)
	}

	// The device lets the type go, then it can be deleted; a builtin one
	// cannot.
	if err := s.SetDeviceTargetType(ctx, "tgt-01", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTargetType(ctx, "stand-7"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTargetType(ctx, "stand-7"); !errors.Is(err, ErrTargetTypeNotFound) {
		t.Errorf("the deleted type reads as %v", err)
	}
	if err := s.DeleteTargetType(ctx, "bar-12"); !errors.Is(err, ErrTargetTypeBuiltin) {
		t.Errorf("deleting a builtin type gave %v", err)
	}
	if err := s.DeleteTargetType(ctx, "nothing"); !errors.Is(err, ErrTargetTypeNotFound) {
		t.Errorf("deleting an unknown type gave %v", err)
	}
}

// A type whose values do not describe a target is refused, and so is a
// device set to a type that does not exist.
func TestTargetTypeIsChecked(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	good := TargetType{
		ID: "ok-1", Name: scenario.Text{"en": "Fine"}, Class: ClassPC,
		DisplayW: 100, DisplayH: 50, ResW: 800, ResH: 400, Orientation: Landscape, Sound: SoundNone,
	}
	bad := map[string]func(*TargetType){
		"no id":            func(tt *TargetType) { tt.ID = "Not An Id" },
		"no name":          func(tt *TargetType) { tt.Name = nil },
		"unknown class":    func(tt *TargetType) { tt.Class = "quantum" },
		"no display":       func(tt *TargetType) { tt.DisplayW = 0 },
		"no resolution":    func(tt *TargetType) { tt.ResH = 0 },
		"bad orientation":  func(tt *TargetType) { tt.Orientation = "sideways" },
		"bad sound":        func(tt *TargetType) { tt.Sound = "gramophone" },
		"too many beacons": func(tt *TargetType) { tt.Beacons = make([]Beacon, MaxBeacons+1) },
	}
	for name, breakIt := range bad {
		tt := good
		breakIt(&tt)
		if _, err := s.CreateTargetType(ctx, tt); !errors.Is(err, ErrBadTargetType) {
			t.Errorf("%s gave %v", name, err)
		}
	}
	if _, err := s.CreateTargetType(ctx, good); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTargetType(ctx, TargetType{
		ID: "nothing", Name: scenario.Text{"en": "x"}, Class: ClassPi, DisplayW: 1, DisplayH: 1,
		ResW: 1, ResH: 1, Orientation: Landscape, Sound: SoundNone,
	}); !errors.Is(err, ErrTargetTypeNotFound) {
		t.Errorf("updating an unknown type gave %v", err)
	}

	if err := s.UpsertDevice(ctx, Device{ID: "tgt-01", Kind: KindTarget}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceTargetType(ctx, "tgt-01", "nothing"); !errors.Is(err, ErrTargetTypeNotFound) {
		t.Errorf("a device set to an unknown type gave %v", err)
	}
	if err := s.SetDeviceTargetType(ctx, "nobody", "ok-1"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("an unknown device gave %v", err)
	}
}

// The calibration of a device follows the type it is set to.
func TestDeviceCalibration(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.UpsertDevice(ctx, Device{ID: "tgt-01", Kind: KindTarget}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.DeviceCalibration(ctx, "tgt-01"); err != nil || ok {
		t.Errorf("a device without a type has the calibration %v, %v", ok, err)
	}
	if err := s.SetDeviceTargetType(ctx, "tgt-01", "bar-12"); err != nil {
		t.Fatal(err)
	}
	calib, ok, err := s.DeviceCalibration(ctx, "tgt-01")
	if err != nil || !ok {
		t.Fatalf("the calibration of the device is %v, %v", ok, err)
	}
	if calib.Value() != "-138,-340 1618,-340 1618,660 -138,660" {
		t.Errorf("the device calibration is %q", calib.Value())
	}
	if _, _, err := s.DeviceCalibration(ctx, "nobody"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("an unknown device gave %v", err)
	}
}
