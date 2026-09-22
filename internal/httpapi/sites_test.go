package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario/scenariotest"
)

// venue makes a site with one room through the API and returns the room id.
func (h *apiHarness) venue(t *testing.T, siteID, roomID string, slots int) {
	t.Helper()
	decode[Site](t, h.call("POST", Prefix+"/sites",
		`{"id":"`+siteID+`","name":"Cinema One","timezone":"Europe/Berlin"}`), http.StatusCreated)
	decode[Room](t, h.call("POST", Prefix+"/rooms",
		`{"id":"`+roomID+`","site_id":"`+siteID+`","name":"Arena","age_rating":"16",
		  "beacon_period_ms":120,"beacon_slots":`+strconv.Itoa(slots)+`}`), http.StatusCreated)
}

// Sites hold rooms, rooms hold targets, and neither goes away while it
// still holds something (D-066).
func TestSiteAndRoomRoutes(t *testing.T) {
	h := newAPI(t)
	created := decode[Site](t, h.call("POST", Prefix+"/sites",
		`{"id":"hall-1","name":"Cinema One","address":"Hauptstrasse 1","timezone":"Europe/Berlin",
		  "contact":"sascha@example.com","notes":{"en":"The first venue.","de":"Der erste Standort."},
		  "licence_id":"LIC-0001","franchise_rate":7,"monthly_threshold":500000,"currency":"EUR","valid_from":1750000000000}`),
		http.StatusCreated)
	switch {
	case created.ID != "hall-1" || created.Name != "Cinema One":
		t.Errorf("the site is %+v", created)
	case created.FranchiseRate != 7 || created.MonthlyThreshold != 500000 || created.LicenceID != "LIC-0001":
		t.Errorf("the licence fields are %+v", created)
	case created.Notes["de"] == "":
		t.Errorf("the notes are %+v", created.Notes)
	}
	expectError(t, h.call("POST", Prefix+"/sites", `{"id":"hall-1","name":"Again"}`), http.StatusConflict, codeConflict)
	expectError(t, h.call("POST", Prefix+"/sites", `{"id":"Not An Id","name":"X"}`), http.StatusBadRequest, codeBadRequest)

	room := decode[Room](t, h.call("POST", Prefix+"/rooms",
		`{"id":"arena","site_id":"hall-1","name":"Arena","age_rating":"16","wifi_channel":36,
		  "beacon_period_ms":120,"beacon_slots":6,"capacity":12,"notes":{"en":"Six lanes.","de":"Sechs Bahnen."}}`),
		http.StatusCreated)
	if room.Slots != 6 || room.PeriodMS != 120 || room.AgeRating != "16" {
		t.Errorf("the room is %+v", room)
	}
	expectError(t, h.call("POST", Prefix+"/rooms", `{"id":"x","site_id":"nowhere","name":"X"}`), 404, codeNotFound)

	// The site names its rooms and its controllers.
	h.device("ctl-01", store.StatusApproved, nil)
	if _, err := h.st.GetDevice(context.Background(), "ctl-01"); err != nil {
		t.Fatal(err)
	}
	site := decode[Site](t, h.call("GET", Prefix+"/sites/hall-1", ""), 200)
	if len(site.Rooms) != 1 || site.Rooms[0] != "arena" {
		t.Errorf("the site holds the rooms %v", site.Rooms)
	}

	// A change keeps the id of the path.
	updated := decode[Site](t, h.call("PUT", Prefix+"/sites/hall-1",
		`{"id":"ignored","name":"Cinema One","timezone":"Europe/Berlin","currency":"CHF"}`), 200)
	if updated.ID != "hall-1" || updated.Currency != "CHF" {
		t.Errorf("the update gave %+v", updated)
	}
	expectError(t, h.call("PUT", Prefix+"/sites/nowhere", `{"name":"X"}`), 404, codeNotFound)

	changedRoom := decode[Room](t, h.call("PUT", Prefix+"/rooms/arena",
		`{"name":"Arena","age_rating":"12","beacon_period_ms":200,"beacon_slots":4}`), 200)
	if changedRoom.AgeRating != "12" || changedRoom.Slots != 4 || changedRoom.SiteID != "hall-1" {
		t.Errorf("the room after the change is %+v", changedRoom)
	}

	// A site that holds a room stays; a room that holds a device stays.
	expectError(t, h.call("DELETE", Prefix+"/sites/hall-1", ""), http.StatusConflict, codeInUse)
	h.device("tgt-01", store.StatusApproved, nil)
	decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-01/place",
		`{"room_id":"arena","beacon_slot":2,"position":"left of the door"}`), 200)
	expectError(t, h.call("DELETE", Prefix+"/rooms/arena", ""), http.StatusConflict, codeInUse)

	decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-01/place", `{}`), 200)
	if rec := h.call("DELETE", Prefix+"/rooms/arena", ""); rec.Code != http.StatusNoContent {
		t.Errorf("the room delete answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := h.call("DELETE", Prefix+"/sites/hall-1", ""); rec.Code != http.StatusNoContent {
		t.Errorf("the site delete answered %d: %s", rec.Code, rec.Body.String())
	}
	expectError(t, h.call("GET", Prefix+"/sites/hall-1", ""), 404, codeNotFound)
	expectError(t, h.call("GET", Prefix+"/rooms/arena", ""), 404, codeNotFound)
}

