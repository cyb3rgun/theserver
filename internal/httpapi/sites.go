package httpapi

import (
	"net/http"

	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The shape of a venue over API v1 (D-066): sites hold rooms, rooms hold
// targets, and a device says which room it stands in and which slot of the
// beacon plan it holds.

// Site is a venue as API v1 shows it. The licence fields are set by the
// manufacturer and are stored and shown, not used yet (D-070):
// franchise_rate is a percentage, monthly_threshold is in the smallest unit
// of currency, and valid_from is unix milliseconds, 0 while it is not set.
type Site struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Address          string            `json:"address"`
	Timezone         string            `json:"timezone"`
	Contact          string            `json:"contact"`
	Notes            map[string]string `json:"notes"`
	LicenceID        string            `json:"licence_id"`
	FranchiseRate    float64           `json:"franchise_rate"`
	MonthlyThreshold int64             `json:"monthly_threshold"`
	Currency         string            `json:"currency"`
	ValidFrom        int64             `json:"valid_from"`
	CreatedAt        int64             `json:"created_at"`
	UpdatedAt        int64             `json:"updated_at"`
	// Rooms are the ids of the rooms of the site, Controllers the devices of
	// kind controller that belong to it.
	Rooms       []string `json:"rooms"`
	Controllers []string `json:"controllers"`
}

// Room is one room of a site. AgeRating is the age of its players: a
// scenario rated higher cannot be played there (D-067). PeriodMS and Slots
// are the beacon multiplex plan (D-068).
type Room struct {
	ID          string            `json:"id"`
	SiteID      string            `json:"site_id"`
	Name        string            `json:"name"`
	AgeRating   string            `json:"age_rating"`
	WiFiChannel int               `json:"wifi_channel"`
	PeriodMS    int               `json:"beacon_period_ms"`
	Slots       int               `json:"beacon_slots"`
	Capacity    int               `json:"capacity"`
	Notes       map[string]string `json:"notes"`
	CreatedAt   int64             `json:"created_at"`
	UpdatedAt   int64             `json:"updated_at"`
	// Targets are the devices of kind target in the room, by id.
	Targets []string `json:"targets"`
}

// RoomPlan is the beacon multiplex plan of a room with the slots in use
// (D-068).
type RoomPlan struct {
	RoomID   string    `json:"room_id"`
	PeriodMS int       `json:"beacon_period_ms"`
	Slots    int       `json:"beacon_slots"`
	Taken    []SlotUse `json:"taken"`
	Free     []int     `json:"free"`
}

// SlotUse is one slot of a plan and the target that holds it.
type SlotUse struct {
	Slot       int    `json:"slot"`
	DeviceID   string `json:"device_id"`
	TargetType string `json:"target_type"`
}

// DevicePlace is the body of POST /devices/{id}/place: where a device
// stands. An empty room_id takes the device out of its room; site_id is
// only read when no room is given, which is how a controller belongs to a
// site without belonging to a room (D-066).
type DevicePlace struct {
	RoomID   string `json:"room_id"`
	SiteID   string `json:"site_id"`
	Slot     int    `json:"beacon_slot"`
	Position string `json:"position"`
}

func siteJSON(s store.Site, rooms []store.Room, controllers []store.Device) Site {
	out := Site{
		ID: s.ID, Name: s.Name, Address: s.Address, Timezone: s.Timezone, Contact: s.Contact,
		Notes: s.Notes, LicenceID: s.LicenceID, FranchiseRate: s.FranchiseRate,
		MonthlyThreshold: s.MonthlyThreshold, Currency: s.Currency, ValidFrom: s.ValidFrom,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		Rooms: []string{}, Controllers: []string{},
	}
	for _, room := range rooms {
		out.Rooms = append(out.Rooms, room.ID)
	}
	for _, device := range controllers {
		out.Controllers = append(out.Controllers, device.ID)
	}
	return out
}

func siteStore(body Site) store.Site {
	return store.Site{
		ID: body.ID, Name: body.Name, Address: body.Address, Timezone: body.Timezone,
		Contact: body.Contact, Notes: scenario.Text(body.Notes), LicenceID: body.LicenceID,
		FranchiseRate: body.FranchiseRate, MonthlyThreshold: body.MonthlyThreshold,
		Currency: body.Currency, ValidFrom: body.ValidFrom,
	}
}

func roomJSON(r store.Room, targets []store.Device) Room {
	out := Room{
		ID: r.ID, SiteID: r.SiteID, Name: r.Name, AgeRating: r.AgeRating, WiFiChannel: r.WiFiChannel,
		PeriodMS: r.PeriodMS, Slots: r.Slots, Capacity: r.Capacity, Notes: r.Notes,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Targets: []string{},
	}
	for _, target := range targets {
		out.Targets = append(out.Targets, target.ID)
	}
	return out
}

