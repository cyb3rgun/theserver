package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// How a target type is mounted, as the CHECK constraint on
// target_types.orientation allows it.
const (
	Landscape = "landscape"
	Portrait  = "portrait"
)

// Where a target type makes its sound, as the CHECK constraint on
// target_types.sound allows it.
const (
	SoundNone    = "none"
	SoundBuiltin = "builtin"
	SoundHDMI    = "hdmi"
	SoundUSB     = "usb"
)

// The number of clusters a layout may hold (D-068): none while a type is
// not measured yet, otherwise two to sixteen. A target with six or eight
// clusters is described as easily as one with four.
const (
	MinBeacons = 2
	MaxBeacons = 16
)

var (
	// ErrTargetTypeNotFound is returned for a target type id the table does
	// not hold.
	ErrTargetTypeNotFound = errors.New("target type not found")
	// ErrTargetTypeExists refuses to create a type whose id is taken.
	ErrTargetTypeExists = errors.New("target type exists already")
	// ErrTargetTypeBuiltin refuses to delete one of the types that ship with
	// the server (D-064).
	ErrTargetTypeBuiltin = errors.New("a builtin target type cannot be deleted")
	// ErrTargetTypeInUse refuses to delete a type that devices are set to.
	ErrTargetTypeInUse = errors.New("target type is in use by devices")
	// ErrBadTargetType is a type whose values do not describe a target.
	ErrBadTargetType = errors.New("target type is not usable")
)

// Orientations lists the ways a target type can be mounted.
func Orientations() []string { return []string{Landscape, Portrait} }

// Sounds lists where a target type can make its sound.
func Sounds() []string { return []string{SoundNone, SoundBuiltin, SoundHDMI, SoundUSB} }

// A Beacon is one cluster of the beacon layout: its position in millimetres
// from the top left corner of the picture, and the output channel of the
// target module that drives it (D-061, D-068). A cluster may sit outside the
// picture, so both positions may be negative. A cluster of a type described
// before the channels existed reads as channel 0.
type Beacon struct {
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	Ch int     `json:"ch"`
}

// MaxChannel is the highest output channel a cluster can be driven by: the
// mask of the target module has room for eight.
const MaxChannel = 7

// A TargetType describes a kind of target once (D-061): the display and its
// resolution, how it is mounted, where the beacon clusters sit, the class of
// the computer behind it and where the sound comes from. Devices belong to a
// type, a scenario can name the type it was made for, and a client gets its
// calibration from it. Builtin marks a type that ships with the server: it
// can be edited but not deleted.
type TargetType struct {
	ID          string
	Name        scenario.Text
	Class       string
	DisplayW    float64 // the picture width in millimetres
	DisplayH    float64 // the picture height in millimetres
	ResW        int     // the resolution in pixels, which is the canvas
	ResH        int
	Orientation string
	Beacons     []Beacon
	Sound       string
	Notes       scenario.Text
	Builtin     bool
	CreatedAt   int64
	UpdatedAt   int64
}

// A CalibPoint is one beacon cluster in canvas coordinates: the channel
// that drives it and where it sits (D-068).
type CalibPoint struct {
	ID int
	X  int
	Y  int
}

// A Calibration is what a device of a target type is told about its beacons
// (D-068). Points holds one point per cluster, in canvas coordinates, so a
// target with six or eight clusters is described as well as one with four.
// Rect is the bounding box of those points, the four corners in the order
// top left, top right, bottom right, bottom left, which is the order
// theclient reads its calib.rect setting in; a layout that spans no area has
// none and HasRect is false.
type Calibration struct {
	TargetType string
	Points     []CalibPoint
	Rect       [4][2]int
	HasRect    bool
	// CanvasW and CanvasH are the canvas the points are in.
	CanvasW int
	CanvasH int
}

// Value is the rectangle as calib.rect carries it: four points, whole
// numbers, "x,y x,y x,y x,y". It is empty when the layout spans no area.
func (c Calibration) Value() string {
	if !c.HasRect {
		return ""
	}
	parts := make([]string, len(c.Rect))
	for i, p := range c.Rect {
		parts[i] = strconv.Itoa(p[0]) + "," + strconv.Itoa(p[1])
	}
	return strings.Join(parts, " ")
}

