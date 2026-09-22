package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/venue"
)

// The pages of the venue (D-066): the list of sites with the form that
// describes a new one, one page per site with its rooms, its controllers and
// the licence fields the manufacturer sets, and one page per room with its
// targets, their types and slots, and the beacon plan of the room.

type sitesData struct {
	layout
	Sites  []siteRow
	Fields []typeField
}

// siteRow is one line of the site list.
type siteRow struct {
	httpapi.Site
	Rooms       int
	Controllers int
}

type siteData struct {
	layout
	Site        httpapi.Site
	Note        string
	Fields      []typeField
	RoomFields  []typeField
	Rooms       []roomRow
	Controllers []httpapi.Device
	Script      string
}

// roomRow is one room of a site with what stands in it.
type roomRow struct {
	httpapi.Room
	Targets int
	Free    int
}

type roomData struct {
	layout
	Room    httpapi.Room
	Site    httpapi.Site
	Note    string
	Fields  []typeField
	Plan    httpapi.RoomPlan
	Targets []targetRow
	Free    []int
	Script  string
}

// targetRow is one target of a room as the room page shows it.
type targetRow struct {
	httpapi.Device
	TypeName string
	Online   bool
}

func (a *Admin) sitesPage(w http.ResponseWriter, r *http.Request, s session) {
	data := sitesData{layout: a.layout("admin.sites.title", "sites", s)}
	if !a.loadSites(w, r, s, &data) {
		return
	}
	if id := r.URL.Query().Get("deleted"); id != "" {
		data.Notice = i18n.T(s.Lang, "admin.sites.deleted", id)
	}
	a.render(w, s.Lang, http.StatusOK, "sites", "layout", data)
}