func roomStore(body Room) store.Room {
	return store.Room{
		ID: body.ID, SiteID: body.SiteID, Name: body.Name, AgeRating: body.AgeRating,
		WiFiChannel: body.WiFiChannel, PeriodMS: body.PeriodMS, Slots: body.Slots,
		Capacity: body.Capacity, Notes: scenario.Text(body.Notes),
	}
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sites, err := s.opts.Store.ListSites(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]Site, 0, len(sites))
	for _, site := range sites {
		rooms, err := s.opts.Store.RoomsOfSite(ctx, site.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		controllers, err := s.opts.Store.ControllersOfSite(ctx, site.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out = append(out, siteJSON(site, rooms, controllers))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out})
}

func (s *Server) getSite(w http.ResponseWriter, r *http.Request) {
	s.writeSite(w, r, r.PathValue("id"), http.StatusOK)
}

func (s *Server) writeSite(w http.ResponseWriter, r *http.Request, id string, status int) {
	ctx := r.Context()
	site, err := s.opts.Store.GetSite(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rooms, err := s.opts.Store.RoomsOfSite(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	controllers, err := s.opts.Store.ControllersOfSite(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, siteJSON(site, rooms, controllers))
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	var body Site
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	created, err := s.opts.Store.CreateSite(r.Context(), siteStore(body))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "site created", "site", created.ID)
	w.Header().Set("Location", Prefix+"/sites/"+created.ID)
	writeJSON(w, http.StatusCreated, siteJSON(created, nil, nil))
}

func (s *Server) updateSite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body Site
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	update := siteStore(body)
	update.ID = id
	updated, err := s.opts.Store.UpdateSite(r.Context(), update)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "site changed", "site", updated.ID)
	s.writeSite(w, r, id, http.StatusOK)
}

func (s *Server) deleteSite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Store.DeleteSite(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "site deleted", "site", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var (
		rooms []store.Room
		err   error
	)
	if site := r.URL.Query().Get("site"); site != "" {
		rooms, err = s.opts.Store.RoomsOfSite(ctx, site)
	} else {
		rooms, err = s.opts.Store.ListRooms(ctx)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]Room, 0, len(rooms))
	for _, room := range rooms {
		targets, err := s.opts.Store.TargetsOfRoom(ctx, room.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out = append(out, roomJSON(room, targets))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": out})
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	s.writeRoom(w, r, r.PathValue("id"), http.StatusOK)
}

func (s *Server) writeRoom(w http.ResponseWriter, r *http.Request, id string, status int) {
	ctx := r.Context()
	room, err := s.opts.Store.GetRoom(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	targets, err := s.opts.Store.TargetsOfRoom(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, roomJSON(room, targets))
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	var body Room
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	created, err := s.opts.Store.CreateRoom(r.Context(), roomStore(body))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "room created", "room", created.ID, "site", created.SiteID)
	w.Header().Set("Location", Prefix+"/rooms/"+created.ID)
	writeJSON(w, http.StatusCreated, roomJSON(created, nil))
}

// updateRoom writes the values of a room. A plan that changed reaches every
// target of the room at once (D-068).
func (s *Server) updateRoom(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body Room
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	update := roomStore(body)
	update.ID = id
	updated, err := s.opts.Store.UpdateRoom(r.Context(), update)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "room changed", "room", updated.ID)
	targets, err := s.opts.Store.TargetsOfRoom(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, target := range targets {
		s.sendSetup(r, target.ID)
	}
	s.writeRoom(w, r, id, http.StatusOK)
}

func (s *Server) deleteRoom(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Store.DeleteRoom(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "room deleted", "room", id)
	w.WriteHeader(http.StatusNoContent)
}

// roomPlan answers the beacon plan of a room with the slots in use, so an
// operator sees which slot is free before a target is hung (D-068).
func (s *Server) roomPlan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	room, err := s.opts.Store.GetRoom(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	targets, err := s.opts.Store.TargetsOfRoom(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	free, err := s.opts.Store.FreeSlots(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	plan := RoomPlan{RoomID: room.ID, PeriodMS: room.PeriodMS, Slots: room.Slots, Taken: []SlotUse{}, Free: []int{}}
	for _, target := range targets {
		if target.BeaconSlot > 0 {
			plan.Taken = append(plan.Taken, SlotUse{
				Slot: target.BeaconSlot, DeviceID: target.ID, TargetType: target.TargetType,
			})
		}
	}
	plan.Free = append(plan.Free, free...)
	writeJSON(w, http.StatusOK, plan)
}

// placeDevice says where a device stands and tells it what follows from
// that: the plan of its room and its own slot (D-066, D-068).
func (s *Server) placeDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body DevicePlace
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	place := store.Placement{RoomID: body.RoomID, SiteID: body.SiteID, Slot: body.Slot, Position: body.Position}
	if err := s.opts.Store.PlaceDevice(ctx, id, place); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "device placed", "device", id, "room", body.RoomID, "site", body.SiteID, "slot", body.Slot)

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
