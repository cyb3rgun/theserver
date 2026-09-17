package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/cyb3rgun/theserver/internal/config"
	"github.com/cyb3rgun/theserver/internal/settings"
)

// Settings is what the API needs from the configuration of the running
// server. *config.Runtime is one.
type Settings interface {
	Config() config.Config
	Sources() config.Sources
	File() string
	FileExists() bool
	RestartPending() []string
	Pending(key string) (any, bool)
	Change(values map[string]any) ([]config.Change, error)
	ResetKeys(keys []string) ([]config.Change, error)
}

var _ Settings = (*config.Runtime)(nil)

// SettingView is one setting as GET /api/v1/settings shows it: the registry
// description, the texts in the requested language, the value in effect and
// where it came from.
type SettingView struct {
	Key         string   `json:"key"`
	Section     string   `json:"section"`
	Kind        string   `json:"kind"`
	Value       any      `json:"value"`
	Default     any      `json:"default"`
	Source      string   `json:"source"`
	Pending     any      `json:"pending,omitempty"` // the value after the next restart
	Unit        string   `json:"unit"`
	Min         *int64   `json:"min,omitempty"`
	Max         *int64   `json:"max,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Empty       bool     `json:"empty"`
	Restart     bool     `json:"restart"`
	Env         string   `json:"env"`
	Flag        string   `json:"flag,omitempty"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Why         string   `json:"why"`
}

// SettingsList is the body of GET /api/v1/settings.
type SettingsList struct {
	Language string `json:"language"`
	// File is the configuration file changes are written to. FileExists is
	// false until the first change creates it, for a server started
	// without --config.
	File           string        `json:"file"`
	FileExists     bool          `json:"file_exists"`
	RestartPending []string      `json:"restart_pending"`
	Settings       []SettingView `json:"settings"`
}

// SettingChange is one changed setting in the answer to a change.
type SettingChange struct {
	Key     string `json:"key"`
	Old     any    `json:"old"`
	New     any    `json:"new"`
	Restart bool   `json:"restart"`
}

// SettingsChanged answers PUT /api/v1/settings and POST /settings/reset.
type SettingsChanged struct {
	Changes []SettingChange `json:"changes"`
	// Applied lists the settings that took effect at once.
	Applied []string `json:"applied"`
	// RestartPending lists every setting that waits for a restart, from
	// this change or an earlier one.
	RestartPending []string `json:"restart_pending"`
}

// SettingsReset is the body of POST /api/v1/settings/reset.
type SettingsReset struct {
	Keys []string `json:"keys"`
}

// FieldError says why one setting was refused, in a form a page can
// translate: Code with its parameters, and an English Message.
type FieldError struct {
	Key     string   `json:"key"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Min     *int64   `json:"min,omitempty"`
	Max     *int64   `json:"max,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Allowed []string `json:"allowed,omitempty"`
	// Name is the environment variable or flag that sets an overridden
	// setting.
	Name string `json:"name,omitempty"`
}

// Codes of a FieldError besides those of the settings registry.
const (
	FieldOverridden  = "overridden"
	FieldCertKeyPair = "cert_key_pair"
)

