package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The shape of a venue (D-066): a site is the place, a room belongs to a
// site, a target is a device of kind target in a room, and a controller is a
// device of kind controller that belongs to a site and may belong to a room.

// DefaultSiteID is the site the move to sites and rooms put every device
// into (D-069).
const DefaultSiteID = "site-default"

// The bounds of a room plan. A period below 20 ms leaves no room for a
// slot, and more than 32 slots is no plan a target module can serve.
const (
	MinPeriodMS = 20
	MaxPeriodMS = 5000
	MinSlots    = 1
	MaxSlots    = 32
)

var (
	// ErrSiteNotFound is returned for a site id the store does not hold.
	ErrSiteNotFound = errors.New("site not found")
	// ErrSiteExists refuses to create a site whose id is taken.
	ErrSiteExists = errors.New("site exists already")
	// ErrSiteInUse refuses to delete a site that still holds rooms or
	// devices.
	ErrSiteInUse = errors.New("site is in use by rooms or devices")
	// ErrBadSite is a site whose values do not describe a venue.
	ErrBadSite = errors.New("site is not usable")
	// ErrRoomNotFound is returned for a room id the store does not hold.
	ErrRoomNotFound = errors.New("room not found")
	// ErrRoomExists refuses to create a room whose id is taken.
	ErrRoomExists = errors.New("room exists already")
	// ErrRoomInUse refuses to delete a room that still holds devices or is
	// named by a session.
	ErrRoomInUse = errors.New("room is in use by devices or sessions")
	// ErrBadRoom is a room whose values do not describe a room.
	ErrBadRoom = errors.New("room is not usable")
	// ErrSlotTaken refuses a beacon slot another target of the same room
	// already has, and a slot outside the plan of the room (D-068).
	ErrSlotTaken = errors.New("beacon slot is not free in this room")
)

// A Site is a venue. The licence fields are set by the manufacturer and are
// stored and shown, not used yet (D-070): FranchiseRate is a percentage,
// MonthlyThreshold is in the smallest unit of Currency, and ValidFrom is
// unix milliseconds, 0 while it is not set.
type Site struct {
	ID               string
	Name             string
	Address          string
	Timezone         string
	Contact          string
	Notes            scenario.Text
	LicenceID        string
	FranchiseRate    float64
	MonthlyThreshold int64
	Currency         string
	ValidFrom        int64
	CreatedAt        int64
	UpdatedAt        int64
}

// A Room is one room of a site. AgeRating is the age of the players in it,
// as a scenario rating; a scenario rated higher cannot be played there
// (D-067). PeriodMS and Slots are the beacon multiplex plan: the length of
// one round and how many slots it has (D-068).
type Room struct {
	ID          string
	SiteID      string
	Name        string
	AgeRating   string
	WiFiChannel int
	PeriodMS    int
	Slots       int
	Capacity    int
	Notes       scenario.Text
	CreatedAt   int64
	UpdatedAt   int64
}

// Validate refuses a site that does not describe a venue.
func (s Site) Validate() error {
	switch {
	case !scenario.ValidID(s.ID):
		return fmt.Errorf("%w: id %q is not a usable id", ErrBadSite, s.ID)
	case s.Name == "":
		return fmt.Errorf("%w: the site needs a name", ErrBadSite)
	case s.FranchiseRate < 0 || s.FranchiseRate > 100:
		return fmt.Errorf("%w: the franchise rate is %g percent", ErrBadSite, s.FranchiseRate)
	case s.MonthlyThreshold < 0:
		return fmt.Errorf("%w: the monthly threshold is %d", ErrBadSite, s.MonthlyThreshold)
	case len(s.Currency) != 0 && len(s.Currency) != 3:
		return fmt.Errorf("%w: the currency %q is not a three letter code", ErrBadSite, s.Currency)
	}
	return nil
}

