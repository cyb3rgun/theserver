package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/settings"
)

// The settings page (D-034) is a plain form rendered from the registry: each
// field shows label, control, unit, current value, default, source, a short
// description on hover and the why to expand. Saving sends only the changed
// fields to PUT /api/v1/settings, all or nothing; a refused save shows the
// reason next to each field, in the language of the page.

type settingsData struct {
	layout
	File          string
	Sections      []settingsSection
	RestartLabels []string
	Script        string // integrity of static/settings.js
}

type settingsSection struct {
	Name   string
	Title  string
	Fields []settingField
}

type settingField struct {
	httpapi.SettingView
	ID          string
	Control     string // text, number, switch or select
	Input       string // the value in the control
	Initial     string // the configured value, for the unsaved count
	CurrentText string
	DefaultText string
	PendingText string
	RangeText   string
	MinText     string
	MaxText     string
	Locked      bool
	LockedNote  string
	Error       string
}

// loadSettings reads the settings in the language of the session.
func (a *Admin) loadSettings(w http.ResponseWriter, r *http.Request, s session, data *settingsData) (httpapi.SettingsList, bool) {
	var list httpapi.SettingsList
	err := a.call(r, s, http.MethodGet, "/settings?lang="+url.QueryEscape(s.Lang), nil, &list)
	if a.failed(w, r, err, &data.Error) {
		return list, false
	}
	return list, true
}

func (a *Admin) settingsPage(w http.ResponseWriter, r *http.Request, s session) {
	data := a.newSettingsData(s)
	list, ok := a.loadSettings(w, r, s, &data)
	if !ok {
		return
	}
	q := r.URL.Query()
	switch {
	case q.Has("saved"):
		data.Notice = i18n.T(s.Lang, "admin.settings.saved", atoi(q.Get("saved")), atoi(q.Get("now")), atoi(q.Get("later")))
	case q.Has("unchanged"):
		data.Notice = i18n.T(s.Lang, "admin.settings.nothing")
	case q.Has("reset"):
		if setting, ok := settings.Get(q.Get("reset")); ok {
			data.Notice = i18n.T(s.Lang, "admin.settings.reset_done", setting.TextIn(s.Lang).Label)
		}
	}
	a.fillSettings(&data, s.Lang, list, nil, nil)
	a.render(w, s.Lang, http.StatusOK, "settings", "layout", data)
}

func (a *Admin) newSettingsData(s session) settingsData {
	return settingsData{layout: a.layout("admin.settings.title", "settings", s), Script: a.scripts["settings.js"]}
}

// saveSettings sends the fields that differ from the configured values.
func (a *Admin) saveSettings(w http.ResponseWriter, r *http.Request, s session) {
	data := a.newSettingsData(s)
	list, ok := a.loadSettings(w, r, s, &data)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		data.Error = i18n.T(s.Lang, "admin.api_error.bad_request")
		a.fillSettings(&data, s.Lang, list, nil, nil)
		a.render(w, s.Lang, http.StatusBadRequest, "settings", "layout", data)
		return
	}

	submitted := map[string]string{}
	changes := map[string]any{}
	for _, view := range list.Settings {
		values, sent := r.PostForm[view.Key]
		if !sent || len(values) == 0 || locked(view) {
			continue
		}
		value := values[len(values)-1]
		submitted[view.Key] = value
		if value != configured(view) {
			changes[view.Key] = value
		}
	}
	if len(changes) == 0 {
		http.Redirect(w, r, "/admin/settings?unchanged=1", http.StatusSeeOther)
		return
	}

	var result httpapi.SettingsChanged
	err := a.call(r, s, http.MethodPut, "/settings", changes, &result)
	var ae *apiError
	switch {
	case err == nil:
		later := 0
		for _, c := range result.Changes {
			if c.Restart {
				later++
			}
		}
		target := fmt.Sprintf("/admin/settings?saved=%d&now=%d&later=%d", len(result.Changes), len(result.Applied), later)
		http.Redirect(w, r, target, http.StatusSeeOther)
	case errors.As(err, &ae) && len(ae.Fields) > 0:
		fieldErrors := map[string]string{}
		for _, f := range ae.Fields {
			fieldErrors[f.Key] = fieldMessage(s.Lang, f)
		}
		data.Error = i18n.T(s.Lang, "admin.settings.refused")
		a.fillSettings(&data, s.Lang, list, submitted, fieldErrors)
		a.render(w, s.Lang, http.StatusBadRequest, "settings", "layout", data)
	default:
		if a.failed(w, r, err, &data.Error) {
			return
		}
		a.fillSettings(&data, s.Lang, list, submitted, nil)
		a.render(w, s.Lang, http.StatusConflict, "settings", "layout", data)
	}
}

