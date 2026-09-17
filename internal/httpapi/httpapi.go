// Package httpapi is the HTTP surface of theserver (D-020): the router, the
// health endpoint, the mount point of the device link, and API v1 under
// /api/v1. cmd/theserver only wires it to the store, the certificate and the
// lifecycle.
package httpapi

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/cyb3rgun/theserver/internal/content"
	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/internal/version"
)

// Prefix is where API v1 lives.
const Prefix = "/api/v1"

// OpenAPI is the description of API v1, served at /api/v1/openapi.yaml. It is
// a copy of docs/openapi.yaml, and a test keeps the two equal.
//
//go:embed openapi.yaml
var OpenAPI []byte

// A Pinger tells whether the database answers. *store.Store is one.
type Pinger interface {
	Ping(ctx context.Context) error
}

// DeviceLink is what the router and the API need from the device link.
// *link.Server is one.
type DeviceLink interface {
	http.Handler
	Online() []link.DeviceStatus
	Disconnect(deviceID string, why link.DisconnectReason) bool
	AnnouncePending(ctx context.Context, deviceID, sessionID string) ([]link.Announcement, error)
}

var _ DeviceLink = (*link.Server)(nil)

// Options wire the router.
type Options struct {
	// Store backs the API. Without it only /healthz is served.
	Store *store.Store
	// Health is asked by /healthz; it defaults to Store.
	Health Pinger
	// Link serves /link/v1 and tells the API who is online. It may be nil.
	Link DeviceLink
	// Settings is the configuration of the running server, read and changed
	// through /api/v1/settings. It may be nil.
	Settings Settings
	// Content keeps the scenario packages (D-036). Without it the scenario
	// routes that touch packages answer 500.
	Content *content.Store
	Logger  *slog.Logger
}

// Server is the router of theserver.
type Server struct {
	opts Options
	log  *slog.Logger
	mux  *http.ServeMux
	api  *http.ServeMux
}

// New returns the router with /healthz, /link/v1 and, with a store, API v1.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Health == nil && opts.Store != nil {
		opts.Health = opts.Store
	}
	s := &Server{
		opts: opts,
		log:  opts.Logger.With("component", "api"),
		mux:  http.NewServeMux(),
		api:  http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /healthz", s.healthz)
	if opts.Link != nil {
		s.mux.Handle("GET "+link.Path, opts.Link)
	}
	if opts.Store != nil {
		s.routeAPI()
		s.mux.Handle(Prefix+"/", s.api)
	}
	return s
}

// ServeHTTP serves every route.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// API is the handler of /api/v1 alone. The admin pages call it in process,
// with a context from AsAdmin.
func (s *Server) API() http.Handler {
	return s.api
}

