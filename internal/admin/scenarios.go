package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The scenario pages (D-036 to D-040): the catalogue with the upload, a page
// per scenario with its versions, the problems of each draft in the language
// of the page, publish and delete for drafts, and the devices that hold the
// versions. Everything goes through API v1.

// problemView is a validation problem as a page shows it: the translated
// text of its code, the field, and the English detail below.
type problemView struct {
	Code   string
	Field  string
	Text   string
	Detail string
}

func problemsIn(lang string, problems []scenario.Problem) []problemView {
	views := make([]problemView, 0, len(problems))
	for _, p := range problems {
		views = append(views, problemView{Code: p.Code, Field: p.Field, Text: i18n.T(lang, "scenario.problem."+p.Code), Detail: p.Detail})
	}
	return views
}

// titleIn is the title of a scenario in lang, else in English, else its id.
func titleIn(lang string, title map[string]string, id string) string {
	if text := scenario.Text(title).In(lang); text != "" {
		return text
	}
	return id
}

type catalogueEntry struct {
	httpapi.ScenarioSummary
	Title string
}

type scenariosData struct {
	layout
	Scenarios []catalogueEntry
	// Drafts are the working copies of the editor (D-041).
	Drafts []draftEntry
	// Problems are those of an upload that was not stored.
	Problems    []problemView
	MaxUploadMB int
	Tiers       []tierChoice
	// Types are the target types a new draft can take its canvas from
	// (D-062).
	Types  []typeChoice
	Script string // integrity of static/scenarios.js
}

// draftEntry is one editor draft in the catalogue.
type draftEntry struct {
	httpapi.Draft
	Name     string
	TierName string
	Problems int
}

// tierChoice is a tier a new draft can be opened in.
type tierChoice struct {
	Value string
	Label string
}

func (a *Admin) newScenariosData(s session) scenariosData {
	return scenariosData{layout: a.layout("admin.scenarios.title", "scenarios", s), Script: a.scripts["scenarios.js"]}
}

// loadCatalogue reads the catalogue, the upload limit and the target types.
func (a *Admin) loadCatalogue(w http.ResponseWriter, r *http.Request, s session, data *scenariosData) bool {
	types, _, ok := a.targetTypeChoices(w, r, s, &data.alert)
	if !ok {
		return false
	}
	data.Types = types
	var list httpapi.ScenarioList
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/scenarios", nil, &list), &data.alert) {
		return false
	}
	for _, sc := range list.Scenarios {
		data.Scenarios = append(data.Scenarios, catalogueEntry{ScenarioSummary: sc, Title: titleIn(s.Lang, sc.Current.Title, sc.ID)})
	}
	var drafts httpapi.DraftList
	if err := a.call(r, s, http.MethodGet, "/drafts", nil, &drafts); err == nil {
		for _, d := range drafts.Drafts {
			data.Drafts = append(data.Drafts, draftEntry{
				Draft:    d,
				Name:     titleIn(s.Lang, d.Title, d.ScenarioID),
				TierName: i18n.T(s.Lang, "admin.tier."+d.Tier),
				Problems: len(d.Problems),
			})
		}
	}
	for _, tier := range httpapi.EditorTiers() {
		data.Tiers = append(data.Tiers, tierChoice{Value: tier, Label: i18n.T(s.Lang, "admin.tier."+tier)})
	}
	var settingsList httpapi.SettingsList
	if err := a.call(r, s, http.MethodGet, "/settings", nil, &settingsList); err == nil {
		for _, v := range settingsList.Settings {
			if v.Key == "content.max_upload_mb" {
				data.MaxUploadMB, _ = strconv.Atoi(plain(v.Value))
			}
		}
	}
	return true
}

func (a *Admin) scenariosPage(w http.ResponseWriter, r *http.Request, s session) {
	data := a.newScenariosData(s)
	if !a.loadCatalogue(w, r, s, &data) {
		return
	}
	switch query := r.URL.Query(); {
	case query.Get("deleted") != "":
		data.Notice = i18n.T(s.Lang, "admin.scenario.deleted_last", query.Get("deleted"))
	case query.Get("draft_deleted") != "":
		data.Notice = i18n.T(s.Lang, "admin.scenarios.draft_deleted")
	case query.Get("draft_gone") != "":
		data.Notice = i18n.T(s.Lang, "admin.scenarios.draft_gone")
	}
	a.render(w, s.Lang, http.StatusOK, "scenarios", "layout", data)
}