// resetSetting sets one setting back to its default.
func (a *Admin) resetSetting(w http.ResponseWriter, r *http.Request, s session) {
	key := r.PostFormValue("reset")
	data := a.newSettingsData(s)
	err := a.call(r, s, http.MethodPost, "/settings/reset", httpapi.SettingsReset{Keys: []string{key}}, nil)
	if err == nil {
		http.Redirect(w, r, "/admin/settings?reset="+url.QueryEscape(key), http.StatusSeeOther)
		return
	}
	var ae *apiError
	fieldErrors := map[string]string{}
	if errors.As(err, &ae) && len(ae.Fields) > 0 {
		for _, f := range ae.Fields {
			fieldErrors[f.Key] = fieldMessage(s.Lang, f)
		}
		data.Error = i18n.T(s.Lang, "admin.settings.refused")
	} else if a.failed(w, r, err, &data.Error) {
		return
	}
	list, ok := a.loadSettings(w, r, s, &data)
	if !ok {
		return
	}
	a.fillSettings(&data, s.Lang, list, nil, fieldErrors)
	a.render(w, s.Lang, http.StatusBadRequest, "settings", "layout", data)
}

// fillSettings turns the API list into the sections of the page. submitted
// holds the values of a refused save, which the controls show again.
func (a *Admin) fillSettings(data *settingsData, lang string, list httpapi.SettingsList, submitted, fieldErrors map[string]string) {
	data.File = list.File
	if data.File == "" && data.Error == "" {
		data.Error = i18n.T(lang, "admin.settings.no_file")
	}
	byKey := map[string]httpapi.SettingView{}
	for _, view := range list.Settings {
		byKey[view.Key] = view
	}
	for _, key := range list.RestartPending {
		if view, ok := byKey[key]; ok {
			data.RestartLabels = append(data.RestartLabels, view.Label)
		}
	}

	for _, view := range list.Settings {
		if len(data.Sections) == 0 || data.Sections[len(data.Sections)-1].Name != view.Section {
			data.Sections = append(data.Sections, settingsSection{
				Name:  view.Section,
				Title: i18n.T(lang, "admin.settings.section."+view.Section),
			})
		}
		field := settingField{
			SettingView: view,
			ID:          "s-" + strings.ReplaceAll(view.Key, ".", "-"),
			Initial:     configured(view),
			CurrentText: shown(lang, view.Value, view.Unit),
			DefaultText: shown(lang, view.Default, view.Unit),
			Locked:      locked(view),
			Error:       fieldErrors[view.Key],
		}
		field.Input = field.Initial
		if value, ok := submitted[view.Key]; ok {
			field.Input = value
		}
		if view.Pending != nil {
			field.PendingText = shown(lang, view.Pending, view.Unit)
		}
		switch {
		case view.Kind == settings.Enum.String():
			field.Control = "select"
		case view.Kind == settings.Bool.String():
			field.Control = "switch"
		case view.Kind == settings.Int.String() || view.Kind == settings.Duration.String():
			field.Control = "number"
		default:
			field.Control = "text"
		}
		if view.Min != nil && view.Max != nil {
			field.MinText, field.MaxText = strconv.FormatInt(*view.Min, 10), strconv.FormatInt(*view.Max, 10)
			field.RangeText = rangeText(lang, *view.Min, *view.Max, view.Unit)
		}
		switch view.Source {
		case "env":
			field.LockedNote = i18n.T(lang, "admin.settings.locked_env", view.Env)
		case "flag":
			field.LockedNote = i18n.T(lang, "admin.settings.locked_flag", view.Flag)
		}
		section := &data.Sections[len(data.Sections)-1]
		section.Fields = append(section.Fields, field)
	}
}