func (a *Admin) loadSites(w http.ResponseWriter, r *http.Request, s session, data *sitesData) bool {
	var list struct {
		Sites []httpapi.Site `json:"sites"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/sites", nil, &list), &data.alert) {
		return false
	}
	data.Sites = make([]siteRow, 0, len(list.Sites))
	for _, site := range list.Sites {
		data.Sites = append(data.Sites, siteRow{Site: site, Rooms: len(site.Rooms), Controllers: len(site.Controllers)})
	}
	if data.Fields == nil {
		data.Fields = venueFields(venue.SiteFields(), nil, nil, s.Lang, true)
	}
	return true
}

func (a *Admin) createSite(w http.ResponseWriter, r *http.Request, s session) {
	data := sitesData{layout: a.layout("admin.sites.title", "sites", s)}
	body, err := siteForm(r)
	var created httpapi.Site
	if err == nil {
		err = a.call(r, s, http.MethodPost, "/sites", body, &created)
		if errors.Is(err, errSessionEnded) {
			a.sessionEnded(w, r)
			return
		}
		if a.failed(w, r, err, &data.alert) {
			return
		}
	} else {
		data.Error = i18n.T(s.Lang, "admin.sites.bad_number")
	}
	if data.Error == "" {
		http.Redirect(w, r, "/admin/sites/"+url.PathEscape(created.ID)+"?created=1", http.StatusSeeOther)
		return
	}
	data.Fields = venueFields(venue.SiteFields(), siteValues(body), body.Notes, s.Lang, true)
	if a.loadSites(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusBadRequest, "sites", "layout", data)
	}
}

func (a *Admin) sitePage(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	data, ok := a.loadSite(w, r, s, id)
	if !ok {
		return
	}
	switch q := r.URL.Query(); {
	case q.Has("created"):
		data.Notice = i18n.T(s.Lang, "admin.sites.created", data.Site.Name)
	case q.Has("saved"):
		data.Notice = i18n.T(s.Lang, "admin.sites.saved", data.Site.Name)
	case q.Get("room_deleted") != "":
		data.Notice = i18n.T(s.Lang, "admin.rooms.deleted", q.Get("room_deleted"))
	}
	a.render(w, s.Lang, http.StatusOK, "site", "layout", data)
}

func (a *Admin) loadSite(w http.ResponseWriter, r *http.Request, s session, id string) (siteData, bool) {
	data := siteData{
		layout: a.layout("admin.sites.one", "sites", s),
		Script: a.scripts["scenarios.js"],
	}
	data.Back = "/admin/sites/" + url.PathEscape(id)
	err := a.call(r, s, http.MethodGet, "/sites/"+url.PathEscape(id), nil, &data.Site)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		data.alert = alert{Error: i18n.T(s.Lang, "admin.sites.unknown", id)}
		a.render(w, s.Lang, http.StatusNotFound, "site", "layout", data)
		return data, false
	}
	if a.failed(w, r, err, &data.alert) {
		return data, false
	}
	data.Title = data.Site.Name
	data.Note = textIn(data.Site.Notes, s.Lang)
	data.Fields = venueFields(venue.SiteFields(), siteValues(data.Site), data.Site.Notes, s.Lang, false)
	data.RoomFields = venueFields(venue.RoomFields(), nil, nil, s.Lang, true)

	var rooms struct {
		Rooms []httpapi.Room `json:"rooms"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/rooms?site="+url.QueryEscape(id), nil, &rooms), &data.alert) {
		return data, false
	}
	for _, room := range rooms.Rooms {
		var plan httpapi.RoomPlan
		if a.failed(w, r, a.call(r, s, http.MethodGet, "/rooms/"+url.PathEscape(room.ID)+"/plan", nil, &plan), &data.alert) {
			return data, false
		}
		data.Rooms = append(data.Rooms, roomRow{Room: room, Targets: len(room.Targets), Free: len(plan.Free)})
	}

	var devices struct {
		Devices []httpapi.Device `json:"devices"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/devices", nil, &devices), &data.alert) {
		return data, false
	}
	for _, device := range devices.Devices {
		if device.SiteID == id && device.Kind == "controller" {
			data.Controllers = append(data.Controllers, device)
		}
	}
	return data, true
}

func (a *Admin) saveSite(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	body, formErr := siteForm(r)
	body.ID = id
	var callErr error
	if formErr == nil {
		var saved httpapi.Site
		callErr = a.call(r, s, http.MethodPut, "/sites/"+url.PathEscape(id), body, &saved)
	}
	data, ok := a.loadSite(w, r, s, id)
	if !ok {
		return
	}
	if a.failed(w, r, callErr, &data.alert) {
		return
	}
	if formErr != nil {
		data.Error = i18n.T(s.Lang, "admin.sites.bad_number")
	}
	if data.Error == "" {
		http.Redirect(w, r, "/admin/sites/"+url.PathEscape(id)+"?saved=1", http.StatusSeeOther)
		return
	}
	data.Fields = venueFields(venue.SiteFields(), siteValues(body), body.Notes, s.Lang, false)
	a.render(w, s.Lang, http.StatusBadRequest, "site", "layout", data)
}

func (a *Admin) deleteSite(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	err := a.call(r, s, http.MethodDelete, "/sites/"+url.PathEscape(id), nil, nil)
	if err == nil {
		http.Redirect(w, r, "/admin/sites?deleted="+url.QueryEscape(id), http.StatusSeeOther)
		return
	}
	data, ok := a.loadSite(w, r, s, id)
	if !ok {
		return
	}
	if a.failed(w, r, err, &data.alert) {
		return
	}
	a.render(w, s.Lang, http.StatusConflict, "site", "layout", data)
}

// createRoom describes a room of a site from the form of the site page.
func (a *Admin) createRoom(w http.ResponseWriter, r *http.Request, s session) {
	siteID := r.PathValue("id")
	body, formErr := roomForm(r)
	body.SiteID = siteID
	var created httpapi.Room
	var callErr error
	if formErr == nil {
		callErr = a.call(r, s, http.MethodPost, "/rooms", body, &created)
		if errors.Is(callErr, errSessionEnded) {
			a.sessionEnded(w, r)
			return
		}
	}
	data, ok := a.loadSite(w, r, s, siteID)
	if !ok {
		return
	}
	if a.failed(w, r, callErr, &data.alert) {
		return
	}
	if formErr != nil {
		data.Error = i18n.T(s.Lang, "admin.sites.bad_number")
	}
	if data.Error == "" {
		http.Redirect(w, r, "/admin/rooms/"+url.PathEscape(created.ID)+"?created=1", http.StatusSeeOther)
		return
	}
	data.RoomFields = venueFields(venue.RoomFields(), roomValues(body), body.Notes, s.Lang, true)
	a.render(w, s.Lang, http.StatusBadRequest, "site", "layout", data)
}

func (a *Admin) roomPage(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	data, ok := a.loadRoom(w, r, s, id)
	if !ok {
		return
	}
	switch q := r.URL.Query(); {
	case q.Has("created"):
		data.Notice = i18n.T(s.Lang, "admin.rooms.created", data.Room.Name)
	case q.Has("saved"):
		data.Notice = i18n.T(s.Lang, "admin.rooms.saved", data.Room.Name)
	}
	a.render(w, s.Lang, http.StatusOK, "room", "layout", data)
}

func (a *Admin) loadRoom(w http.ResponseWriter, r *http.Request, s session, id string) (roomData, bool) {
	data := roomData{
		layout: a.layout("admin.rooms.one", "sites", s),
		Script: a.scripts["scenarios.js"],
	}
	data.Back = "/admin/rooms/" + url.PathEscape(id)
	err := a.call(r, s, http.MethodGet, "/rooms/"+url.PathEscape(id), nil, &data.Room)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		data.alert = alert{Error: i18n.T(s.Lang, "admin.rooms.unknown", id)}
		a.render(w, s.Lang, http.StatusNotFound, "room", "layout", data)
		return data, false
	}
	if a.failed(w, r, err, &data.alert) {
		return data, false
	}
	data.Title = data.Room.Name
	data.Note = textIn(data.Room.Notes, s.Lang)
	data.Fields = venueFields(venue.RoomFields(), roomValues(data.Room), data.Room.Notes, s.Lang, false)

	if a.failed(w, r, a.call(r, s, http.MethodGet, "/sites/"+url.PathEscape(data.Room.SiteID), nil, &data.Site), &data.alert) {
		return data, false
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/rooms/"+url.PathEscape(id)+"/plan", nil, &data.Plan), &data.alert) {
		return data, false
	}
	data.Free = data.Plan.Free

	var devices struct {
		Devices []httpapi.Device `json:"devices"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/devices", nil, &devices), &data.alert) {
		return data, false
	}
	_, names, ok := a.targetTypeChoices(w, r, s, &data.alert)
	if !ok {
		return data, false
	}
	for _, device := range devices.Devices {
		if device.RoomID != id {
			continue
		}
		row := targetRow{Device: device, TypeName: names[device.TargetType], Online: device.Online}
		data.Targets = append(data.Targets, row)
	}
	return data, true
}