// Validate refuses a room that does not describe a room.
func (r Room) Validate() error {
	switch {
	case !scenario.ValidID(r.ID):
		return fmt.Errorf("%w: id %q is not a usable id", ErrBadRoom, r.ID)
	case r.SiteID == "":
		return fmt.Errorf("%w: the room needs a site", ErrBadRoom)
	case r.Name == "":
		return fmt.Errorf("%w: the room needs a name", ErrBadRoom)
	case !validRating(r.AgeRating):
		return fmt.Errorf("%w: the age rating %q is not one of the ratings", ErrBadRoom, r.AgeRating)
	case r.WiFiChannel < 0 || r.WiFiChannel > 196:
		return fmt.Errorf("%w: the WiFi channel is %d", ErrBadRoom, r.WiFiChannel)
	case r.PeriodMS < MinPeriodMS || r.PeriodMS > MaxPeriodMS:
		return fmt.Errorf("%w: the beacon period is %d ms, allowed are %d to %d",
			ErrBadRoom, r.PeriodMS, MinPeriodMS, MaxPeriodMS)
	case r.Slots < MinSlots || r.Slots > MaxSlots:
		return fmt.Errorf("%w: the plan has %d slots, allowed are %d to %d", ErrBadRoom, r.Slots, MinSlots, MaxSlots)
	case r.Capacity < 0:
		return fmt.Errorf("%w: the capacity is %d", ErrBadRoom, r.Capacity)
	}
	return nil
}

func validRating(rating string) bool {
	for _, r := range scenario.AgeRatings() {
		if r == rating {
			return true
		}
	}
	return false
}

const siteColumns = `id, name, address, timezone, contact, notes,
  licence_id, franchise_rate, monthly_threshold, currency, valid_from, created_at, updated_at`

// ListSites returns every site, ordered by name.
func (s *Store) ListSites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+siteColumns+` FROM sites ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, fmt.Errorf("list sites: %w", err)
		}
		out = append(out, site)
	}
	return out, rows.Err()
}

// GetSite reads one site. It returns ErrSiteNotFound for an unknown id.
func (s *Store) GetSite(ctx context.Context, id string) (Site, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE id = ?`, id)
	site, err := scanSite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, fmt.Errorf("%s: %w", id, ErrSiteNotFound)
	}
	if err != nil {
		return Site{}, fmt.Errorf("get site %s: %w", id, err)
	}
	return site, nil
}

// CreateSite stores a new site.
func (s *Store) CreateSite(ctx context.Context, site Site) (Site, error) {
	if site.Currency == "" {
		site.Currency = "EUR"
	}
	if err := site.Validate(); err != nil {
		return Site{}, err
	}
	notes, err := json.Marshal(nonNilText(site.Notes))
	if err != nil {
		return Site{}, err
	}
	if _, err := s.GetSite(ctx, site.ID); err == nil {
		return Site{}, fmt.Errorf("%s: %w", site.ID, ErrSiteExists)
	} else if !errors.Is(err, ErrSiteNotFound) {
		return Site{}, err
	}
	defer s.writing()()
	now := s.nowMilli()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO sites (id, name, address, timezone, contact, notes,
  licence_id, franchise_rate, monthly_threshold, currency, valid_from, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		site.ID, site.Name, site.Address, site.Timezone, site.Contact, notes,
		site.LicenceID, site.FranchiseRate, site.MonthlyThreshold, site.Currency, site.ValidFrom, now, now)
	if err != nil {
		return Site{}, fmt.Errorf("create site %s: %w", site.ID, err)
	}
	return s.GetSite(ctx, site.ID)
}