// PointsValue is the layout as calib.pts carries it: one cluster per part,
// "id,x,y id,x,y ...", ordered by channel.
func (c Calibration) PointsValue() string {
	parts := make([]string, len(c.Points))
	for i, p := range c.Points {
		parts[i] = strconv.Itoa(p.ID) + "," + strconv.Itoa(p.X) + "," + strconv.Itoa(p.Y)
	}
	return strings.Join(parts, " ")
}

// Calibration turns the beacon layout of the type into canvas coordinates
// (D-063, D-068). The clusters are in millimetres from the top left corner
// of the picture, the picture is DisplayW by DisplayH millimetres and ResW
// by ResH pixels, so a millimetre is ResW/DisplayW pixels across and
// ResH/DisplayH pixels down. Every cluster becomes one point, ordered by
// channel, and the bounding box of those points is the rectangle of B12; a
// layout that spans no area has points and no rectangle. A type without
// clusters or without a size has no calibration and the second return value
// is false.
func (t TargetType) Calibration() (Calibration, bool) {
	if len(t.Beacons) == 0 || t.DisplayW <= 0 || t.DisplayH <= 0 || t.ResW < 1 || t.ResH < 1 {
		return Calibration{}, false
	}
	scaleX, scaleY := float64(t.ResW)/t.DisplayW, float64(t.ResH)/t.DisplayH
	calib := Calibration{TargetType: t.ID, CanvasW: t.ResW, CanvasH: t.ResH}
	for _, b := range t.Beacons {
		calib.Points = append(calib.Points, CalibPoint{ID: b.Ch, X: roundPx(b.X * scaleX), Y: roundPx(b.Y * scaleY)})
	}
	slices.SortStableFunc(calib.Points, func(a, b CalibPoint) int { return a.ID - b.ID })

	minX, maxX := calib.Points[0].X, calib.Points[0].X
	minY, maxY := calib.Points[0].Y, calib.Points[0].Y
	for _, p := range calib.Points[1:] {
		minX, maxX = min(minX, p.X), max(maxX, p.X)
		minY, maxY = min(minY, p.Y), max(maxY, p.Y)
	}
	if minX != maxX && minY != maxY {
		calib.Rect = [4][2]int{{minX, minY}, {maxX, minY}, {maxX, maxY}, {minX, maxY}}
		calib.HasRect = true
	}
	return calib, true
}

func roundPx(v float64) int {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return int(math.Round(v))
}

// Validate refuses a type that does not describe a target. It names the
// field that is wrong, so the API and the page can say what to correct.
func (t TargetType) Validate() error {
	switch {
	case !scenario.ValidID(t.ID):
		return fmt.Errorf("%w: id %q is not a usable id", ErrBadTargetType, t.ID)
	case t.Name.In("en") == "":
		return fmt.Errorf("%w: the type needs a name", ErrBadTargetType)
	case t.Class != ClassESP && t.Class != ClassPi && t.Class != ClassPC:
		return fmt.Errorf("%w: class %q is not one of esp, pi, pc", ErrBadTargetType, t.Class)
	case t.DisplayW <= 0 || t.DisplayH <= 0:
		return fmt.Errorf("%w: the display is %g by %g mm; both sides must be larger than 0",
			ErrBadTargetType, t.DisplayW, t.DisplayH)
	case t.ResW < 1 || t.ResH < 1:
		return fmt.Errorf("%w: the resolution is %d by %d px; both sides must be at least 1",
			ErrBadTargetType, t.ResW, t.ResH)
	case t.Orientation != Landscape && t.Orientation != Portrait:
		return fmt.Errorf("%w: orientation %q is neither landscape nor portrait", ErrBadTargetType, t.Orientation)
	case t.Sound != SoundNone && t.Sound != SoundBuiltin && t.Sound != SoundHDMI && t.Sound != SoundUSB:
		return fmt.Errorf("%w: sound %q is not one of none, builtin, hdmi, usb", ErrBadTargetType, t.Sound)
	case len(t.Beacons) == 1 || len(t.Beacons) > MaxBeacons:
		return fmt.Errorf("%w: %d beacon clusters, allowed are none or %d to %d",
			ErrBadTargetType, len(t.Beacons), MinBeacons, MaxBeacons)
	}
	seen := map[int]bool{}
	for i, b := range t.Beacons {
		switch {
		case math.IsNaN(b.X) || math.IsNaN(b.Y) || math.IsInf(b.X, 0) || math.IsInf(b.Y, 0):
			return fmt.Errorf("%w: beacon %d has no usable position", ErrBadTargetType, i+1)
		case b.Ch < 0 || b.Ch > MaxChannel:
			return fmt.Errorf("%w: beacon %d is on channel %d, allowed are 0 to %d",
				ErrBadTargetType, i+1, b.Ch, MaxChannel)
		case seen[b.Ch]:
			return fmt.Errorf("%w: channel %d drives two clusters", ErrBadTargetType, b.Ch)
		}
		seen[b.Ch] = true
	}
	return nil
}