// uploadScenario hands the multipart body to the API as it comes. A stored
// draft leads to its page; a package that was not stored is shown on the
// catalogue with its problems.
func (a *Admin) uploadScenario(w http.ResponseWriter, r *http.Request, s session) {
	var result httpapi.Upload
	err := a.send(r, s, http.MethodPost, "/scenarios", r.Body, r.Header.Get("Content-Type"), &result)
	if err == nil {
		target := fmt.Sprintf("/admin/scenarios/%s?uploaded=%d", url.PathEscape(result.Scenario.ID), result.Scenario.Version)
		if result.Replaced {
			target += "&replaced=1"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}

	data := a.newScenariosData(s)
	status := http.StatusBadRequest
	var ae *apiError
	if errors.As(err, &ae) {
		status = ae.Status
	}
	if errors.As(err, &ae) && len(ae.Problems) > 0 {
		data.alert = alert{Error: i18n.T(s.Lang, "admin.scenarios.refused"), ErrorDetail: ae.Message}
		data.Problems = problemsIn(s.Lang, ae.Problems)
	} else if a.failed(w, r, err, &data.alert) {
		return
	}
	refused := data.alert
	if !a.loadCatalogue(w, r, s, &data) {
		return
	}
	data.alert = refused
	a.render(w, s.Lang, status, "scenarios", "layout", data)
}

type versionView struct {
	httpapi.ScenarioVersion
	Checks      []problemView
	Publishable bool
	// Stale is a draft at or below the latest published version.
	Stale bool
}

type holdingView struct {
	httpapi.Holding
	Title    string
	UpToDate bool
}

func holdingsIn(holdings []httpapi.Holding, titles map[string]string) []holdingView {
	views := make([]holdingView, 0, len(holdings))
	for _, h := range holdings {
		title := titles[h.ScenarioID]
		if title == "" {
			title = h.ScenarioID
		}
		views = append(views, holdingView{Holding: h, Title: title, UpToDate: h.Current && h.Latest > 0 && h.Version >= h.Latest})
	}
	return views
}

type scenarioData struct {
	layout
	Script   string // integrity of static/scenarios.js
	ID       string
	Name     string
	Current  httpapi.ScenarioVersion
	Latest   int
	Versions []versionView
	Holdings []holdingView
}

func (a *Admin) scenarioPage(w http.ResponseWriter, r *http.Request, s session) {
	data, ok := a.loadScenario(w, r, s, r.PathValue("id"))
	if !ok {
		return
	}
	q := r.URL.Query()
	switch {
	case q.Has("uploaded") && q.Has("replaced"):
		data.Notice = i18n.T(s.Lang, "admin.scenario.replaced", atoi(q.Get("uploaded")))
	case q.Has("uploaded"):
		data.Notice = i18n.T(s.Lang, "admin.scenario.uploaded", atoi(q.Get("uploaded")))
	case q.Has("published"):
		data.Notice = i18n.T(s.Lang, "admin.scenario.published_done", atoi(q.Get("published")))
	case q.Has("deleted"):
		data.Notice = i18n.T(s.Lang, "admin.scenario.deleted", atoi(q.Get("deleted")))
	}
	a.render(w, s.Lang, http.StatusOK, "scenario", "layout", data)
}

// loadScenario reads one scenario. A scenario the API does not know is a
// 404 page.
func (a *Admin) loadScenario(w http.ResponseWriter, r *http.Request, s session, id string) (scenarioData, bool) {
	data := scenarioData{layout: a.layout("admin.scenario.title", "scenarios", s), ID: id, Name: id, Script: a.scripts["scenarios.js"]}
	data.Back = "/admin/scenarios/" + url.PathEscape(id)
	var detail httpapi.ScenarioDetail
	err := a.call(r, s, http.MethodGet, "/scenarios/"+url.PathEscape(id), nil, &detail)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		data.alert = alert{Error: i18n.T(s.Lang, "admin.scenario.unknown", id)}
		a.render(w, s.Lang, http.StatusNotFound, "scenario", "layout", data)
		return data, false
	}
	if a.failed(w, r, err, &data.alert) {
		return data, false
	}
	data.Latest = detail.Latest
	for _, v := range detail.Versions {
		view := versionView{
			ScenarioVersion: v,
			Checks:          problemsIn(s.Lang, v.Problems),
			Publishable:     v.Status == "draft" && len(v.Problems) == 0 && v.Version > detail.Latest,
			Stale:           v.Status == "draft" && v.Version <= detail.Latest,
		}
		data.Versions = append(data.Versions, view)
		if data.Current.ID == "" || v.Version == detail.Latest {
			data.Current = v
		}
	}
	data.Name = titleIn(s.Lang, data.Current.Title, id)
	data.Title = i18n.T(s.Lang, "admin.scenario.title_of", data.Name)
	data.Holdings = holdingsIn(detail.Holdings, map[string]string{id: data.Name})
	return data, true
}

