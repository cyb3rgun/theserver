package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/store"
)

// venue makes a site with one room through the pages and returns nothing;
// the ids are the ones it was given.
func (h *harness) venue(t *testing.T, siteID, roomID string, slots string) {
	t.Helper()
	if rec := h.do("POST", "/admin/sites", url.Values{
		"id": {siteID}, "name": {"Cinema One"}, "timezone": {"Europe/Berlin"},
		"currency": {"EUR"}, "franchise_rate": {"7"}, "monthly_threshold": {"500000"},
	}, true); rec.Code != 303 {
		t.Fatalf("the site create answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := h.do("POST", "/admin/sites/"+siteID+"/rooms", url.Values{
		"id": {roomID}, "name": {"Arena"}, "age_rating": {"16"},
		"beacon_period_ms": {"120"}, "beacon_slots": {slots}, "wifi_channel": {"36"}, "capacity": {"12"},
	}, true); rec.Code != 303 {
		t.Fatalf("the room create answered %d: %s", rec.Code, rec.Body.String())
	}
}

// The site pages: the list, the licence fields, the rooms and the refusals
// of a delete (D-066, D-070).
func TestSitePagesAndActions(t *testing.T) {
	h := newHarness(t)

	empty := h.html("GET", "/admin/sites", nil)
	contains(t, empty, "Sites", "No sites yet.", `id="new-site"`, "Site id", "Franchise rate")

	h.venue(t, "hall-1", "arena", "6")
	list := h.html("GET", "/admin/sites", nil)
	contains(t, list, "Cinema One", "Europe/Berlin", `href="/admin/sites/hall-1"`)

	site := h.html("GET", "/admin/sites/hall-1?created=1", nil)
	contains(t, site, "The site Cinema One is created.", "Licence", "7 percent", "500000 EUR",
		"Arena", "120 ms, 6 slots", `id="new-room"`, `id="edit-site"`)

	// The licence values are saved like every other field.
	saved := h.do("POST", "/admin/sites/hall-1", url.Values{
		"name": {"Cinema One"}, "timezone": {"Europe/Berlin"}, "currency": {"CHF"},
		"franchise_rate": {"5.5"}, "monthly_threshold": {"250000"}, "licence_id": {"LIC-0007"},
		"valid_from": {"2026-10-01"},
	}, true)
	if saved.Code != 303 {
		t.Fatalf("the save answered %d: %s", saved.Code, saved.Body.String())
	}
	after := h.html("GET", "/admin/sites/hall-1?saved=1", nil)
	contains(t, after, "is saved.", "5.5 percent", "250000 CHF", "LIC-0007")

	// A number that cannot be read keeps the page.
	bad := h.do("POST", "/admin/sites/hall-1", url.Values{
		"name": {"Cinema One"}, "franchise_rate": {"much"},
	}, true)
	if bad.Code != 400 {
		t.Fatalf("a bad number answered %d", bad.Code)
	}
	contains(t, bad.Body.String(), "A number could not be read")

	// A site with a room stays.
	refused := h.do("POST", "/admin/sites/hall-1/delete", url.Values{}, true)
	if refused.Code != 409 {
		t.Fatalf("deleting a site with rooms answered %d", refused.Code)
	}
	contains(t, refused.Body.String(), "in use")

	if rec := h.do("POST", "/admin/rooms/arena/delete", url.Values{}, true); rec.Code != 303 {
		t.Fatalf("the room delete answered %d: %s", rec.Code, rec.Body.String())
	}
	gone := h.do("POST", "/admin/sites/hall-1/delete", url.Values{}, true)
	if gone.Code != 303 || gone.Header().Get("Location") != "/admin/sites?deleted=hall-1" {
		t.Fatalf("the site delete answered %d to %q", gone.Code, gone.Header().Get("Location"))
	}
	if rec := h.do("GET", "/admin/sites/hall-1", nil, true); rec.Code != 404 {
		t.Errorf("the page of the deleted site answered %d", rec.Code)
	}
}