func (a *Admin) saveRoom(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	body, formErr := roomForm(r)
	body.ID = id
	var callErr error
	if formErr == nil {
		var saved httpapi.Room
		callErr = a.call(r, s, http.MethodPut, "/rooms/"+url.PathEscape(id), body, &saved)
	}
	data, ok := a.loadRoom(w, r, s, id)
	if !ok {
		return
	}
	if a.failed(w, r, callErr, &data.alert) {
		return
	}
	if formErr != nil {
		data.Error = i18n.T(s.Lang, "admin.sites.bad_number")
	}
	if data.Error == "" {
		http.Redirect(w, r, "/admin/rooms/"+url.PathEscape(id)+"?saved=1", http.StatusSeeOther)
		return
	}
	data.Fields = venueFields(venue.RoomFields(), roomValues(body), body.Notes, s.Lang, false)
	a.render(w, s.Lang, http.StatusBadRequest, "room", "layout", data)
}

func (a *Admin) deleteRoom(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	data, ok := a.loadRoom(w, r, s, id)
	if !ok {
		return
	}
	siteID := data.Room.SiteID
	err := a.call(r, s, http.MethodDelete, "/rooms/"+url.PathEscape(id), nil, nil)
	if err == nil {
		http.Redirect(w, r, "/admin/sites/"+url.PathEscape(siteID)+"?room_deleted="+url.QueryEscape(id), http.StatusSeeOther)
		return
	}
	if a.failed(w, r, err, &data.alert) {
		return
	}
	a.render(w, s.Lang, http.StatusConflict, "room", "layout", data)
}

