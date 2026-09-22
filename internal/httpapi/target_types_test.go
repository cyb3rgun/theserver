package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/cyb3rgun/theserver/internal/store"
)

// The seeds are there and the API shows what the operator needs (D-064).
func TestTargetTypesList(t *testing.T) {
	h := newAPI(t)
	h.device("tgt-01", store.StatusApproved, nil)
	if err := h.st.SetDeviceTargetType(context.Background(), "tgt-01", "bar-12"); err != nil {
		t.Fatal(err)
	}

	list := decode[struct {
		TargetTypes []TargetType `json:"target_types"`
	}](t, h.call("GET", Prefix+"/target-types", ""), 200)
	if len(list.TargetTypes) != 3 {
		t.Fatalf("the API lists %d types", len(list.TargetTypes))
	}
	byID := map[string]TargetType{}
	for _, tt := range list.TargetTypes {
		byID[tt.ID] = tt
	}
	bar := byID["bar-12"]
	switch {
	case bar.ResW != 1480 || bar.ResH != 320:
		t.Errorf("bar-12 has the resolution %d x %d", bar.ResW, bar.ResH)
	case len(bar.Beacons) != 4 || bar.Beacons[0].X != -27.5:
		t.Errorf("bar-12 has the beacons %+v", bar.Beacons)
	case !bar.Builtin:
		t.Error("bar-12 is not builtin")
	case len(bar.Devices) != 1 || bar.Devices[0] != "tgt-01":
		t.Errorf("bar-12 is used by %v", bar.Devices)
	case bar.Name["de"] == "" || bar.Notes["de"] == "":
		t.Errorf("bar-12 lacks a German text: %+v %+v", bar.Name, bar.Notes)
	}
	if got := decode[TargetType](t, h.call("GET", Prefix+"/target-types/bar-12", ""), 200); got.ID != "bar-12" {
		t.Errorf("the detail answered %+v", got)
	}
	expectError(t, h.call("GET", Prefix+"/target-types/nothing", ""), 404, codeNotFound)
}

// The derived rectangle of every seed, computed by hand (D-063).
func TestTargetTypeCalibrationEndpoint(t *testing.T) {
	h := newAPI(t)
	cases := map[string]string{
		"board-10": "0,0 1280,0 1280,800 0,800",
		"bar-12":   "-138,-340 1618,-340 1618,660 -138,660",
		"tv-50":    "0,0 1920,0 1920,1080 0,1080",
	}
	for id, want := range cases {
		got := decode[Calibration](t, h.call("GET", Prefix+"/target-types/"+id+"/calibration", ""), 200)
		if got.Value != want {
			t.Errorf("%s gives %q, want %q", id, got.Value, want)
		}
		if len(got.Rect) != 4 || got.TargetType != id {
			t.Errorf("%s gives %+v", id, got)
		}
	}
	if got := decode[Calibration](t, h.call("GET", Prefix+"/target-types/bar-12/calibration", ""), 200); got.CanvasW != 1480 || got.CanvasH != 320 {
		t.Errorf("the canvas of bar-12 is %d x %d", got.CanvasW, got.CanvasH)
	}

	// A type whose clusters sit on one line has its points and no
	// rectangle (D-068).
	flat := `{"id":"flat-1","name":{"en":"Flat"},"class":"pi","display_w_mm":100,"display_h_mm":50,
	  "res_w":800,"res_h":400,"orientation":"landscape","sound":"none","beacons":[{"x":0,"y":0},{"x":0,"y":50}]}`
	decode[TargetType](t, h.call("POST", Prefix+"/target-types", flat), http.StatusCreated)
	line := decode[Calibration](t, h.call("GET", Prefix+"/target-types/flat-1/calibration", ""), 200)
	if line.PointsValue != "0,0,0 1,0,400" || line.Value != "" || len(line.Rect) != 0 {
		t.Errorf("a line gives %+v", line)
	}

	// A type without clusters has nothing to send.
	bare := `{"id":"bare-1","name":{"en":"Bare"},"class":"pi","display_w_mm":100,"display_h_mm":50,
	  "res_w":800,"res_h":400,"orientation":"landscape","sound":"none"}`
	decode[TargetType](t, h.call("POST", Prefix+"/target-types", bare), http.StatusCreated)
	expectError(t, h.call("GET", Prefix+"/target-types/bare-1/calibration", ""), http.StatusConflict, codeNoCalibration)
	expectError(t, h.call("GET", Prefix+"/target-types/nothing/calibration", ""), 404, codeNotFound)
}