// Mount adds a handler to the router, as the admin pages are added.
func (s *Server) Mount(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// A Route is one endpoint of API v1.
type Route struct {
	Method string
	Path   string // below Prefix, with {name} wildcards
	Auth   bool
	// Device lets an approved device in with its token besides an admin
	// (D-038).
	Device bool
}

// routes lists API v1. The router, the OpenAPI coverage test and the admin
// pages all read it.
func (s *Server) routes() []struct {
	Route
	handler http.HandlerFunc
} {
	type entry = struct {
		Route
		handler http.HandlerFunc
	}
	return []entry{
		{Route{http.MethodGet, "/devices", true, false}, s.listDevices},
		{Route{http.MethodGet, "/devices/{id}", true, false}, s.getDevice},
		{Route{http.MethodPost, "/devices/{id}/approve", true, false}, s.approveDevice},
		{Route{http.MethodPost, "/devices/{id}/block", true, false}, s.blockDevice},
		{Route{http.MethodPost, "/devices/{id}/reset", true, false}, s.resetDevice},
		{Route{http.MethodPost, "/devices/{id}/token", true, false}, s.newDeviceToken},
		{Route{http.MethodPost, "/devices/{id}/min_age", true, false}, s.setMinAge},
		{Route{http.MethodGet, "/devices/{id}/scenarios", true, false}, s.deviceScenarios},
		{Route{http.MethodGet, "/sessions", true, false}, s.listSessions},
		{Route{http.MethodPost, "/sessions", true, false}, s.createSession},
		{Route{http.MethodPost, "/sessions/{id}/start", true, false}, s.startSession},
		{Route{http.MethodPost, "/sessions/{id}/stop", true, false}, s.stopSession},
		{Route{http.MethodPost, "/sessions/{id}/devices", true, false}, s.addSessionDevice},
		{Route{http.MethodPost, "/sessions/{id}/scenario", true, false}, s.assignScenario},
		{Route{http.MethodGet, "/drafts", true, false}, s.listDrafts},
		{Route{http.MethodPost, "/drafts", true, false}, s.createDraft},
		{Route{http.MethodGet, "/drafts/{id}", true, false}, s.getDraft},
		{Route{http.MethodPatch, "/drafts/{id}", true, false}, s.patchDraft},
		{Route{http.MethodDelete, "/drafts/{id}", true, false}, s.deleteDraft},
		{Route{http.MethodPost, "/drafts/{id}/media", true, false}, s.uploadDraftMedia},
		{Route{http.MethodGet, "/drafts/{id}/media/{name}", true, false}, s.getDraftMedia},
		{Route{http.MethodPatch, "/drafts/{id}/media/{name}", true, false}, s.measureDraftMedia},
		{Route{http.MethodDelete, "/drafts/{id}/media/{name}", true, false}, s.deleteDraftMedia},
		{Route{http.MethodPost, "/drafts/{id}/trace", true, false}, s.traceDraft},
		{Route{http.MethodPost, "/drafts/{id}/validate", true, false}, s.validateDraft},
		{Route{http.MethodPost, "/drafts/{id}/publish", true, false}, s.publishDraft},
		{Route{http.MethodGet, "/scenarios", true, false}, s.listScenarios},
		{Route{http.MethodPost, "/scenarios", true, false}, s.uploadScenario},
		{Route{http.MethodGet, "/scenarios/{id}", true, false}, s.getScenario},
		{Route{http.MethodPost, "/scenarios/{id}/{version}/publish", true, false}, s.publishScenario},
		{Route{http.MethodDelete, "/scenarios/{id}/{version}", true, false}, s.deleteScenario},
		{Route{http.MethodGet, "/scenarios/{id}/{version}/package.zip", true, true}, s.downloadPackage},
		{Route{http.MethodGet, "/scenarios/{id}/{version}/cover.png", true, false}, s.scenarioCover},
		{Route{http.MethodGet, "/rankings", true, false}, s.rankings},
		{Route{http.MethodGet, "/events", true, false}, s.events},
		{Route{http.MethodGet, "/online", true, false}, s.online},
		{Route{http.MethodGet, "/settings", true, false}, s.settingsList},
		{Route{http.MethodPut, "/settings", true, false}, s.settingsChange},
		{Route{http.MethodPost, "/settings/reset", true, false}, s.settingsReset},
		{Route{http.MethodGet, "/openapi.yaml", false, false}, s.openAPI},
	}
}

// Routes lists the endpoints of API v1.
func Routes() []Route {
	s := &Server{}
	var routes []Route
	for _, r := range s.routes() {
		routes = append(routes, r.Route)
	}
	return routes
}

func (s *Server) routeAPI() {
	allowed := map[string][]string{}
	for _, r := range s.routes() {
		var h http.Handler = r.handler
		switch {
		case r.Device:
			h = s.authorizeDownload(h)
		case r.Auth:
			h = s.authorize(h)
		}
		s.api.Handle(r.Method+" "+Prefix+r.Path, h)
		allowed[r.Path] = append(allowed[r.Path], r.Method)
	}
	// Any other method on a known path is a JSON 405, anything else a JSON
	// 404, so every answer of the API has the same error shape.
	for path, methods := range allowed {
		if slices.Contains(methods, http.MethodGet) {
			methods = append(methods, http.MethodHead)
		}
		allow := strings.Join(methods, ", ")
		s.api.HandleFunc(Prefix+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			writeError(w, http.StatusMethodNotAllowed, codeMethodNotAllowed, r.Method+" is not allowed here")
		})
	}
	s.api.HandleFunc(Prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, codeNotFound, "no such endpoint: "+r.URL.Path)
	})
}