// The room page lists its targets with type, slot and status, and shows the
// plan with the free slots (D-066, D-068).
func TestRoomPageShowsTargetsAndPlan(t *testing.T) {
	h := newHarness(t)
	h.venue(t, "hall-1", "arena", "4")
	h.device("tgt-01", store.StatusApproved)
	h.device("tgt-02", store.StatusPending)
	h.html("POST", "/admin/devices/tgt-01/target-type", url.Values{"target_type": {"bar-12"}})
	h.html("POST", "/admin/devices/tgt-01/place", url.Values{
		"room_id": {"arena"}, "beacon_slot": {"2"}, "position": {"left of the door"},
	})
	h.html("POST", "/admin/devices/tgt-02/place", url.Values{"room_id": {"arena"}})

	page := h.html("GET", "/admin/rooms/arena?created=1", nil)
	contains(t, page, "Arena", "Beacon plan", "120 ms", "Targets in this room",
		"tgt-01", "Bar display 11.9 inch", "left of the door", "tgt-02", `id="edit-room"`)
	if !strings.Contains(page, "1, 3, 4") {
		t.Errorf("the free slots are not shown as 1, 3, 4:\n%s", page)
	}

	// A slot another target holds is refused, and the page says why.
	taken := h.html("POST", "/admin/devices/tgt-02/place", url.Values{"room_id": {"arena"}, "beacon_slot": {"2"}})
	contains(t, taken, "slot")
	device, err := h.st.GetDevice(context.Background(), "tgt-02")
	if err != nil || device.BeaconSlot != 0 {
		t.Fatalf("the refused device is %+v, %v", device, err)
	}

	// A room that holds devices stays.
	refused := h.do("POST", "/admin/rooms/arena/delete", url.Values{}, true)
	if refused.Code != 409 {
		t.Fatalf("deleting a room with devices answered %d", refused.Code)
	}

	// The device page says where the device stands and can move it out.
	devicePage := h.html("GET", "/admin/devices/tgt-01/view", nil)
	contains(t, devicePage, `id="place-form"`, "Cinema One, Arena", "slot 2", "left of the door")
	cleared := h.html("POST", "/admin/devices/tgt-01/place", url.Values{"room_id": {""}})
	contains(t, cleared, "Device tgt-01 stands in no room any more.")
}

// A session runs in a room and takes its targets (D-067).
func TestSessionsPageTakesARoom(t *testing.T) {
	h := newHarness(t)
	h.venue(t, "hall-1", "arena", "4")
	for _, id := range []string{"tgt-01", "tgt-02"} {
		h.device(id, store.StatusApproved)
		h.html("POST", "/admin/devices/"+id+"/place", url.Values{"room_id": {"arena"}})
	}

	page := h.html("GET", "/admin/sessions", nil)
	contains(t, page, `name="room_id"`, "Cinema One, Arena", "no room")

	created := h.html("POST", "/admin/sessions", url.Values{"id": {"evening"}, "room_id": {"arena"}})
	contains(t, created, "Session evening is created.", `href="/admin/rooms/arena"`, "tgt-01", "tgt-02")

	session, err := h.st.GetSession(context.Background(), "evening")
	if err != nil {
		t.Fatal(err)
	}
	if session.RoomID != "arena" || len(session.Devices) != 2 {
		t.Errorf("the session is %+v", session)
	}
}

// A target type takes a channel per cluster, and a layout of six is drawn
// with six dots (D-068).
func TestTargetTypeTakesChannels(t *testing.T) {
	h := newHarness(t)
	form := url.Values{
		"id": {"hex-24"}, "name.en": {"Hex target"}, "name.de": {"Sechseck"},
		"class": {"pi"}, "display_w_mm": {"600"}, "display_h_mm": {"300"},
		"res_w": {"1200"}, "res_h": {"600"}, "orientation": {"landscape"}, "sound": {"none"},
		"beacon.x":  {"0", "300", "600", "600", "300", "0", ""},
		"beacon.y":  {"0", "-20", "0", "300", "320", "300", ""},
		"beacon.ch": {"0", "1", "2", "3", "4", "5", "6"},
	}
	if rec := h.do("POST", "/admin/target-types", form, true); rec.Code != 303 {
		t.Fatalf("the create answered %d: %s", rec.Code, rec.Body.String())
	}
	page := h.html("GET", "/admin/target-types/hex-24", nil)
	contains(t, page, "Hex target", "Channel", "0,0,0 1,600,-40 2,1200,0 3,1200,600 4,600,640 5,0,600")
	if got := strings.Count(page, "<circle"); got != 6 {
		t.Errorf("the outline has %d dots, want 6", got)
	}

	stored, err := h.st.GetTargetType(context.Background(), "hex-24")
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range stored.Beacons {
		if b.Ch != i {
			t.Errorf("cluster %d is on channel %d", i+1, b.Ch)
		}
	}
}
