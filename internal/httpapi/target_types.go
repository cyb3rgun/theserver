package httpapi

import (
	"errors"
	"net/http"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// TargetType is a target type as API v1 shows it (D-061). The display is in
// millimetres, the resolution in pixels and the beacon clusters in
// millimetres from the top left corner of the picture; a cluster may sit
// outside the picture, so its values may be negative. Builtin marks a type
// that ships with the server: it can be edited but not deleted.
type TargetType struct {
	ID          string            `json:"id"`
	Name        map[string]string `json:"name"`
	Class       string            `json:"class"`
	DisplayWMM  float64           `json:"display_w_mm"`
	DisplayHMM  float64           `json:"display_h_mm"`
	ResW        int               `json:"res_w"`
	ResH        int               `json:"res_h"`
	Orientation string            `json:"orientation"`
	Beacons     []Beacon          `json:"beacons"`
	Sound       string            `json:"sound"`
	Notes       map[string]string `json:"notes"`
	Builtin     bool              `json:"builtin"`
	CreatedAt   int64             `json:"created_at"`
	UpdatedAt   int64             `json:"updated_at"`
	// Devices are the devices that are set to this type, by id.
	Devices []string `json:"devices"`
}

// Beacon is one cluster of a beacon layout, in millimetres, with the output
// channel of the target module that drives it (D-068).
type Beacon struct {
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	Ch int     `json:"ch"`
}

// Calibration is the beacon layout of a type in canvas coordinates (D-068):
// one point per cluster with the channel that drives it, and the bounding
// box of those points as the rectangle of B12, which is empty for a layout
// that spans no area.
type Calibration struct {
	TargetType  string       `json:"target_type"`
	Points      []CalibPoint `json:"points"`
	PointsValue string       `json:"points_value"`
	Rect        [][2]int     `json:"rect"`
	Value       string       `json:"value"`
	CanvasW     int          `json:"canvas_w"`
	CanvasH     int          `json:"canvas_h"`
}

// CalibPoint is one beacon cluster in canvas coordinates: id is the output
// channel that drives it.
type CalibPoint struct {
	ID int `json:"id"`
	X  int `json:"x"`
	Y  int `json:"y"`
}

// DeviceTargetType is the body of POST /devices/{id}/target-type. An empty
// id takes the type off the device.
type DeviceTargetType struct {
	TargetType string `json:"target_type"`
}

// DeviceCalibrated answers a change of what a device is or where it stands:
// the device as it is now, the calibration of its type and every setting it
// was told (D-068).
type DeviceCalibrated struct {
	Device      Device       `json:"device"`
	Calibration *Calibration `json:"calibration"`
	Settings    []Setting    `json:"settings"`
	// Sent says whether the device took the settings; a device that is
	// offline gets them when it connects.
	Sent bool `json:"sent"`
}

// A Setting is one key a device is told with set_config.
type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func targetTypeJSON(t store.TargetType, devices []string) TargetType {
	beacons := make([]Beacon, 0, len(t.Beacons))
	for _, b := range t.Beacons {
		beacons = append(beacons, Beacon{X: b.X, Y: b.Y, Ch: b.Ch})
	}
	if devices == nil {
		devices = []string{}
	}
	return TargetType{
		ID: t.ID, Name: t.Name, Class: t.Class,
		DisplayWMM: t.DisplayW, DisplayHMM: t.DisplayH, ResW: t.ResW, ResH: t.ResH,
		Orientation: t.Orientation, Beacons: beacons, Sound: t.Sound, Notes: t.Notes,
		Builtin: t.Builtin, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Devices: devices,
	}
}

func targetTypeStore(body TargetType) store.TargetType {
	beacons := make([]store.Beacon, 0, len(body.Beacons))
	for _, b := range body.Beacons {
		beacons = append(beacons, store.Beacon{X: b.X, Y: b.Y, Ch: b.Ch})
	}
	return store.TargetType{
		ID: body.ID, Name: scenario.Text(body.Name), Class: body.Class,
		DisplayW: body.DisplayWMM, DisplayH: body.DisplayHMM, ResW: body.ResW, ResH: body.ResH,
		Orientation: body.Orientation, Beacons: beacons, Sound: body.Sound, Notes: scenario.Text(body.Notes),
	}
}

func calibrationJSON(c store.Calibration) Calibration {
	points := make([]CalibPoint, 0, len(c.Points))
	for _, p := range c.Points {
		points = append(points, CalibPoint{ID: p.ID, X: p.X, Y: p.Y})
	}
	rect := make([][2]int, 0, len(c.Rect))
	if c.HasRect {
		for _, p := range c.Rect {
			rect = append(rect, p)
		}
	}
	return Calibration{
		TargetType: c.TargetType, Points: points, PointsValue: c.PointsValue(),
		Rect: rect, Value: c.Value(), CanvasW: c.CanvasW, CanvasH: c.CanvasH,
	}
}

func (s *Server) listTargetTypes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	types, err := s.opts.Store.ListTargetTypes(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	devices, err := s.opts.Store.ListDevices(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byType := map[string][]string{}
	for _, d := range devices {
		if d.TargetType != "" {
			byType[d.TargetType] = append(byType[d.TargetType], d.ID)
		}
	}
	out := make([]TargetType, 0, len(types))
	for _, t := range types {
		out = append(out, targetTypeJSON(t, byType[t.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"target_types": out})
}

func (s *Server) getTargetType(w http.ResponseWriter, r *http.Request) {
	s.writeTargetType(w, r, r.PathValue("id"), http.StatusOK)
}

func (s *Server) writeTargetType(w http.ResponseWriter, r *http.Request, id string, status int) {
	ctx := r.Context()
	t, err := s.opts.Store.GetTargetType(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	devices, err := s.opts.Store.DevicesOfTargetType(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, targetTypeJSON(t, devices))
}

// createTargetType stores a type an operator described. Builtin is never
// taken from the body: only the migration writes builtin types (D-064).
func (s *Server) createTargetType(w http.ResponseWriter, r *http.Request) {
	var body TargetType
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	created, err := s.opts.Store.CreateTargetType(r.Context(), targetTypeStore(body))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "target type created", "target_type", created.ID)
	w.Header().Set("Location", Prefix+"/target-types/"+created.ID)
	writeJSON(w, http.StatusCreated, targetTypeJSON(created, nil))
}

// updateTargetType writes the values of a type that exists. The id in the
// path decides; one in the body is ignored.
func (s *Server) updateTargetType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body TargetType
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	update := targetTypeStore(body)
	update.ID = id
	updated, err := s.opts.Store.UpdateTargetType(r.Context(), update)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "target type changed", "target_type", updated.ID)
	// Every device of the type gets the points of the new layout.
	s.pushSetup(r, updated.ID)
	s.writeTargetType(w, r, id, http.StatusOK)
}

func (s *Server) deleteTargetType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Store.DeleteTargetType(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "target type deleted", "target_type", id)
	w.WriteHeader(http.StatusNoContent)
}

// targetTypeCalibration answers the derived rectangle of a type, so that an
// operator sees what a device is told (D-063).
func (s *Server) targetTypeCalibration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.opts.Store.GetTargetType(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	calib, ok := t.Calibration()
	if !ok {
		s.fail(w, r, errNoCalibrationOf(id))
		return
	}
	writeJSON(w, http.StatusOK, calibrationJSON(calib))
}

func errNoCalibrationOf(id string) error {
	return &noCalibrationError{id: id}
}

// noCalibrationError is a type without beacon clusters, so nothing can be
// derived from it.
type noCalibrationError struct{ id string }

func (e *noCalibrationError) Error() string {
	return "target type " + e.id + " has no beacon clusters, so there is nothing to send"
}

// setDeviceTargetType says which type a device is and tells the device the
// calibration of that type at once (D-063).
func (s *Server) setDeviceTargetType(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body DeviceTargetType
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	if err := s.opts.Store.SetDeviceTargetType(ctx, id, body.TargetType); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "device target type set", "device", id, "target_type", body.TargetType)

	answer := DeviceCalibrated{}
	if setup, sent, ok := s.sendSetup(r, id); ok {
		if setup.HasCalib {
			out := calibrationJSON(setup.Calib)
			answer.Calibration = &out
		}
		answer.Sent = sent
		answer.Settings = settingsJSON(setup)
	}
	device, err := s.opts.Store.GetDevice(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	answer.Device = deviceJSON(device, s.onlineByID())
	writeJSON(w, http.StatusOK, answer)
}

// sendSetup tells one device what it is and where it stands (D-063,
// D-068). It reports the setup, whether the device took it, and whether the
// link was there to ask at all.
func (s *Server) sendSetup(r *http.Request, deviceID string) (store.Setup, bool, bool) {
	if s.opts.Link == nil {
		return store.Setup{}, false, false
	}
	setup, sent, err := s.opts.Link.SendSetup(r.Context(), deviceID)
	if err != nil && !errors.Is(err, link.ErrDeviceOffline) {
		s.log.Warn("could not set up the device", "device", deviceID, "error", err)
	}
	return setup, sent, true
}

// settingsJSON is what a device was told, key by key.
func settingsJSON(setup store.Setup) []Setting {
	out := make([]Setting, 0, 5)
	for _, setting := range setup.Settings() {
		out = append(out, Setting{Key: setting.Key, Value: setting.Value})
	}
	return out
}

// pushSetup tells every device of a target type its settings again, after
// the type changed. A device that is offline gets them when it connects.
func (s *Server) pushSetup(r *http.Request, typeID string) {
	if s.opts.Link == nil {
		return
	}
	devices, err := s.opts.Store.DevicesOfTargetType(r.Context(), typeID)
	if err != nil {
		s.log.Warn("could not read the devices of a target type", "target_type", typeID, "error", err)
		return
	}
	for _, deviceID := range devices {
		s.sendSetup(r, deviceID)
	}
}