const targetTypeColumns = `id, name, class, display_w_mm, display_h_mm, res_w, res_h,
  orientation, beacons, sound, notes, builtin, created_at, updated_at`

// ListTargetTypes returns every target type, ordered by id.
func (s *Store) ListTargetTypes(ctx context.Context) ([]TargetType, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+targetTypeColumns+` FROM target_types ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list target types: %w", err)
	}
	defer rows.Close()
	var out []TargetType
	for rows.Next() {
		t, err := scanTargetType(rows)
		if err != nil {
			return nil, fmt.Errorf("list target types: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTargetType reads one type. It returns ErrTargetTypeNotFound for an
// unknown id.
func (s *Store) GetTargetType(ctx context.Context, id string) (TargetType, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+targetTypeColumns+` FROM target_types WHERE id = ?`, id)
	t, err := scanTargetType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return TargetType{}, fmt.Errorf("%s: %w", id, ErrTargetTypeNotFound)
	}
	if err != nil {
		return TargetType{}, fmt.Errorf("get target type %s: %w", id, err)
	}
	return t, nil
}

// numbered gives a layout that names no channels the channels 0, 1, 2 and
// so on, in the order the clusters were written. An operator who does not
// care which output drives which cluster does not have to say, and one who
// does keeps what was typed (D-068).
func (t TargetType) numbered() TargetType {
	if len(t.Beacons) < 2 {
		return t
	}
	for _, b := range t.Beacons {
		if b.Ch != 0 {
			return t
		}
	}
	beacons := make([]Beacon, len(t.Beacons))
	for i, b := range t.Beacons {
		b.Ch = i
		beacons[i] = b
	}
	t.Beacons = beacons
	return t
}

// CreateTargetType stores a new type. The id has to be free, and Builtin is
// never set from outside: only the migration writes it (D-064).
func (s *Store) CreateTargetType(ctx context.Context, t TargetType) (TargetType, error) {
	t = t.numbered()
	t.Builtin = false
	if err := t.Validate(); err != nil {
		return TargetType{}, err
	}
	name, notes, beacons, err := targetTypeJSON(t)
	if err != nil {
		return TargetType{}, err
	}
	if _, err := s.GetTargetType(ctx, t.ID); err == nil {
		return TargetType{}, fmt.Errorf("%s: %w", t.ID, ErrTargetTypeExists)
	} else if !errors.Is(err, ErrTargetTypeNotFound) {
		return TargetType{}, err
	}
	defer s.writing()()
	now := s.nowMilli()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO target_types (
  id, name, class, display_w_mm, display_h_mm, res_w, res_h,
  orientation, beacons, sound, notes, builtin, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		t.ID, name, t.Class, t.DisplayW, t.DisplayH, t.ResW, t.ResH,
		t.Orientation, beacons, t.Sound, notes, now, now)
	if err != nil {
		return TargetType{}, fmt.Errorf("create target type %s: %w", t.ID, err)
	}
	return s.GetTargetType(ctx, t.ID)
}

// UpdateTargetType writes the values of a type that exists. A builtin type
// can be edited like any other; what it stays is undeletable.
func (s *Store) UpdateTargetType(ctx context.Context, t TargetType) (TargetType, error) {
	t = t.numbered()
	if err := t.Validate(); err != nil {
		return TargetType{}, err
	}
	name, notes, beacons, err := targetTypeJSON(t)
	if err != nil {
		return TargetType{}, err
	}
	defer s.writing()()
	result, err := s.db.ExecContext(ctx, `
UPDATE target_types
   SET name         = ?,
       class        = ?,
       display_w_mm = ?,
       display_h_mm = ?,
       res_w        = ?,
       res_h        = ?,
       orientation  = ?,
       beacons      = ?,
       sound        = ?,
       notes        = ?,
       updated_at   = ?
 WHERE id = ?`,
		name, t.Class, t.DisplayW, t.DisplayH, t.ResW, t.ResH,
		t.Orientation, beacons, t.Sound, notes, s.nowMilli(), t.ID)
	if err != nil {
		return TargetType{}, fmt.Errorf("update target type %s: %w", t.ID, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return TargetType{}, fmt.Errorf("%s: %w", t.ID, ErrTargetTypeNotFound)
	}
	return s.GetTargetType(ctx, t.ID)
}

// DeleteTargetType removes a type. A builtin type stays (D-064), and so does
// one that devices are set to; the error names which it is.
func (s *Store) DeleteTargetType(ctx context.Context, id string) error {
	t, err := s.GetTargetType(ctx, id)
	if err != nil {
		return err
	}
	if t.Builtin {
		return fmt.Errorf("%s: %w", id, ErrTargetTypeBuiltin)
	}
	devices, err := s.DevicesOfTargetType(ctx, id)
	if err != nil {
		return err
	}
	if len(devices) > 0 {
		return fmt.Errorf("%s is set on %s: %w", id, strings.Join(devices, ", "), ErrTargetTypeInUse)
	}
	defer s.writing()()
	result, err := s.db.ExecContext(ctx, `DELETE FROM target_types WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete target type %s: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("%s: %w", id, ErrTargetTypeNotFound)
	}
	return nil
}

