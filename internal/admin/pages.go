package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
)

// errSessionEnded is an API answer of 401 or 403: the admin token behind the
// cookie is gone or revoked.
var errSessionEnded = errors.New("admin session ended")

// apiError is any other error answer of the API.
type apiError struct {
	Status int
	httpapi.ErrorDetail
}

func (e *apiError) Error() string {
	return e.Message
}

// recorder captures an in process API answer.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// call runs one API v1 request in process as the admin of the session and
// decodes the answer into out.
func (a *Admin) call(r *http.Request, s session, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(httpapi.AsAdmin(r.Context(), s.TokenID), method, httpapi.Prefix+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = r.RemoteAddr

	rec := &recorder{header: http.Header{}}
	a.opts.API.ServeHTTP(rec, req)

	switch {
	case rec.status == http.StatusUnauthorized, rec.status == http.StatusForbidden:
		return errSessionEnded
	case rec.status >= 400:
		var eb httpapi.ErrorBody
		if err := json.Unmarshal(rec.body.Bytes(), &eb); err != nil {
			return fmt.Errorf("API answered %d: %s", rec.status, rec.body.String())
		}
		return &apiError{Status: rec.status, ErrorDetail: eb.Error}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(rec.body.Bytes(), out); err != nil {
		return fmt.Errorf("API answer of %s %s: %w", method, path, err)
	}
	return nil
}

// failed handles an error of call. It reports whether the handler has to
// stop; otherwise the message is shown on the page, in the language of the
// request.
func (a *Admin) failed(w http.ResponseWriter, r *http.Request, err error, message *string) bool {
	var ae *apiError
	lang := a.lang(r)
	switch {
	case err == nil:
		return false
	case errors.Is(err, errSessionEnded):
		a.sessionEnded(w, r)
		return true
	case errors.As(err, &ae):
		key := "admin.api_error." + ae.Code
		if !i18n.Has(lang, key) {
			key = "admin.api_error.unknown"
		}
		a.log.Debug("api refused an admin request", "path", r.URL.Path, "status", ae.Status, "code", ae.Code, "message", ae.Message)
		*message = i18n.T(lang, key)
		return false
	default:
		a.log.Error("admin request failed", "path", r.URL.Path, "error", err)
		*message = i18n.T(lang, "admin.error_generic")
		return false
	}
}

func now() string {
	return time.Now().Format("15:04:05")
}

// Devices

type devicesData struct {
	layout
	Devices []httpapi.Device
	Updated string
}

// tokenData fills the token dialog. Table is the devices table after the
// change, swapped in beside the dialog so the page shows the device offline
// at once.
type tokenData struct {
	Token httpapi.NewToken
	Error string
	Table devicesData
}

func (a *Admin) loadDevices(w http.ResponseWriter, r *http.Request, s session, data *devicesData) bool {
	var list struct {
		Devices []httpapi.Device `json:"devices"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/devices", nil, &list), &data.Error) {
		return false
	}
	data.Devices = list.Devices
	data.Updated = now()
	return true
}

func (a *Admin) devicesPage(w http.ResponseWriter, r *http.Request, s session) {
	data := devicesData{layout: a.layout("admin.devices.title", "devices", s)}
	if a.loadDevices(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusOK, "devices", "layout", data)
	}
}

func (a *Admin) devicesTable(w http.ResponseWriter, r *http.Request, s session) {
	data := devicesData{layout: a.layout("admin.devices.title", "devices", s)}
	if a.loadDevices(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusOK, "devices", "devices-table", data)
	}
}

// deviceNotices names the catalogue text shown after a device action.
var deviceNotices = map[string]string{
	"approve": "admin.devices.approved",
	"block":   "admin.devices.blocked",
	"reset":   "admin.devices.reset_done",
}

func (a *Admin) deviceAction(w http.ResponseWriter, r *http.Request, s session) {
	id, action := r.PathValue("id"), r.PathValue("action")
	path := "/devices/" + url.PathEscape(id) + "/" + action

	if action == "token" {
		var data tokenData
		err := a.call(r, s, http.MethodPost, path, nil, &data.Token)
		if a.failed(w, r, err, &data.Error) {
			return
		}
		data.Table = devicesData{layout: a.layout("admin.devices.title", "devices", s)}
		if data.Error == "" {
			data.Table.Notice = i18n.T(s.Lang, "admin.devices.token_done", id)
		}
		if a.loadDevices(w, r, s, &data.Table) {
			a.render(w, s.Lang, http.StatusOK, "devices", "token-dialog", data)
		}
		return
	}

	data := devicesData{layout: a.layout("admin.devices.title", "devices", s)}
	notice, known := deviceNotices[action]
	if !known {
		data.Error = i18n.T(s.Lang, "admin.unknown_action", action)
	} else {
		var device httpapi.Device
		if a.failed(w, r, a.call(r, s, http.MethodPost, path, nil, &device), &data.Error) {
			return
		}
		if data.Error == "" {
			if action == "reset" {
				data.Notice = i18n.T(s.Lang, notice, id, device.SeqEpoch)
			} else {
				data.Notice = i18n.T(s.Lang, notice, id)
			}
		}
	}
	if a.loadDevices(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusOK, "devices", "devices-table", data)
	}
}

// Sessions

type sessionsData struct {
	layout
	Sessions []httpapi.Session
	Devices  []httpapi.Device
}

func (a *Admin) loadSessions(w http.ResponseWriter, r *http.Request, s session, data *sessionsData) bool {
	var sessions struct {
		Sessions []httpapi.Session `json:"sessions"`
	}
	var devices struct {
		Devices []httpapi.Device `json:"devices"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/sessions", nil, &sessions), &data.Error) {
		return false
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/devices", nil, &devices), &data.Error) {
		return false
	}
	data.Sessions, data.Devices = sessions.Sessions, devices.Devices
	return true
}

func (a *Admin) sessionsPage(w http.ResponseWriter, r *http.Request, s session) {
	data := sessionsData{layout: a.layout("admin.sessions.title", "sessions", s)}
	if a.loadSessions(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusOK, "sessions", "layout", data)
	}
}

func (a *Admin) createSession(w http.ResponseWriter, r *http.Request, s session) {
	data := sessionsData{layout: a.layout("admin.sessions.title", "sessions", s)}
	body := httpapi.NewSession{
		ID:       strings.TrimSpace(r.PostFormValue("id")),
		Scenario: strings.TrimSpace(r.PostFormValue("scenario")),
		Room:     strings.TrimSpace(r.PostFormValue("room")),
	}
	var created httpapi.Session
	if a.failed(w, r, a.call(r, s, http.MethodPost, "/sessions", body, &created), &data.Error) {
		return
	}
	if data.Error == "" {
		data.Notice = i18n.T(s.Lang, "admin.sessions.created", created.ID)
	}
	if a.loadSessions(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusOK, "sessions", "sessions-area", data)
	}
}