// A type is created, changed and deleted through the API; a builtin one and
// one that devices are set to stay (D-064).
func TestTargetTypeRoutes(t *testing.T) {
	h := newAPI(t)
	body := `{"id":"stand-7","name":{"en":"Phone stand","de":"Handyhalter"},"class":"pi",
	  "display_w_mm":150,"display_h_mm":70,"res_w":1080,"res_h":1920,"orientation":"portrait",
	  "sound":"usb","beacons":[{"x":0,"y":0},{"x":150,"y":0},{"x":150,"y":70},{"x":0,"y":70}],
	  "notes":{"en":"A phone in a stand.","de":"Ein Telefon im Halter."},"builtin":true}`
	rec := h.call("POST", Prefix+"/target-types", body)
	created := decode[TargetType](t, rec, http.StatusCreated)
	if created.Builtin {
		t.Error("a created type came back builtin")
	}
	if rec.Header().Get("Location") != Prefix+"/target-types/stand-7" {
		t.Errorf("Location is %q", rec.Header().Get("Location"))
	}
	expectError(t, h.call("POST", Prefix+"/target-types", body), http.StatusConflict, codeConflict)
	for _, bad := range []string{
		`{"id":"Not An Id","name":{"en":"x"},"class":"pi","display_w_mm":1,"display_h_mm":1,"res_w":1,"res_h":1,"orientation":"landscape","sound":"none"}`,
		`{"id":"ok-2","name":{"en":"x"},"class":"quantum","display_w_mm":1,"display_h_mm":1,"res_w":1,"res_h":1,"orientation":"landscape","sound":"none"}`,
		`{"id":"ok-3","name":{"en":"x"},"class":"pi","display_w_mm":0,"display_h_mm":1,"res_w":1,"res_h":1,"orientation":"landscape","sound":"none"}`,
		`{"id":"ok-4","name":{},"class":"pi","display_w_mm":1,"display_h_mm":1,"res_w":1,"res_h":1,"orientation":"landscape","sound":"none"}`,
		`{`,
	} {
		expectError(t, h.call("POST", Prefix+"/target-types", bad), http.StatusBadRequest, codeBadRequest)
	}

	// The change takes the id from the path.
	changed := `{"id":"ignored","name":{"en":"Phone stand","de":"Handyhalter"},"class":"pc",
	  "display_w_mm":150,"display_h_mm":70,"res_w":1080,"res_h":1920,"orientation":"portrait",
	  "sound":"none","beacons":[{"x":0,"y":0},{"x":150,"y":70}],"notes":{"en":"Quiet.","de":"Leise."}}`
	updated := decode[TargetType](t, h.call("PUT", Prefix+"/target-types/stand-7", changed), 200)
	switch {
	case updated.ID != "stand-7":
		t.Errorf("the update renamed the type to %s", updated.ID)
	case updated.Class != "pc" || updated.Sound != "none":
		t.Errorf("the update gave %+v", updated)
	case updated.CreatedAt != created.CreatedAt:
		t.Error("the update moved created_at")
	}
	expectError(t, h.call("PUT", Prefix+"/target-types/nothing", changed), 404, codeNotFound)

	// A device is set to the type, so the type stays.
	h.device("tgt-01", store.StatusApproved, nil)
	decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-01/target-type", `{"target_type":"stand-7"}`), 200)
	expectError(t, h.call("DELETE", Prefix+"/target-types/stand-7", ""), http.StatusConflict, codeInUse)
	expectError(t, h.call("DELETE", Prefix+"/target-types/bar-12", ""), http.StatusConflict, codeBuiltin)

	decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-01/target-type", `{"target_type":""}`), 200)
	if rec := h.call("DELETE", Prefix+"/target-types/stand-7", ""); rec.Code != http.StatusNoContent {
		t.Errorf("the delete answered %d: %s", rec.Code, rec.Body.String())
	}
	expectError(t, h.call("DELETE", Prefix+"/target-types/stand-7", ""), 404, codeNotFound)
}