// DevicesOfTargetType lists the devices that are set to a type, by id.
func (s *Store) DevicesOfTargetType(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM devices WHERE target_type = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("devices of target type %s: %w", id, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var deviceID string
		if err := rows.Scan(&deviceID); err != nil {
			return nil, err
		}
		out = append(out, deviceID)
	}
	return out, rows.Err()
}

// SetDeviceTargetType says which type a device is. An empty type id clears
// it; a type that does not exist is refused with ErrTargetTypeNotFound.
func (s *Store) SetDeviceTargetType(ctx context.Context, deviceID, typeID string) error {
	var value any
	if typeID != "" {
		if _, err := s.GetTargetType(ctx, typeID); err != nil {
			return err
		}
		value = typeID
	}
	defer s.writing()()
	result, err := s.db.ExecContext(ctx,
		`UPDATE devices SET target_type = ?, updated_at = ? WHERE id = ?`, value, s.nowMilli(), deviceID)
	if err != nil {
		return fmt.Errorf("set target type of device %s: %w", deviceID, err)
	}
	return checkOneRow(result, deviceID)
}

// DeviceCalibration is the calibration of the type a device is set to. A
// device without a type, or a type without clusters, has none and the
// second return value is false.
func (s *Store) DeviceCalibration(ctx context.Context, deviceID string) (Calibration, bool, error) {
	device, err := s.GetDevice(ctx, deviceID)
	if err != nil {
		return Calibration{}, false, err
	}
	return s.calibrationOf(ctx, device)
}

func (s *Store) calibrationOf(ctx context.Context, device Device) (Calibration, bool, error) {
	if device.TargetType == "" {
		return Calibration{}, false, nil
	}
	t, err := s.GetTargetType(ctx, device.TargetType)
	if errors.Is(err, ErrTargetTypeNotFound) {
		return Calibration{}, false, nil
	}
	if err != nil {
		return Calibration{}, false, err
	}
	calib, ok := t.Calibration()
	return calib, ok, nil
}

// A Setting is one key and its value as a device is told it with set_config.
type Setting struct {
	Key   string
	Value string
}