func (a *Admin) sessionAction(w http.ResponseWriter, r *http.Request, s session) {
	data := sessionsData{layout: a.layout("admin.sessions.title", "sessions", s)}
	id, action := r.PathValue("id"), r.PathValue("action")
	path := "/sessions/" + url.PathEscape(id) + "/" + action

	var err error
	var result httpapi.Session
	switch action {
	case "start":
		err = a.call(r, s, http.MethodPost, path, nil, &result)
		data.Notice = i18n.T(s.Lang, "admin.sessions.running", id)
	case "stop":
		err = a.call(r, s, http.MethodPost, path, nil, &result)
		data.Notice = i18n.T(s.Lang, "admin.sessions.stopped", id)
	case "devices":
		device := strings.TrimSpace(r.PostFormValue("device_id"))
		err = a.call(r, s, http.MethodPost, path, httpapi.SessionDevice{DeviceID: device}, &result)
		data.Notice = i18n.T(s.Lang, "admin.sessions.device_added", device, id)
	default:
		data.Error = i18n.T(s.Lang, "admin.unknown_action", action)
	}
	if a.failed(w, r, err, &data.Error) {
		return
	}
	if data.Error != "" {
		data.Notice = ""
	}
	if a.loadSessions(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusOK, "sessions", "sessions-area", data)
	}
}

// Ranking

type rankingData struct {
	layout
	Sessions []httpapi.Session
	Selected string
	Ranking  httpapi.Ranking
	Hits     int
	Misses   int
	Points   int64
	Computed string
}

func (a *Admin) loadRanking(w http.ResponseWriter, r *http.Request, s session, data *rankingData) bool {
	data.Selected = r.URL.Query().Get("session")
	path := "/rankings"
	if data.Selected != "" {
		path += "?session=" + url.QueryEscape(data.Selected)
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, path, nil, &data.Ranking), &data.Error) {
		return false
	}
	for _, e := range data.Ranking.Entries {
		data.Hits += e.Hits
		data.Misses += e.Misses
		data.Points += e.Points
	}
	if data.Ranking.ComputedAt != 0 {
		data.Computed = time.UnixMilli(data.Ranking.ComputedAt).Format("15:04:05")
	}
	return true
}

func (a *Admin) rankingPage(w http.ResponseWriter, r *http.Request, s session) {
	data := rankingData{layout: a.layout("admin.ranking.title", "ranking", s)}
	var sessions struct {
		Sessions []httpapi.Session `json:"sessions"`
	}
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/sessions", nil, &sessions), &data.Error) {
		return
	}
	data.Sessions = sessions.Sessions
	if a.loadRanking(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusOK, "ranking", "layout", data)
	}
}

func (a *Admin) rankingTable(w http.ResponseWriter, r *http.Request, s session) {
	data := rankingData{layout: a.layout("admin.ranking.title", "ranking", s)}
	if a.loadRanking(w, r, s, &data) {
		a.render(w, s.Lang, http.StatusOK, "ranking", "ranking-table", data)
	}
}

// Settings

type settingsData struct {
	layout
	Settings []httpapi.SettingView
}

func (a *Admin) settingsPage(w http.ResponseWriter, r *http.Request, s session) {
	data := settingsData{layout: a.layout("admin.settings.title", "settings", s)}
	var list httpapi.SettingsList
	if a.failed(w, r, a.call(r, s, http.MethodGet, "/settings?lang="+url.QueryEscape(s.Lang), nil, &list), &data.Error) {
		return
	}
	data.Settings = list.Settings
	a.render(w, s.Lang, http.StatusOK, "settings", "layout", data)
}