// UpdateSite writes the values of a site that exists.
func (s *Store) UpdateSite(ctx context.Context, site Site) (Site, error) {
	if site.Currency == "" {
		site.Currency = "EUR"
	}
	if err := site.Validate(); err != nil {
		return Site{}, err
	}
	notes, err := json.Marshal(nonNilText(site.Notes))
	if err != nil {
		return Site{}, err
	}
	defer s.writing()()
	result, err := s.db.ExecContext(ctx, `
UPDATE sites
   SET name = ?, address = ?, timezone = ?, contact = ?, notes = ?,
       licence_id = ?, franchise_rate = ?, monthly_threshold = ?, currency = ?, valid_from = ?,
       updated_at = ?
 WHERE id = ?`,
		site.Name, site.Address, site.Timezone, site.Contact, notes,
		site.LicenceID, site.FranchiseRate, site.MonthlyThreshold, site.Currency, site.ValidFrom,
		s.nowMilli(), site.ID)
	if err != nil {
		return Site{}, fmt.Errorf("update site %s: %w", site.ID, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return Site{}, fmt.Errorf("%s: %w", site.ID, ErrSiteNotFound)
	}
	return s.GetSite(ctx, site.ID)
}

// DeleteSite removes a site that holds neither rooms nor devices.
func (s *Store) DeleteSite(ctx context.Context, id string) error {
	if _, err := s.GetSite(ctx, id); err != nil {
		return err
	}
	rooms, err := s.RoomsOfSite(ctx, id)
	if err != nil {
		return err
	}
	devices, err := s.DevicesOfSite(ctx, id)
	if err != nil {
		return err
	}
	if len(rooms) > 0 || len(devices) > 0 {
		return fmt.Errorf("%s holds %d rooms and %d devices: %w", id, len(rooms), len(devices), ErrSiteInUse)
	}
	defer s.writing()()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete site %s: %w", id, err)
	}
	return nil
}

const roomColumns = `id, site_id, name, age_rating, wifi_channel, beacon_period_ms, beacon_slots,
  capacity, notes, created_at, updated_at`

// ListRooms returns every room, ordered by site and name.
func (s *Store) ListRooms(ctx context.Context) ([]Room, error) {
	return s.roomsWhere(ctx, ``)
}

// RoomsOfSite returns the rooms of one site, ordered by name.
func (s *Store) RoomsOfSite(ctx context.Context, siteID string) ([]Room, error) {
	return s.roomsWhere(ctx, ` WHERE site_id = ?`, siteID)
}

func (s *Store) roomsWhere(ctx context.Context, where string, args ...any) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+roomColumns+` FROM rooms`+where+` ORDER BY site_id, name, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list rooms: %w", err)
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, fmt.Errorf("list rooms: %w", err)
		}
		out = append(out, room)
	}
	return out, rows.Err()
}

// GetRoom reads one room. It returns ErrRoomNotFound for an unknown id.
func (s *Store) GetRoom(ctx context.Context, id string) (Room, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+roomColumns+` FROM rooms WHERE id = ?`, id)
	room, err := scanRoom(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, fmt.Errorf("%s: %w", id, ErrRoomNotFound)
	}
	if err != nil {
		return Room{}, fmt.Errorf("get room %s: %w", id, err)
	}
	return room, nil
}

// CreateRoom stores a new room of a site that exists.
func (s *Store) CreateRoom(ctx context.Context, room Room) (Room, error) {
	room = room.withDefaults()
	if err := room.Validate(); err != nil {
		return Room{}, err
	}
	if _, err := s.GetSite(ctx, room.SiteID); err != nil {
		return Room{}, err
	}
	notes, err := json.Marshal(nonNilText(room.Notes))
	if err != nil {
		return Room{}, err
	}
	if _, err := s.GetRoom(ctx, room.ID); err == nil {
		return Room{}, fmt.Errorf("%s: %w", room.ID, ErrRoomExists)
	} else if !errors.Is(err, ErrRoomNotFound) {
		return Room{}, err
	}
	defer s.writing()()
	now := s.nowMilli()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO rooms (id, site_id, name, age_rating, wifi_channel, beacon_period_ms, beacon_slots,
  capacity, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		room.ID, room.SiteID, room.Name, room.AgeRating, room.WiFiChannel, room.PeriodMS, room.Slots,
		room.Capacity, notes, now, now)
	if err != nil {
		return Room{}, fmt.Errorf("create room %s: %w", room.ID, err)
	}
	return s.GetRoom(ctx, room.ID)
}

// UpdateRoom writes the values of a room that exists. The site of a room
// does not change here: a room that moves is a new room.
func (s *Store) UpdateRoom(ctx context.Context, room Room) (Room, error) {
	current, err := s.GetRoom(ctx, room.ID)
	if err != nil {
		return Room{}, err
	}
	room.SiteID = current.SiteID
	room = room.withDefaults()
	if err := room.Validate(); err != nil {
		return Room{}, err
	}
	notes, err := json.Marshal(nonNilText(room.Notes))
	if err != nil {
		return Room{}, err
	}
	defer s.writing()()
	_, err = s.db.ExecContext(ctx, `