// A device says which room it stands in and which slot it holds; the plan
// of the room says which slots are free (D-068).
func TestRoomPlanAndPlacement(t *testing.T) {
	h := newAPI(t)
	h.venue(t, "hall-1", "arena", 3)
	for _, id := range []string{"tgt-01", "tgt-02"} {
		h.device(id, store.StatusApproved, nil)
	}
	decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-01/target-type", `{"target_type":"bar-12"}`), 200)

	placed := decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-01/place",
		`{"room_id":"arena","beacon_slot":1,"position":"left"}`), 200)
	switch {
	case placed.Device.RoomID != "arena" || placed.Device.SiteID != "hall-1":
		t.Errorf("the device stands in %+v", placed.Device)
	case placed.Device.BeaconSlot != 1 || placed.Device.Position != "left":
		t.Errorf("the placement is %+v", placed.Device)
	}

	// A slot that is taken and one outside the plan are refused.
	expectError(t, h.call("POST", Prefix+"/devices/tgt-02/place", `{"room_id":"arena","beacon_slot":1}`),
		http.StatusConflict, codeSlotTaken)
	expectError(t, h.call("POST", Prefix+"/devices/tgt-02/place", `{"room_id":"arena","beacon_slot":9}`),
		http.StatusConflict, codeSlotTaken)
	expectError(t, h.call("POST", Prefix+"/devices/tgt-02/place", `{"room_id":"nowhere"}`), 404, codeNotFound)
	decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-02/place", `{"room_id":"arena","beacon_slot":3}`), 200)

	plan := decode[RoomPlan](t, h.call("GET", Prefix+"/rooms/arena/plan", ""), 200)
	switch {
	case plan.PeriodMS != 120 || plan.Slots != 3:
		t.Errorf("the plan is %+v", plan)
	case len(plan.Taken) != 2 || plan.Taken[0].Slot != 1 || plan.Taken[0].DeviceID != "tgt-01":
		t.Errorf("the slots in use are %+v", plan.Taken)
	case plan.Taken[0].TargetType != "bar-12":
		t.Errorf("the type of the first target is %q", plan.Taken[0].TargetType)
	case len(plan.Free) != 1 || plan.Free[0] != 2:
		t.Errorf("the free slots are %v", plan.Free)
	}
	expectError(t, h.call("GET", Prefix+"/rooms/nowhere/plan", ""), 404, codeNotFound)

	// The room names its targets.
	room := decode[Room](t, h.call("GET", Prefix+"/rooms/arena", ""), 200)
	if len(room.Targets) != 2 {
		t.Errorf("the room holds %v", room.Targets)
	}
}

// A session runs in a room: it takes the targets of that room, one can be
// left out, and a scenario rated above the room is refused (D-067).
func TestSessionInARoom(t *testing.T) {
	h := newAPI(t)
	h.venue(t, "hall-1", "arena", 6)
	for _, id := range []string{"tgt-01", "tgt-02", "tgt-03"} {
		h.device(id, store.StatusApproved, nil)
		decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/"+id+"/place", `{"room_id":"arena"}`), 200)
	}
	// A device that is not approved does not join by itself.
	h.device("tgt-09", store.StatusPending, nil)
	decode[DeviceCalibrated](t, h.call("POST", Prefix+"/devices/tgt-09/place", `{"room_id":"arena"}`), 200)

	session := decode[Session](t, h.call("POST", Prefix+"/sessions", `{"id":"evening","room_id":"arena"}`), http.StatusCreated)
	if session.RoomID != "arena" || session.Room != "Arena" {
		t.Errorf("the session names the room %+v", session)
	}
	if len(session.Devices) != 3 {
		t.Fatalf("the session took %v", session.Devices)
	}
	expectError(t, h.call("POST", Prefix+"/sessions", `{"id":"nowhere","room_id":"nothing"}`), 404, codeNotFound)

	// One target is left out.
	fewer := decode[Session](t, h.call("DELETE", Prefix+"/sessions/evening/devices/tgt-02", ""), 200)
	if len(fewer.Devices) != 2 {
		t.Errorf("after the exclusion the session holds %v", fewer.Devices)
	}

	// The scenario is rated 12 and the room is set to 6, so it is refused
	// for the room, not for a single device (D-067).
	decode[Room](t, h.call("PUT", Prefix+"/rooms/arena",
		`{"name":"Arena","age_rating":"6","beacon_period_ms":120,"beacon_slots":6}`), 200)
	h.publishFixture(scenariotest.Video)
	rec := h.call("POST", Prefix+"/sessions/evening/scenario", `{"id":"night-range","version":1}`)
	expectError(t, rec, http.StatusConflict, codeAgeRating)
	if body := rec.Body.String(); !strings.Contains(body, "room Arena") {
		t.Errorf("the refusal does not name the room: %s", body)
	}

	// The room takes the rating back up and the assignment goes through.
	decode[Room](t, h.call("PUT", Prefix+"/rooms/arena",
		`{"name":"Arena","age_rating":"16","beacon_period_ms":120,"beacon_slots":6}`), 200)
	decode[SessionAssignment](t, h.call("POST", Prefix+"/sessions/evening/scenario",
		`{"id":"night-range","version":1}`), 200)
}