// scenarioAction publishes or deletes a draft and goes back to the page.
func (a *Admin) scenarioAction(w http.ResponseWriter, r *http.Request, s session) {
	id, action := r.PathValue("id"), r.PathValue("action")
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		http.NotFound(w, r)
		return
	}
	base := "/scenarios/" + url.PathEscape(id) + "/" + strconv.Itoa(version)
	var notice string
	switch action {
	case "publish":
		err = a.call(r, s, http.MethodPost, base+"/publish", nil, nil)
		notice = "published"
	case "delete":
		err = a.call(r, s, http.MethodDelete, base, nil, nil)
		notice = "deleted"
	default:
		http.NotFound(w, r)
		return
	}
	page := "/admin/scenarios/" + url.PathEscape(id)
	if err == nil {
		if action == "delete" && !a.scenarioExists(r, s, id) {
			http.Redirect(w, r, "/admin/scenarios?deleted="+url.QueryEscape(fmt.Sprintf("%s %d", id, version)), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("%s?%s=%d", page, notice, version), http.StatusSeeOther)
		return
	}
	var refused alert
	if a.failed(w, r, err, &refused) {
		return
	}
	data, ok := a.loadScenario(w, r, s, id)
	if !ok {
		return
	}
	data.alert = refused
	a.render(w, s.Lang, http.StatusConflict, "scenario", "layout", data)
}

func (a *Admin) scenarioExists(r *http.Request, s session, id string) bool {
	return a.call(r, s, http.MethodGet, "/scenarios/"+url.PathEscape(id), nil, nil) == nil
}

// placeholderCover stands in for a package without a cover.
const placeholderCover = `<svg xmlns="http://www.w3.org/2000/svg" width="90" height="160" viewBox="0 0 90 160">` +
	`<rect width="90" height="160" fill="#d6d9dd"/><path d="M25 60h40v40H25z" fill="none" stroke="#5f6368" stroke-width="3"/></svg>`

// scenarioCover shows the cover of a version, or a neutral placeholder.
func (a *Admin) scenarioCover(w http.ResponseWriter, r *http.Request, s session) {
	rec := httptest.NewRecorder()
	a.forward(rec, r, s, "/scenarios/"+url.PathEscape(r.PathValue("id"))+"/"+url.PathEscape(r.PathValue("version"))+"/cover.png")
	switch rec.Code {
	case http.StatusOK:
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Write(rec.Body.Bytes())
	case http.StatusUnauthorized, http.StatusForbidden:
		a.sessionEnded(w, r)
	default:
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(placeholderCover))
	}
}

// scenarioPackage lets an admin download a package, Range included.
func (a *Admin) scenarioPackage(w http.ResponseWriter, r *http.Request, s session) {
	a.forward(w, r, s, "/scenarios/"+url.PathEscape(r.PathValue("id"))+"/"+url.PathEscape(r.PathValue("version"))+"/package.zip")
}