// placeDevice says on the device page where a device stands (D-066).
func (a *Admin) placeDevice(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	slot, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("beacon_slot")))
	body := httpapi.DevicePlace{
		RoomID:   strings.TrimSpace(r.PostFormValue("room_id")),
		SiteID:   strings.TrimSpace(r.PostFormValue("site_id")),
		Slot:     slot,
		Position: strings.TrimSpace(r.PostFormValue("position")),
	}
	var answer httpapi.DeviceCalibrated
	err := a.call(r, s, http.MethodPost, "/devices/"+url.PathEscape(id)+"/place", body, &answer)
	if errors.Is(err, errSessionEnded) {
		a.sessionEnded(w, r)
		return
	}
	data, ok := a.loadDevice(w, r, s, id)
	if !ok {
		return
	}
	if a.failed(w, r, err, &data.alert) {
		return
	}
	if data.Error == "" {
		switch {
		case body.RoomID == "":
			data.Notice = i18n.T(s.Lang, "admin.device.place_cleared", id)
		case body.Slot > 0:
			data.Notice = i18n.T(s.Lang, "admin.device.place_done_slot", id, data.RoomNames[body.RoomID], body.Slot)
		default:
			data.Notice = i18n.T(s.Lang, "admin.device.place_done", id, data.RoomNames[body.RoomID])
		}
	}
	a.render(w, s.Lang, http.StatusOK, "device", "layout", data)
}

// roomChoices reads the rooms for a select, with their names in the
// language of the page.
func (a *Admin) roomChoices(w http.ResponseWriter, r *http.Request, s session, out *alert) ([]roomChoice, map[string]string, bool) {
	var rooms struct {
		Rooms []httpapi.Room `json:"rooms"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/rooms", nil, &rooms), out) {
		return nil, nil, false
	}
	var sites struct {
		Sites []httpapi.Site `json:"sites"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/sites", nil, &sites), out) {
		return nil, nil, false
	}
	siteNames := map[string]string{}
	for _, site := range sites.Sites {
		siteNames[site.ID] = site.Name
	}
	choices := make([]roomChoice, 0, len(rooms.Rooms))
	names := map[string]string{}
	for _, room := range rooms.Rooms {
		label := room.Name
		if site := siteNames[room.SiteID]; site != "" {
			label = site + ", " + room.Name
		}
		choices = append(choices, roomChoice{ID: room.ID, Name: room.Name, Site: room.SiteID, Label: label})
		names[room.ID] = room.Name
	}
	return choices, names, true
}

// siteForm and roomForm read what the pages posted. A number that cannot be
// read gives an error, and what was typed comes back with it.
func siteForm(r *http.Request) (httpapi.Site, error) {
	if err := r.ParseForm(); err != nil {
		return httpapi.Site{}, err
	}
	out := httpapi.Site{
		ID:        strings.TrimSpace(r.PostFormValue("id")),
		Name:      strings.TrimSpace(r.PostFormValue("name")),
		Address:   strings.TrimSpace(r.PostFormValue("address")),
		Timezone:  strings.TrimSpace(r.PostFormValue("timezone")),
		Contact:   strings.TrimSpace(r.PostFormValue("contact")),
		LicenceID: strings.TrimSpace(r.PostFormValue("licence_id")),
		Currency:  strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency"))),
		Notes:     map[string]string{},
	}
	for _, lang := range i18n.Languages() {
		if text := strings.TrimSpace(r.PostFormValue("notes." + lang)); text != "" {
			out.Notes[lang] = text
		}
	}
	var err error
	out.FranchiseRate, err = number(r, "franchise_rate", err)
	threshold, err := number(r, "monthly_threshold", err)
	out.MonthlyThreshold = int64(threshold)
	out.ValidFrom, err = day(r, "valid_from", err)
	return out, err
}

