package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// MaxBeacons is how many clusters a layout may hold. A real target has four;
// the bound keeps a typed layout from growing without end.
const MaxBeacons = 16

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
// from the top left corner of the picture. A cluster may sit outside the
// picture, so both values may be negative (D-061).
type Beacon struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

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

// A Calibration is the beacon rectangle of a target type in canvas
// coordinates (D-063): the four corners in the order top left, top right,
// bottom right, bottom left, which is the order theclient reads its
// calib.rect setting in.
type Calibration struct {
	TargetType string
	Rect       [4][2]int
	// CanvasW and CanvasH are the canvas the rectangle is in.
	CanvasW int
	CanvasH int
}

// Value is the rectangle as calib.rect carries it: four points, whole
// numbers, "x,y x,y x,y x,y".
func (c Calibration) Value() string {
	parts := make([]string, len(c.Rect))
	for i, p := range c.Rect {
		parts[i] = strconv.Itoa(p[0]) + "," + strconv.Itoa(p[1])
	}
	return strings.Join(parts, " ")
}

// Calibration derives the beacon rectangle of the type in canvas
// coordinates (D-063). The clusters are in millimetres from the top left
// corner of the picture, the picture is DisplayW by DisplayH millimetres and
// ResW by ResH pixels, so a millimetre is ResW/DisplayW pixels across and
// ResH/DisplayH pixels down. The rectangle is the bounding box of every
// cluster, which is the frame the shot is mapped into; a layout that spans
// no area, or a type without a size, has no calibration and the second
// return value is false.
func (t TargetType) Calibration() (Calibration, bool) {
	if len(t.Beacons) < 2 || t.DisplayW <= 0 || t.DisplayH <= 0 || t.ResW < 1 || t.ResH < 1 {
		return Calibration{}, false
	}
	minX, maxX := t.Beacons[0].X, t.Beacons[0].X
	minY, maxY := t.Beacons[0].Y, t.Beacons[0].Y
	for _, b := range t.Beacons[1:] {
		minX, maxX = math.Min(minX, b.X), math.Max(maxX, b.X)
		minY, maxY = math.Min(minY, b.Y), math.Max(maxY, b.Y)
	}
	scaleX, scaleY := float64(t.ResW)/t.DisplayW, float64(t.ResH)/t.DisplayH
	left, right := roundPx(minX*scaleX), roundPx(maxX*scaleX)
	top, bottom := roundPx(minY*scaleY), roundPx(maxY*scaleY)
	if left == right || top == bottom {
		return Calibration{}, false
	}
	return Calibration{
		TargetType: t.ID,
		Rect:       [4][2]int{{left, top}, {right, top}, {right, bottom}, {left, bottom}},
		CanvasW:    t.ResW,
		CanvasH:    t.ResH,
	}, true
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
	case len(t.Beacons) > MaxBeacons:
		return fmt.Errorf("%w: %d beacon clusters, at most %d", ErrBadTargetType, len(t.Beacons), MaxBeacons)
	}
	for i, b := range t.Beacons {
		if math.IsNaN(b.X) || math.IsNaN(b.Y) || math.IsInf(b.X, 0) || math.IsInf(b.Y, 0) {
			return fmt.Errorf("%w: beacon %d has no usable position", ErrBadTargetType, i+1)
		}
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

// CreateTargetType stores a new type. The id has to be free, and Builtin is
// never set from outside: only the migration writes it (D-064).
func (s *Store) CreateTargetType(ctx context.Context, t TargetType) (TargetType, error) {
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
// device without a type, or a type whose layout gives no rectangle, has
// none and the second return value is false.
func (s *Store) DeviceCalibration(ctx context.Context, deviceID string) (Calibration, bool, error) {
	device, err := s.GetDevice(ctx, deviceID)
	if err != nil {
		return Calibration{}, false, err
	}
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