UPDATE rooms
   SET name = ?, age_rating = ?, wifi_channel = ?, beacon_period_ms = ?, beacon_slots = ?,
       capacity = ?, notes = ?, updated_at = ?
 WHERE id = ?`,
		room.Name, room.AgeRating, room.WiFiChannel, room.PeriodMS, room.Slots,
		room.Capacity, notes, s.nowMilli(), room.ID)
	if err != nil {
		return Room{}, fmt.Errorf("update room %s: %w", room.ID, err)
	}
	return s.GetRoom(ctx, room.ID)
}

// DeleteRoom removes a room that holds no devices and that no session names.
func (s *Store) DeleteRoom(ctx context.Context, id string) error {
	if _, err := s.GetRoom(ctx, id); err != nil {
		return err
	}
	devices, err := s.DevicesOfRoom(ctx, id)
	if err != nil {
		return err
	}
	var sessions int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE room_id = ?`, id).Scan(&sessions); err != nil {
		return fmt.Errorf("delete room %s: %w", id, err)
	}
	if len(devices) > 0 || sessions > 0 {
		return fmt.Errorf("%s holds %d devices and %d sessions: %w", id, len(devices), sessions, ErrRoomInUse)
	}
	defer s.writing()()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM rooms WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete room %s: %w", id, err)
	}
	return nil
}

// withDefaults fills the plan of a room that was described without one.
func (r Room) withDefaults() Room {
	if r.AgeRating == "" {
		r.AgeRating = "18"
	}
	if r.PeriodMS == 0 {
		r.PeriodMS = 100
	}
	if r.Slots == 0 {
		r.Slots = 8
	}
	return r
}

// DevicesOfRoom lists the devices of a room, ordered by id.
func (s *Store) DevicesOfRoom(ctx context.Context, roomID string) ([]Device, error) {
	return s.devicesWhere(ctx, `WHERE room_id = ?`, roomID)
}

// TargetsOfRoom lists the devices of kind target in a room, ordered by id.
func (s *Store) TargetsOfRoom(ctx context.Context, roomID string) ([]Device, error) {
	return s.devicesWhere(ctx, `WHERE room_id = ? AND kind = ?`, roomID, KindTarget)
}

// DevicesOfSite lists the devices of a site, ordered by id.
func (s *Store) DevicesOfSite(ctx context.Context, siteID string) ([]Device, error) {
	return s.devicesWhere(ctx, `WHERE site_id = ?`, siteID)
}

// ControllersOfSite lists the devices of kind controller of a site, the ones
// of its rooms and the ones that are free for the whole site (D-066).
func (s *Store) ControllersOfSite(ctx context.Context, siteID string) ([]Device, error) {
	return s.devicesWhere(ctx, `WHERE site_id = ? AND kind = ?`, siteID, KindController)
}