// The setting keys a device is told about itself (D-063, D-068).
const (
	KeyCalibPoints  = "calib.pts"
	KeyCalibRect    = "calib.rect"
	KeyBeaconPeriod = "beacon.period_ms"
	KeyBeaconSlots  = "beacon.slots"
	KeyBeaconSlot   = "beacon.slot"
)

// A Setup is what the server knows about one device without asking it: the
// calibration of its target type and the beacon plan of its room with the
// slot the device holds in it (D-068).
type Setup struct {
	DeviceID string
	Calib    Calibration
	HasCalib bool
	Room     Room
	HasRoom  bool
	Slot     int
}

// Settings are the keys of the setup in the order they are sent. A device
// without a type hears nothing about its beacons, one without a room
// nothing about the plan, and a target without a slot nothing about its
// own.
func (s Setup) Settings() []Setting {
	var out []Setting
	if s.HasCalib {
		out = append(out, Setting{KeyCalibPoints, s.Calib.PointsValue()})
		if s.Calib.HasRect {
			out = append(out, Setting{KeyCalibRect, s.Calib.Value()})
		}
	}
	if s.HasRoom {
		out = append(out,
			Setting{KeyBeaconPeriod, strconv.Itoa(s.Room.PeriodMS)},
			Setting{KeyBeaconSlots, strconv.Itoa(s.Room.Slots)})
		if s.Slot > 0 {
			out = append(out, Setting{KeyBeaconSlot, strconv.Itoa(s.Slot)})
		}
	}
	return out
}

// DeviceSetup reads everything a device is told about itself: the
// calibration of its target type and the plan of its room.
func (s *Store) DeviceSetup(ctx context.Context, deviceID string) (Setup, error) {
	device, err := s.GetDevice(ctx, deviceID)
	if err != nil {
		return Setup{}, err
	}
	setup := Setup{DeviceID: deviceID, Slot: device.BeaconSlot}
	setup.Calib, setup.HasCalib, err = s.calibrationOf(ctx, device)
	if err != nil {
		return Setup{}, err
	}
	if device.RoomID != "" {
		room, err := s.GetRoom(ctx, device.RoomID)
		switch {
		case errors.Is(err, ErrRoomNotFound):
		case err != nil:
			return Setup{}, err
		default:
			setup.Room, setup.HasRoom = room, true
		}
	}
	return setup, nil
}

func targetTypeJSON(t TargetType) (name, notes, beacons []byte, err error) {
	if name, err = json.Marshal(nonNilText(t.Name)); err != nil {
		return nil, nil, nil, err
	}
	if notes, err = json.Marshal(nonNilText(t.Notes)); err != nil {
		return nil, nil, nil, err
	}
	list := t.Beacons
	if list == nil {
		list = []Beacon{}
	}
	if beacons, err = json.Marshal(list); err != nil {
		return nil, nil, nil, err
	}
	return name, notes, beacons, nil
}

func scanTargetType(row rowScanner) (TargetType, error) {
	var (
		t                      TargetType
		name, notes, beacons   string
		builtin                int
		displayW, displayH     float64
		resWidth, resHeightPix int
	)
	err := row.Scan(&t.ID, &name, &t.Class, &displayW, &displayH, &resWidth, &resHeightPix,
		&t.Orientation, &beacons, &t.Sound, &notes, &builtin, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return TargetType{}, err
	}
	t.DisplayW, t.DisplayH = displayW, displayH
	t.ResW, t.ResH = resWidth, resHeightPix
	t.Builtin = builtin != 0
	if err := json.Unmarshal([]byte(name), &t.Name); err != nil {
		return TargetType{}, fmt.Errorf("name of target type %s: %w", t.ID, err)
	}
	if err := json.Unmarshal([]byte(notes), &t.Notes); err != nil {
		return TargetType{}, fmt.Errorf("notes of target type %s: %w", t.ID, err)
	}
	if err := json.Unmarshal([]byte(beacons), &t.Beacons); err != nil {
		return TargetType{}, fmt.Errorf("beacons of target type %s: %w", t.ID, err)
	}
	return t, nil
}