// Health is the body of GET /healthz.
type Health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	DB      string `json:"db"`
}

// healthz answers 200 while the database answers, and 503 when it does not,
// so that a monitor sees the difference.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	body := Health{Status: "ok", Version: version.Version, DB: "ok"}
	status := http.StatusOK
	if s.opts.Health == nil {
		body = Health{Status: "error", Version: version.Version, DB: "error"}
		status = http.StatusServiceUnavailable
	} else if err := s.opts.Health.Ping(r.Context()); err != nil {
		s.log.Error("health check could not reach the database", "error", err)
		body = Health{Status: "error", Version: version.Version, DB: "error"}
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, body)
}

func (s *Server) openAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(OpenAPI)
}

// Error codes of API v1.
const (
	codeBadRequest       = "bad_request"
	codeUnauthorized     = "unauthorized"
	codeForbidden        = "forbidden"
	codeNotFound         = "not_found"
	codeMethodNotAllowed = "method_not_allowed"
	codeConflict         = "conflict"
	codeBadTransition    = "bad_transition"
	codeInternal         = "internal"
	codeInvalidSettings  = "invalid_settings"
	codeInvalidPackage   = "invalid_package"
	codeTooLarge         = "too_large"
	codePublished        = "published"
	codeHasProblems      = "has_problems"
	codeVersionTaken     = "version_taken"
	codeNotPublished     = "not_published"
	codeAgeRating        = "age_rating"
	codeBadMedia         = "bad_media"
	codeBadManifest      = "bad_manifest"
)

// ErrorBody is the shape of every error answer of API v1.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail names what went wrong. Fields lists the refused settings of a
// settings change, Problems the problems of a package that was not stored.
type ErrorDetail struct {
	Code     string             `json:"code"`
	Message  string             `json:"message"`
	Fields   []FieldError       `json:"fields,omitempty"`
	Problems []scenario.Problem `json:"problems,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// fail answers an error from the store with the matching status.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrDeviceNotFound), errors.Is(err, store.ErrSessionNotFound),
		errors.Is(err, store.ErrScenarioNotFound), errors.Is(err, store.ErrDraftNotFound),
		errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, content.ErrBadMedia):
		writeError(w, http.StatusUnsupportedMediaType, codeBadMedia, err.Error())
	case errors.Is(err, content.ErrBadManifest):
		writeError(w, http.StatusBadRequest, codeBadManifest, err.Error())
	case errors.Is(err, content.ErrBadName):
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
	case errors.Is(err, store.ErrBadTransition):
		writeError(w, http.StatusConflict, codeBadTransition, err.Error())
	case errors.Is(err, store.ErrPublished):
		writeError(w, http.StatusConflict, codePublished, err.Error())
	case errors.Is(err, store.ErrHasProblems):
		writeError(w, http.StatusConflict, codeHasProblems, err.Error())
	case errors.Is(err, store.ErrVersionTaken):
		writeError(w, http.StatusConflict, codeVersionTaken, err.Error())
	case errors.Is(err, store.ErrNotPublished):
		writeError(w, http.StatusConflict, codeNotPublished, err.Error())
	case errors.Is(err, store.ErrAgeRating):
		writeError(w, http.StatusConflict, codeAgeRating, err.Error())
	case errors.Is(err, errBadRequest):
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
	case errors.Is(err, errConflict):
		writeError(w, http.StatusConflict, codeConflict, err.Error())
	default:
		s.log.Error("api request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "the server could not complete the request")
	}
}

var (
	errBadRequest = errors.New("bad request")
	errConflict   = errors.New("conflict")
)