func (s *Store) devicesWhere(ctx context.Context, where string, args ...any) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceColumns+` FROM devices `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	var devices []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("list devices: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// A Placement says where a device stands: its room, its beacon slot and the
// free note of its position. An empty RoomID takes the device out of its
// room; the site follows the room unless it is given.
type Placement struct {
	RoomID   string
	SiteID   string
	Slot     int
	Position string
}

// PlaceDevice puts a device into a room of a site, with its beacon slot.
// A slot another target of the room holds, and a slot outside the plan of
// the room, are refused with ErrSlotTaken (D-068).
func (s *Store) PlaceDevice(ctx context.Context, deviceID string, place Placement) error {
	device, err := s.GetDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	siteID := place.SiteID
	if place.RoomID != "" {
		room, err := s.GetRoom(ctx, place.RoomID)
		if err != nil {
			return err
		}
		siteID = room.SiteID
		if err := s.slotIsFree(ctx, room, deviceID, device.Kind, place.Slot); err != nil {
			return err
		}
	}
	if siteID != "" {
		if _, err := s.GetSite(ctx, siteID); err != nil {
			return err
		}
	}
	defer s.writing()()
	result, err := s.db.ExecContext(ctx, `
UPDATE devices
   SET room_id = ?, site_id = ?, beacon_slot = ?, position = ?, updated_at = ?
 WHERE id = ?`,
		nullString(place.RoomID), nullString(siteID), place.Slot, place.Position, s.nowMilli(), deviceID)
	if err != nil {
		return fmt.Errorf("place device %s: %w", deviceID, err)
	}
	return checkOneRow(result, deviceID)
}

// slotIsFree refuses a beacon slot that is outside the plan of the room or
// that another target of the room already holds. Slot 0 means none.
func (s *Store) slotIsFree(ctx context.Context, room Room, deviceID, kind string, slot int) error {
	if slot == 0 || kind != KindTarget {
		return nil
	}
	if slot < 1 || slot > room.Slots {
		return fmt.Errorf("slot %d of room %s, which has %d slots: %w", slot, room.ID, room.Slots, ErrSlotTaken)
	}
	var taken string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM devices WHERE room_id = ? AND beacon_slot = ? AND id <> ?`, room.ID, slot, deviceID).Scan(&taken)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("check slot %d of room %s: %w", slot, room.ID, err)
	}
	return fmt.Errorf("slot %d of room %s belongs to %s: %w", slot, room.ID, taken, ErrSlotTaken)
}

// FreeSlots lists the slots of a room that no target holds, in order.
func (s *Store) FreeSlots(ctx context.Context, roomID string) ([]int, error) {
	room, err := s.GetRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	targets, err := s.TargetsOfRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	taken := map[int]bool{}
	for _, t := range targets {
		taken[t.BeaconSlot] = true
	}
	var free []int
	for slot := 1; slot <= room.Slots; slot++ {
		if !taken[slot] {
			free = append(free, slot)
		}
	}
	return free, nil
}

func scanSite(row rowScanner) (Site, error) {
	var (
		site  Site
		notes string
	)
	err := row.Scan(&site.ID, &site.Name, &site.Address, &site.Timezone, &site.Contact, &notes,
		&site.LicenceID, &site.FranchiseRate, &site.MonthlyThreshold, &site.Currency, &site.ValidFrom,
		&site.CreatedAt, &site.UpdatedAt)
	if err != nil {
		return Site{}, err
	}
	if err := json.Unmarshal([]byte(notes), &site.Notes); err != nil {
		return Site{}, fmt.Errorf("notes of site %s: %w", site.ID, err)
	}
	return site, nil
}

func scanRoom(row rowScanner) (Room, error) {
	var (
		room  Room
		notes string
	)
	err := row.Scan(&room.ID, &room.SiteID, &room.Name, &room.AgeRating, &room.WiFiChannel,
		&room.PeriodMS, &room.Slots, &room.Capacity, &notes, &room.CreatedAt, &room.UpdatedAt)
	if err != nil {
		return Room{}, err
	}
	if err := json.Unmarshal([]byte(notes), &room.Notes); err != nil {
		return Room{}, fmt.Errorf("notes of room %s: %w", room.ID, err)
	}
	return room, nil
}