// locked reports whether an environment variable or a flag sets view, so
// the page cannot change it.
func locked(view httpapi.SettingView) bool {
	return view.Source == "env" || view.Source == "flag"
}

// configured is the value the configuration file holds for view: the one
// waiting for a restart, else the one in effect.
func configured(view httpapi.SettingView) string {
	if view.Pending != nil {
		return plain(view.Pending)
	}
	return plain(view.Value)
}

// plain writes a value from the API as a form sends it back.
func plain(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

// shown writes a value for a person: with its unit, and empty text named.
func shown(lang string, value any, unit string) string {
	text := plain(value)
	switch {
	case text == "":
		return i18n.T(lang, "admin.settings.empty")
	case unit != "":
		return text + " " + unit
	}
	return text
}

func rangeText(lang string, min, max int64, unit string) string {
	suffix := ""
	if unit != "" {
		suffix = " " + unit
	}
	return i18n.T(lang, "admin.settings.range_value", strconv.FormatInt(min, 10), strconv.FormatInt(max, 10), suffix)
}

// fieldMessage translates the reason the API gave for one setting.
func fieldMessage(lang string, f httpapi.FieldError) string {
	switch f.Code {
	case "out_of_range":
		if f.Min != nil && f.Max != nil {
			return i18n.T(lang, "admin.settings.error.out_of_range", rangeText(lang, *f.Min, *f.Max, f.Unit))
		}
	case "not_allowed":
		return i18n.T(lang, "admin.settings.error.not_allowed", strings.Join(f.Allowed, ", "))
	case "bad_duration":
		return i18n.T(lang, "admin.settings.error.bad_duration", f.Unit, f.Unit)
	case "overridden":
		return i18n.T(lang, "admin.settings.error.overridden", f.Name)
	case "bad_address", "bad_number", "bad_bool", "empty", "bad_type", "unknown_setting", "cert_key_pair":
		return i18n.T(lang, "admin.settings.error."+f.Code)
	}
	return i18n.T(lang, "admin.settings.error.other")
}

func atoi(text string) int {
	n, _ := strconv.Atoi(text)
	return n
}

// setLanguage keeps the chosen language in its cookie for as long as a login
// lasts and goes back to the page it came from (D-031).
func (a *Admin) setLanguage(w http.ResponseWriter, r *http.Request) {
	lang := r.PostFormValue("lang")
	if i18n.Supported(lang) {
		http.SetCookie(w, &http.Cookie{
			Name:     i18n.CookieName,
			Value:    lang,
			Path:     cookiePath,
			MaxAge:   int(a.sessionLifetime().Seconds()),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
		})
	}
	http.Redirect(w, r, backTo(r.PostFormValue("back")), http.StatusSeeOther)
}

// backTo accepts only a path of the admin pages, so the switch cannot send a
// browser elsewhere.
func backTo(back string) string {
	u, err := url.Parse(back)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil ||
		!strings.HasPrefix(u.Path, "/admin/") || strings.Contains(u.Path, "..") || strings.ContainsAny(back, "\\\r\n") {
		return "/admin/devices"
	}
	return u.Path
}

func expiredLanguageCookie() *http.Cookie {
	return &http.Cookie{
		Name:     i18n.CookieName,
		Value:    "",
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}