// A device is set to a type and is told the rectangle of that type at once;
// an approved device is told as well (D-063).
func TestDeviceTargetTypeCalibrates(t *testing.T) {
	h := newAPI(t)
	h.device("tgt-01", store.StatusPending, nil)
	h.link.calibration = map[string]store.Calibration{
		"tgt-01": {
			TargetType: "bar-12",
			Points: []store.CalibPoint{{ID: 0, X: -138, Y: -340}, {ID: 1, X: 1618, Y: -340},
				{ID: 2, X: 1618, Y: 660}, {ID: 3, X: -138, Y: 660}},
			Rect:    [4][2]int{{-138, -340}, {1618, -340}, {1618, 660}, {-138, 660}},
			HasRect: true, CanvasW: 1480, CanvasH: 320,
		},
	}

	answer := decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-01/target-type", `{"target_type":"bar-12"}`), 200)
	switch {
	case answer.Device.TargetType != "bar-12":
		t.Errorf("the device is %+v", answer.Device)
	case answer.Calibration == nil || answer.Calibration.Value != "-138,-340 1618,-340 1618,660 -138,660":
		t.Errorf("the answer carries the calibration %+v", answer.Calibration)
	case answer.Calibration.PointsValue != "0,-138,-340 1,1618,-340 2,1618,660 3,-138,660":
		t.Errorf("the answer carries the points %q", answer.Calibration.PointsValue)
	case !answer.Sent:
		t.Error("the answer says the device was not told")
	}
	if len(h.link.calibrated) != 1 || h.link.calibrated[0] != "tgt-01" {
		t.Errorf("the link calibrated %v", h.link.calibrated)
	}

	// Approving the device tells it again, and so does a change of its type.
	decode[Device](t, h.call("POST", Prefix+"/devices/tgt-01/approve", ""), 200)
	if len(h.link.calibrated) != 2 {
		t.Errorf("after the approval the link calibrated %v", h.link.calibrated)
	}
	typeBody := `{"name":{"en":"Bar display","de":"Leistendisplay"},"class":"pi","display_w_mm":295,"display_h_mm":64,
	  "res_w":1480,"res_h":320,"orientation":"landscape","sound":"hdmi",
	  "beacons":[{"x":-20,"y":-60},{"x":315,"y":-60},{"x":315,"y":124},{"x":-20,"y":124}]}`
	decode[TargetType](t, h.call("PUT", Prefix+"/target-types/bar-12", typeBody), 200)
	if len(h.link.calibrated) != 3 {
		t.Errorf("after the type changed the link calibrated %v", h.link.calibrated)
	}

	// A type the server does not have is refused, and the device keeps the
	// one it has.
	expectError(t, h.call("POST", Prefix+"/devices/tgt-01/target-type", `{"target_type":"nothing"}`), 404, codeNotFound)
	expectError(t, h.call("POST", Prefix+"/devices/nobody/target-type", `{"target_type":"bar-12"}`), 404, codeNotFound)
	device := decode[Device](t, h.call("GET", Prefix+"/devices/tgt-01", ""), 200)
	if device.TargetType != "bar-12" {
		t.Errorf("after the refused change the device is %+v", device)
	}

	// Taking the type off leaves nothing to send.
	cleared := decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-01/target-type", `{"target_type":""}`), 200)
	if cleared.Device.TargetType != "" {
		t.Errorf("the device still has the type %q", cleared.Device.TargetType)
	}
}