func (s *Server) settingsList(w http.ResponseWriter, r *http.Request) {
	if s.opts.Settings == nil {
		s.fail(w, r, errors.New("settings are not wired"))
		return
	}
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = settings.FallbackLanguage
	}
	if !slices.Contains(settings.Languages(), lang) {
		s.fail(w, r, fmt.Errorf("%w: lang must be one of %s", errBadRequest, strings.Join(settings.Languages(), ", ")))
		return
	}

	rt := s.opts.Settings
	cfg, sources := rt.Config(), rt.Sources()
	list := SettingsList{
		Language:       lang,
		File:           rt.File(),
		FileExists:     rt.FileExists(),
		RestartPending: nonNil(rt.RestartPending()),
		Settings:       []SettingView{},
	}
	for _, setting := range settings.All() {
		text := setting.TextIn(lang)
		view := SettingView{
			Key: setting.Key, Section: setting.Section, Kind: setting.Kind.String(),
			Value: cfg.Get(setting.Key), Default: setting.Default, Source: string(sources.Of(setting.Key)),
			Unit: setting.Unit, Min: setting.Min, Max: setting.Max, Enum: setting.Enum,
			Empty: setting.Empty, Restart: setting.Restart, Env: setting.Env(),
			Label: text.Label, Description: text.Description, Why: text.Why,
		}
		if setting.Flag != "" {
			view.Flag = "--" + setting.Flag
		}
		if next, ok := rt.Pending(setting.Key); ok {
			view.Pending = next
		}
		if setting.Sensitive {
			view.Value, view.Default, view.Pending = nil, nil, nil
		}
		list.Settings = append(list.Settings, view)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) settingsChange(w http.ResponseWriter, r *http.Request) {
	if s.opts.Settings == nil {
		s.fail(w, r, errors.New("settings are not wired"))
		return
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.UseNumber()
	var values map[string]any
	if err := dec.Decode(&values); err != nil || dec.More() {
		s.fail(w, r, fmt.Errorf("%w: the body must be one JSON object of settings and values", errBadRequest))
		return
	}
	changes, err := s.opts.Settings.Change(values)
	s.answerChanges(w, r, "set", changes, err)
}

func (s *Server) settingsReset(w http.ResponseWriter, r *http.Request) {
	if s.opts.Settings == nil {
		s.fail(w, r, errors.New("settings are not wired"))
		return
	}
	var body SettingsReset
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	changes, err := s.opts.Settings.ResetKeys(body.Keys)
	s.answerChanges(w, r, "reset", changes, err)
}

// answerChanges logs every change (D-033) and answers with them, or with the
// reasons the change was refused.
func (s *Server) answerChanges(w http.ResponseWriter, r *http.Request, action string, changes []config.Change, err error) {
	if err != nil {
		s.failSettings(w, r, err)
		return
	}
	token, _ := AdminFrom(r.Context())
	out := SettingsChanged{Changes: []SettingChange{}, Applied: []string{}}
	for _, c := range changes {
		old, next := c.Old, c.New
		if setting, ok := settings.Get(c.Key); ok && setting.Sensitive {
			old, next = "(hidden)", "(hidden)"
		}
		takesEffect := "now"
		if c.Restart {
			takesEffect = "after restart"
		} else {
			out.Applied = append(out.Applied, c.Key)
		}
		s.log.Info("setting changed",
			"action", action, "key", c.Key, "old", old, "new", next, "takes_effect", takesEffect,
			"admin_token", token.ID, "admin_name", token.Name)
		out.Changes = append(out.Changes, SettingChange{Key: c.Key, Old: old, New: next, Restart: c.Restart})
	}
	out.RestartPending = nonNil(s.opts.Settings.RestartPending())
	writeJSON(w, http.StatusOK, out)
}

// failSettings answers a refused change: 400 with one field error per
// refused setting, 409 when a configuration file appeared after the start,
// 500 otherwise.
func (s *Server) failSettings(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, config.ErrFileAppeared) {
		writeError(w, http.StatusConflict, codeConflict, err.Error())
		return
	}
	fields := fieldErrors(err)
	if len(fields) == 0 {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusBadRequest, ErrorBody{Error: ErrorDetail{
		Code:    codeInvalidSettings,
		Message: fmt.Sprintf("%d setting(s) refused; nothing was changed", len(fields)),
		Fields:  fields,
	}})
}

// fieldErrors lists the refused settings of err, which may join several.
// It returns nil when err holds anything else, such as a failed write.
func fieldErrors(err error) []FieldError {
	var fields []FieldError
	var walk func(error) bool
	walk = func(err error) bool {
		var (
			ve *settings.ValueError
			oe *config.OverrideError
		)
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range joined.Unwrap() {
				if !walk(e) {
					return false
				}
			}
			return true
		}
		switch {
		case errors.As(err, &ve):
			fields = append(fields, FieldError{
				Key: ve.Key, Code: ve.Code(), Message: ve.Error(),
				Min: ve.Min, Max: ve.Max, Unit: ve.Unit, Allowed: ve.Allowed,
			})
		case errors.As(err, &oe):
			fields = append(fields, FieldError{Key: oe.Key, Code: FieldOverridden, Message: oe.Error(), Name: oe.Name})
		case errors.Is(err, config.ErrCertKeyPair):
			for _, key := range []string{"tls.cert_file", "tls.key_file"} {
				fields = append(fields, FieldError{Key: key, Code: FieldCertKeyPair, Message: err.Error()})
			}
		default:
			return false
		}
		return true
	}
	if !walk(err) {
		return nil
	}
	return fields
}

func nonNil(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}
