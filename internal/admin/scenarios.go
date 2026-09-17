package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/store"
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
	// Problems are those of an upload that was not stored.
	Problems    []problemView
	MaxUploadMB int
	Script      string // integrity of static/scenarios.js
}

func (a *Admin) newScenariosData(s session) scenariosData {
	return scenariosData{layout: a.layout("admin.scenarios.title", "scenarios", s), Script: a.scripts["scenarios.js"]}
}

// loadCatalogue reads the catalogue and the upload limit.
func (a *Admin) loadCatalogue(w http.ResponseWriter, r *http.Request, s session, data *scenariosData) bool {
	var list httpapi.ScenarioList
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/scenarios", nil, &list), &data.alert) {
		return false
	}
	for _, sc := range list.Scenarios {
		data.Scenarios = append(data.Scenarios, catalogueEntry{ScenarioSummary: sc, Title: titleIn(s.Lang, sc.Current.Title, sc.ID)})
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
	if deleted := r.URL.Query().Get("deleted"); deleted != "" {
		data.Notice = i18n.T(s.Lang, "admin.scenario.deleted_last", deleted)
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
	return data, true
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