func roomForm(r *http.Request) (httpapi.Room, error) {
	if err := r.ParseForm(); err != nil {
		return httpapi.Room{}, err
	}
	out := httpapi.Room{
		ID:        strings.TrimSpace(r.PostFormValue("id")),
		Name:      strings.TrimSpace(r.PostFormValue("name")),
		AgeRating: r.PostFormValue("age_rating"),
		Notes:     map[string]string{},
	}
	for _, lang := range i18n.Languages() {
		if text := strings.TrimSpace(r.PostFormValue("notes." + lang)); text != "" {
			out.Notes[lang] = text
		}
	}
	var err error
	channel, err := number(r, "wifi_channel", err)
	period, err := number(r, "beacon_period_ms", err)
	slots, err := number(r, "beacon_slots", err)
	capacity, err := number(r, "capacity", err)
	out.WiFiChannel, out.PeriodMS, out.Slots, out.Capacity = int(channel), int(period), int(slots), int(capacity)
	return out, err
}

// day reads a date field as unix milliseconds; an empty field is 0.
func day(r *http.Request, key string, err error) (int64, error) {
	text := strings.TrimSpace(r.PostFormValue(key))
	if text == "" {
		return 0, err
	}
	when, parseErr := time.Parse("2006-01-02", text)
	if parseErr != nil && err == nil {
		return 0, fmt.Errorf("%s: %w", key, parseErr)
	}
	return when.UTC().UnixMilli(), err
}

// siteValues and roomValues are what the form fields show, by key.
func siteValues(site httpapi.Site) map[string]string {
	valid := ""
	if site.ValidFrom > 0 {
		valid = time.UnixMilli(site.ValidFrom).UTC().Format("2006-01-02")
	}
	return map[string]string{
		"id": site.ID, "name": site.Name, "address": site.Address, "timezone": site.Timezone,
		"contact": site.Contact, "licence_id": site.LicenceID,
		"franchise_rate":    trimNumber(site.FranchiseRate),
		"monthly_threshold": strconv.FormatInt(site.MonthlyThreshold, 10),
		"currency":          site.Currency, "valid_from": valid,
	}
}

func roomValues(room httpapi.Room) map[string]string {
	return map[string]string{
		"id": room.ID, "name": room.Name, "age_rating": room.AgeRating,
		"wifi_channel":     strconv.Itoa(room.WiFiChannel),
		"beacon_period_ms": strconv.Itoa(room.PeriodMS),
		"beacon_slots":     strconv.Itoa(room.Slots),
		"capacity":         strconv.Itoa(room.Capacity),
	}
}

// venueFields fills the form of a site or a room from its registry. withID
// shows the id field, which only a new one has; notes is filled from the
// texts of the object.
func venueFields(fields []venue.Field, values map[string]string, notes map[string]string, lang string, withID bool) []typeField {
	var out []typeField
	for _, f := range fields {
		if f.Key == "id" && !withID {
			continue
		}
		texts := f.In(lang)
		field := typeField{
			Key: f.Key, ID: f.ID(), Control: f.Control, Unit: f.Unit, Enum: f.Enum,
			Label: texts.Label, Description: texts.Description, Why: texts.Why,
			Optional: f.Optional, Fixed: f.Licence, Step: "1", Value: values[f.Key],
		}
		if f.Control == venue.ControlTexts {
			field.Texts = textRows(f.Key, notes, lang)
		}
		if f.Decimals {
			field.Step = "any"
		}
		if f.Min != nil {
			field.Min = trimNumber(*f.Min)
		}
		if f.Max != nil {
			field.Max = trimNumber(*f.Max)
		}
		for _, value := range f.Enum {
			field.Options = append(field.Options, selectOption{Value: value, Label: i18n.T(lang, "admin.age."+value)})
		}
		out = append(out, field)
	}
	return out
}