// forward runs a GET of API v1 in process and writes its answer as it comes.
func (a *Admin) forward(w http.ResponseWriter, r *http.Request, s session, path string) {
	req, err := http.NewRequestWithContext(httpapi.AsAdmin(r.Context(), s.TokenID), r.Method, httpapi.Prefix+path, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, name := range []string{"Range", "If-Range", "If-Modified-Since"} {
		if v := r.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	req.RemoteAddr = r.RemoteAddr
	a.opts.API.ServeHTTP(w, req)
}

// scenarioChoice is a published scenario version a session can play.
type scenarioChoice struct {
	Value     string // id@version
	ID        string
	Version   int
	Title     string
	AgeRating string
}

// choices lists the latest published version of every scenario, and the
// titles of all of them.
func choicesIn(lang string, list httpapi.ScenarioList) ([]scenarioChoice, map[string]string) {
	var choices []scenarioChoice
	titles := map[string]string{}
	for _, sc := range list.Scenarios {
		titles[sc.ID] = titleIn(lang, sc.Current.Title, sc.ID)
		if sc.Latest == nil {
			continue
		}
		choices = append(choices, scenarioChoice{
			Value: sc.ID + "@" + strconv.Itoa(sc.Latest.Version), ID: sc.ID, Version: sc.Latest.Version,
			Title: titles[sc.ID], AgeRating: sc.Latest.AgeRating,
		})
	}
	return choices, titles
}

// parseChoice reads id@version.
func parseChoice(value string) (string, int, bool) {
	id, v, ok := strings.Cut(value, "@")
	version, err := strconv.Atoi(v)
	return id, version, ok && err == nil && version > 0 && id != ""
}

// announcementLines turns the answer of an assignment into the lines of
// the notice.
func announcementLines(lang string, announcements []httpapi.Announcement) []string {
	var taken, waiting []string
	var lines []string
	for _, an := range announcements {
		switch an.State {
		case httpapi.AnnouncedTaken:
			taken = append(taken, an.DeviceID)
		case httpapi.AnnouncedWaiting:
			waiting = append(waiting, an.DeviceID)
		default:
			lines = append(lines, i18n.T(lang, "admin.sessions.announce_failed", an.DeviceID, an.Error))
		}
	}
	if len(taken) > 0 {
		lines = append([]string{i18n.T(lang, "admin.sessions.announced", strings.Join(taken, ", "))}, lines...)
	}
	if len(waiting) > 0 {
		lines = append(lines, i18n.T(lang, "admin.sessions.announce_waiting", strings.Join(waiting, ", ")))
	}
	if len(announcements) == 0 {
		lines = append(lines, i18n.T(lang, "admin.sessions.announce_none"))
	}
	return lines
}

type deviceData struct {
	layout
	Device      httpapi.Device
	Holdings    []holdingView
	Assignments []httpapi.Assignment
	Titles      map[string]string
	Ages        []int
	// Types are the target types to choose from, TypeNames their names in
	// the language of the page (D-065).
	Types     []typeChoice
	TypeNames map[string]string
	// Rooms are the rooms the device can stand in and RoomNames their
	// names, by id; Slots are the free slots of the room it stands in,
	// with its own slot among them (D-066).
	Rooms     []roomChoice
	RoomNames map[string]string
	Slots     []int
}

// roomChoice is one room in a select, with the site it belongs to.
type roomChoice struct {
	ID    string
	Name  string
	Site  string
	Label string
}

// typeChoice is one target type in a select.
type typeChoice struct {
	ID    string
	Label string
}

func (a *Admin) devicePage(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	data, ok := a.loadDevice(w, r, s, id)
	if !ok {
		return
	}
	if q := r.URL.Query(); q.Has("age") {
		data.Notice = i18n.T(s.Lang, "admin.device.age_done", id, i18n.T(s.Lang, "admin.age."+q.Get("age")))
	}
	a.render(w, s.Lang, http.StatusOK, "device", "layout", data)
}

func (a *Admin) loadDevice(w http.ResponseWriter, r *http.Request, s session, id string) (deviceData, bool) {
	data := deviceData{layout: a.layout("admin.device.title", "devices", s), Ages: store.MinAges(), Titles: map[string]string{}}
	data.Title = i18n.T(s.Lang, "admin.device.title_of", id)
	data.Back = "/admin/devices/" + url.PathEscape(id) + "/view"
	path := "/devices/" + url.PathEscape(id)
	err := a.call(r, s, http.MethodGet, path, nil, &data.Device)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		data.alert = alert{Error: i18n.T(s.Lang, "admin.device.unknown", id)}
		a.render(w, s.Lang, http.StatusNotFound, "device", "layout", data)
		return data, false
	}
	if a.failed(w, r, err, &data.alert) {
		return data, false
	}
	var held httpapi.DeviceScenarios
	var list httpapi.ScenarioList
	if a.failed(w, r, a.call(r, s, http.MethodGet, path+"/scenarios", nil, &held), &data.alert) {
		return data, false
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/scenarios", nil, &list), &data.alert) {
		return data, false
	}
	_, data.Titles = choicesIn(s.Lang, list)
	data.Holdings = holdingsIn(held.Holdings, data.Titles)
	data.Assignments = held.Assignments
	types, names, ok := a.targetTypeChoices(w, r, s, &data.alert)
	if !ok {
		return data, false
	}
	data.Types, data.TypeNames = types, names
	if !a.loadRoomChoices(w, r, s, &data) {
		return data, false
	}
	return data, true
}

// loadRoomChoices reads the rooms a device can stand in and the slots that
// are free in the room it stands in now (D-066, D-068).
func (a *Admin) loadRoomChoices(w http.ResponseWriter, r *http.Request, s session, data *deviceData) bool {
	rooms, names, ok := a.roomChoices(w, r, s, &data.alert)
	if !ok {
		return false
	}
	data.Rooms, data.RoomNames = rooms, names
	if data.Device.RoomID != "" {
		var plan httpapi.RoomPlan
		if a.failed(w, r, a.call(r, s, http.MethodGet, "/rooms/"+url.PathEscape(data.Device.RoomID)+"/plan", nil, &plan), &data.alert) {
			return false
		}
		data.Slots = append(data.Slots, plan.Free...)
		if data.Device.BeaconSlot > 0 {
			data.Slots = append(data.Slots, data.Device.BeaconSlot)
			slices.Sort(data.Slots)
		}
	}
	return true
}

// targetTypeChoices reads the target types for a select, with their names
// in the language of the page.
func (a *Admin) targetTypeChoices(w http.ResponseWriter, r *http.Request, s session, out *alert) ([]typeChoice, map[string]string, bool) {
	var list struct {
		TargetTypes []httpapi.TargetType `json:"target_types"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/target-types", nil, &list), out) {
		return nil, nil, false
	}
	choices := make([]typeChoice, 0, len(list.TargetTypes))
	names := map[string]string{}
	for _, t := range list.TargetTypes {
		label := textIn(t.Name, s.Lang)
		choices = append(choices, typeChoice{ID: t.ID, Label: label})
		names[t.ID] = label
	}
	return choices, names, true
}

// setDeviceAge sets the age a device is set for.
func (a *Admin) setDeviceAge(w http.ResponseWriter, r *http.Request, s session) {
	id := r.PathValue("id")
	age, err := strconv.Atoi(r.PostFormValue("min_age"))
	body := map[string]any{"min_age": age}
	if err != nil {
		body = map[string]any{"min_age": r.PostFormValue("min_age")}
	}
	err = a.call(r, s, http.MethodPost, "/devices/"+url.PathEscape(id)+"/min_age", body, nil)
	if err == nil {
		http.Redirect(w, r, fmt.Sprintf("/admin/devices/%s/view?age=%d", url.PathEscape(id), age), http.StatusSeeOther)
		return
	}
	var refused alert
	if a.failed(w, r, err, &refused) {
		return
	}
	data, ok := a.loadDevice(w, r, s, id)
	if !ok {
		return
	}
	data.alert = refused
	status := http.StatusBadRequest
	var ae *apiError
	if errors.As(err, &ae) {
		status = ae.Status
	}
	a.render(w, s.Lang, status, "device", "layout", data)
}
