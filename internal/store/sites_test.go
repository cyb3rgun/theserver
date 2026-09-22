package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// site and room make the shape a test needs: one site with one room.
func (s *Store) mustSite(t *testing.T, id, name string) Site {
	t.Helper()
	site, err := s.CreateSite(context.Background(), Site{ID: id, Name: name, Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func (s *Store) mustRoom(t *testing.T, id, siteID, name string) Room {
	t.Helper()
	room, err := s.CreateRoom(context.Background(), Room{ID: id, SiteID: siteID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return room
}

// A site holds rooms, a room holds devices, and neither goes away while it
// still holds something (D-066).
func TestSitesAndRooms(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	site, err := s.CreateSite(ctx, Site{
		ID: "hall-1", Name: "Cinema One", Address: "Hauptstrasse 1", Timezone: "Europe/Berlin",
		Contact: "sascha@example.com", Notes: scenario.Text{"en": "The first venue.", "de": "Der erste Standort."},
		LicenceID: "LIC-0001", FranchiseRate: 7, MonthlyThreshold: 500000, Currency: "EUR", ValidFrom: 1750000000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case site.CreatedAt == 0 || site.UpdatedAt == 0:
		t.Errorf("the site has no times: %+v", site)
	case site.FranchiseRate != 7 || site.MonthlyThreshold != 500000 || site.Currency != "EUR":
		t.Errorf("the licence fields are %+v", site)
	case site.Notes.In("de") != "Der erste Standort.":
		t.Errorf("the notes are %+v", site.Notes)
	}
	if _, err := s.CreateSite(ctx, Site{ID: "hall-1", Name: "Again"}); !errors.Is(err, ErrSiteExists) {
		t.Errorf("a second site with the same id gave %v", err)
	}

	room, err := s.CreateRoom(ctx, Room{
		ID: "hall-1-arena", SiteID: "hall-1", Name: "Arena", AgeRating: "16",
		WiFiChannel: 36, PeriodMS: 120, Slots: 6, Capacity: 12,
		Notes: scenario.Text{"en": "Six lanes.", "de": "Sechs Bahnen."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if room.Slots != 6 || room.PeriodMS != 120 || room.AgeRating != "16" {
		t.Errorf("the room is %+v", room)
	}
	// A room without a plan takes the default one.
	plain := s.mustRoom(t, "hall-1-lounge", "hall-1", "Lounge")
	if plain.PeriodMS != 100 || plain.Slots != 8 || plain.AgeRating != "18" {
		t.Errorf("the default plan is %+v", plain)
	}
	if rooms, err := s.RoomsOfSite(ctx, "hall-1"); err != nil || len(rooms) != 2 {
		t.Errorf("the site holds %v, %v", rooms, err)
	}
	if _, err := s.CreateRoom(ctx, Room{ID: "x", SiteID: "nowhere", Name: "X"}); !errors.Is(err, ErrSiteNotFound) {
		t.Errorf("a room of an unknown site gave %v", err)
	}

	// A site that holds rooms stays.
	if err := s.DeleteSite(ctx, "hall-1"); !errors.Is(err, ErrSiteInUse) {
		t.Errorf("deleting a site with rooms gave %v", err)
	}

	// A room that holds a device stays.
	if err := s.UpsertDevice(ctx, Device{ID: "tgt-01", Kind: KindTarget}); err != nil {
		t.Fatal(err)
	}
	if err := s.PlaceDevice(ctx, "tgt-01", Placement{RoomID: "hall-1-lounge", Slot: 1, Position: "left of the door"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRoom(ctx, "hall-1-lounge"); !errors.Is(err, ErrRoomInUse) {
		t.Errorf("deleting a room with devices gave %v", err)
	}
	device, err := s.GetDevice(ctx, "tgt-01")
	if err != nil || device.RoomID != "hall-1-lounge" || device.SiteID != "hall-1" || device.BeaconSlot != 1 {
		t.Fatalf("the placed device is %+v, %v", device, err)
	}
	if device.Position != "left of the door" {
		t.Errorf("the position is %q", device.Position)
	}

	// Out of the room, and then both go away.
	if err := s.PlaceDevice(ctx, "tgt-01", Placement{}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRoom(ctx, "hall-1-lounge"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRoom(ctx, "hall-1-arena"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSite(ctx, "hall-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSite(ctx, "hall-1"); !errors.Is(err, ErrSiteNotFound) {
		t.Errorf("the deleted site reads as %v", err)
	}
	if _, err := s.GetRoom(ctx, "nothing"); !errors.Is(err, ErrRoomNotFound) {
		t.Errorf("an unknown room gave %v", err)
	}
}

// A site and a room whose values do not describe a venue are refused.
func TestSiteAndRoomAreChecked(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.mustSite(t, "hall-1", "Cinema One")

	badSites := map[string]Site{
		"no id":                     {ID: "Not An Id", Name: "X"},
		"no name":                   {ID: "ok-1"},
		"a rate over a hundred":     {ID: "ok-2", Name: "X", FranchiseRate: 101},
		"a threshold below zero":    {ID: "ok-3", Name: "X", MonthlyThreshold: -1},
		"a currency of two letters": {ID: "ok-4", Name: "X", Currency: "EU"},
	}
	for name, site := range badSites {
		if _, err := s.CreateSite(ctx, site); !errors.Is(err, ErrBadSite) {
			t.Errorf("%s gave %v", name, err)
		}
	}

	badRooms := map[string]Room{
		"no id":                       {ID: "Not An Id", SiteID: "hall-1", Name: "X"},
		"no site":                     {ID: "ok-1", Name: "X"},
		"no name":                     {ID: "ok-2", SiteID: "hall-1"},
		"a rating that is none":       {ID: "ok-3", SiteID: "hall-1", Name: "X", AgeRating: "21"},
		"a wifi channel that is none": {ID: "ok-4", SiteID: "hall-1", Name: "X", WiFiChannel: 500},
		"a period too short":          {ID: "ok-5", SiteID: "hall-1", Name: "X", PeriodMS: 5},
		"too many slots":              {ID: "ok-6", SiteID: "hall-1", Name: "X", Slots: MaxSlots + 1},
	}
	for name, room := range badRooms {
		if _, err := s.CreateRoom(ctx, room); !errors.Is(err, ErrBadRoom) {
			t.Errorf("%s gave %v", name, err)
		}
	}
}

// Two targets of a room cannot hold the same beacon slot, and a slot
// outside the plan is refused (D-068).
func TestBeaconSlotsAreUnique(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.mustSite(t, "hall-1", "Cinema One")
	if _, err := s.CreateRoom(ctx, Room{ID: "arena", SiteID: "hall-1", Name: "Arena", Slots: 3}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"tgt-01", "tgt-02"} {
		if err := s.UpsertDevice(ctx, Device{ID: id, Kind: KindTarget}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PlaceDevice(ctx, "tgt-01", Placement{RoomID: "arena", Slot: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.PlaceDevice(ctx, "tgt-02", Placement{RoomID: "arena", Slot: 2}); !errors.Is(err, ErrSlotTaken) {
		t.Errorf("a slot that is taken gave %v", err)
	}
	if err := s.PlaceDevice(ctx, "tgt-02", Placement{RoomID: "arena", Slot: 4}); !errors.Is(err, ErrSlotTaken) {
		t.Errorf("a slot outside the plan gave %v", err)
	}
	if err := s.PlaceDevice(ctx, "tgt-02", Placement{RoomID: "arena", Slot: 3}); err != nil {
		t.Fatal(err)
	}
	// The same device keeps its own slot when it is placed again.
	if err := s.PlaceDevice(ctx, "tgt-02", Placement{RoomID: "arena", Slot: 3, Position: "back"}); err != nil {
		t.Fatal(err)
	}
	free, err := s.FreeSlots(ctx, "arena")
	if err != nil || len(free) != 1 || free[0] != 1 {
		t.Errorf("the free slots are %v, %v", free, err)
	}
	if targets, err := s.TargetsOfRoom(ctx, "arena"); err != nil || len(targets) != 2 {
		t.Errorf("the room holds %v, %v", targets, err)
	}

	// A controller belongs to the site and takes no slot.
	if err := s.UpsertDevice(ctx, Device{ID: "ctl-01", Kind: KindController}); err != nil {
		t.Fatal(err)
	}
	if err := s.PlaceDevice(ctx, "ctl-01", Placement{SiteID: "hall-1"}); err != nil {
		t.Fatal(err)
	}
	controllers, err := s.ControllersOfSite(ctx, "hall-1")
	if err != nil || len(controllers) != 1 || controllers[0].ID != "ctl-01" {
		t.Errorf("the controllers of the site are %v, %v", controllers, err)
	}
	if controllers[0].RoomID != "" {
		t.Errorf("the free controller sits in room %q", controllers[0].RoomID)
	}
}

// A device is told the points of its type and the plan of its room (D-068).
func TestDeviceSetup(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.mustSite(t, "hall-1", "Cinema One")
	if _, err := s.CreateRoom(ctx, Room{ID: "arena", SiteID: "hall-1", Name: "Arena", PeriodMS: 120, Slots: 4}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDevice(ctx, Device{ID: "tgt-01", Kind: KindTarget}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceTargetType(ctx, "tgt-01", "bar-12"); err != nil {
		t.Fatal(err)
	}
	if err := s.PlaceDevice(ctx, "tgt-01", Placement{RoomID: "arena", Slot: 2}); err != nil {
		t.Fatal(err)
	}

	setup, err := s.DeviceSetup(ctx, "tgt-01")
	if err != nil || !setup.HasCalib || !setup.HasRoom {
		t.Fatalf("the setup is %+v, %v", setup, err)
	}
	want := []Setting{
		{KeyCalibPoints, "0,-138,-340 1,1618,-340 2,1618,660 3,-138,660"},
		{KeyCalibRect, "-138,-340 1618,-340 1618,660 -138,660"},
		{KeyBeaconPeriod, "120"},
		{KeyBeaconSlots, "4"},
		{KeyBeaconSlot, "2"},
	}
	got := setup.Settings()
	if len(got) != len(want) {
		t.Fatalf("the device is told %+v", got)
	}
	for i, setting := range want {
		if got[i] != setting {
			t.Errorf("setting %d is %+v, want %+v", i, got[i], setting)
		}
	}

	// A device without a room hears nothing about a plan.
	if err := s.PlaceDevice(ctx, "tgt-01", Placement{}); err != nil {
		t.Fatal(err)
	}
	setup, err = s.DeviceSetup(ctx, "tgt-01")
	if err != nil || setup.HasRoom {
		t.Fatalf("a device without a room has the setup %+v, %v", setup, err)
	}
	if len(setup.Settings()) != 2 {
		t.Errorf("it is told %+v", setup.Settings())
	}
}

// openAt opens a database with the migrations up to version only, which is
// how a database of an earlier release looks.
func openAt(t *testing.T, dir string, version int) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(filepath.Join(dir, FileName), DefaultBusyTimeout))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db, path: filepath.Join(dir, FileName), now: time.Now}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version > version {
			break
		}
		if err := s.apply(t.Context(), m); err != nil {
			t.Fatalf("migration %s: %v", m.name, err)
		}
	}
	return s
}

// What existed before the migration keeps its history: every device lands in
// the default site, in a room named after the text it carried, one without a
// text in a room Default, and a session that named a room by text runs in
// that room (D-069).
func TestMigrationPutsOldDevicesIntoRooms(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// A database as B12 left it: devices with a room as free text.
	old := openAt(t, dir, 10)
	for _, d := range []Device{
		{ID: "tgt-01", Kind: KindTarget, Room: "Arena"},
		{ID: "tgt-02", Kind: KindTarget, Room: "Arena"},
		{ID: "tgt-03", Kind: KindTarget, Room: "Lounge"},
		{ID: "ctl-01", Kind: KindController},
	} {
		if err := old.UpsertDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.db.ExecContext(ctx, `
INSERT INTO sessions (id, scenario, room, state, created_at, updated_at)
VALUES ('evening', '', 'Arena', 'created', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	// The migration of this pass runs on it.
	s := openTestDir(t, dir)
	site, err := s.GetSite(ctx, DefaultSiteID)
	if err != nil {
		t.Fatalf("the default site: %v", err)
	}
	if site.Name != "Default" || site.Notes.In("de") == "" {
		t.Errorf("the default site is %+v", site)
	}
	rooms, err := s.RoomsOfSite(ctx, DefaultSiteID)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Room{}
	for _, room := range rooms {
		byName[room.Name] = room
	}
	if len(rooms) != 3 {
		t.Fatalf("the move made the rooms %+v", rooms)
	}
	for _, name := range []string{"Arena", "Lounge", "Default"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("there is no room %s: %+v", name, rooms)
		}
	}

	want := map[string]string{"tgt-01": "Arena", "tgt-02": "Arena", "tgt-03": "Lounge", "ctl-01": "Default"}
	for id, roomName := range want {
		device, err := s.GetDevice(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if device.SiteID != DefaultSiteID {
			t.Errorf("%s landed in site %q", id, device.SiteID)
		}
		if device.RoomID != byName[roomName].ID {
			t.Errorf("%s landed in room %q, want %s", id, device.RoomID, roomName)
		}
		if device.Room != want[id] && id != "ctl-01" {
			t.Errorf("%s lost its room text: %q", id, device.Room)
		}
	}
	session, err := s.GetSession(ctx, "evening")
	if err != nil {
		t.Fatal(err)
	}
	if session.RoomID != byName["Arena"].ID {
		t.Errorf("the session runs in room %q, want Arena", session.RoomID)
	}

	// The seeds of B12 carry their channels now (D-068).
	bar, err := s.GetTargetType(ctx, "bar-12")
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range bar.Beacons {
		if b.Ch != i {
			t.Errorf("cluster %d of bar-12 is on channel %d", i+1, b.Ch)
		}
	}
}
