package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"slices"
	"strconv"

	"github.com/cyb3rgun/theserver/internal/content"
	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/store"
)

// UploadField is the multipart field of POST /scenarios that carries the
// package.
const UploadField = "package"

// DefaultMaxUploadMB is the upload limit when the server has no settings.
const DefaultMaxUploadMB = 2048

// multipartOverhead is what a multipart body may hold besides the package.
const multipartOverhead = 64 << 10

// ScenarioVersion is one version of a scenario as API v1 shows it (D-036).
type ScenarioVersion struct {
	ID           string             `json:"id"`
	Version      int                `json:"version"`
	Tier         string             `json:"tier"`
	Title        map[string]string  `json:"title"`
	AgeRating    string             `json:"age_rating"`
	ManifestHash string             `json:"manifest_hash"`
	Size         int64              `json:"size"`
	Status       string             `json:"status"`
	UploadedAt   int64              `json:"uploaded_at"`
	UploadedBy   string             `json:"uploaded_by"`
	PublishedAt  int64              `json:"published_at"`
	Problems     []scenario.Problem `json:"problems"`
}

// ScenarioSummary is one scenario in GET /scenarios. Current is the latest
// published version, or the newest draft while none is published.
type ScenarioSummary struct {
	ID      string            `json:"id"`
	Current ScenarioVersion   `json:"current"`
	Latest  *ScenarioVersion  `json:"latest"`
	Drafts  []ScenarioVersion `json:"drafts"`
}

// ScenarioList is the body of GET /scenarios.
type ScenarioList struct {
	Scenarios []ScenarioSummary `json:"scenarios"`
}

// ScenarioDetail is the body of GET /scenarios/{id}: every version, newest
// first, and every device that holds or held one.
type ScenarioDetail struct {
	ID       string            `json:"id"`
	Latest   int               `json:"latest"`
	Versions []ScenarioVersion `json:"versions"`
	Holdings []Holding         `json:"holdings"`
}

// Holding is a scenario version a device holds or held.
type Holding struct {
	DeviceID    string `json:"device_id"`
	ScenarioID  string `json:"scenario_id"`
	Version     int    `json:"version"`
	InstalledAt int64  `json:"installed_at"`
	// Current is set while the device holds the version.
	Current bool `json:"current"`
	// Latest is the latest published version of the scenario, 0 when none
	// is published.
	Latest int `json:"latest"`
}

// Upload is the answer to a stored upload. The draft may have problems;
// it cannot be published until an upload without them replaces it.
type Upload struct {
	Replaced bool               `json:"replaced"`
	Scenario ScenarioVersion    `json:"scenario"`
	Problems []scenario.Problem `json:"problems"`
}

// ScenarioAssignment is the body of POST /sessions/{id}/scenario.
type ScenarioAssignment struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// SessionAssignment answers an assignment with the session and what each
// device of it was told.
type SessionAssignment struct {
	Session       Session        `json:"session"`
	Announcements []Announcement `json:"announcements"`
}

// The states of an Announcement.
const (
	AnnouncedTaken   = "announced"
	AnnouncedWaiting = "waiting"
	AnnouncedFailed  = "failed"
)

// Announcement is one content_available to a device of a session: taken by
// the device, waiting until the device connects, or failed with Error.
type Announcement struct {
	DeviceID   string `json:"device_id"`
	SessionID  string `json:"session_id"`
	ScenarioID string `json:"scenario_id"`
	Version    int    `json:"version"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
}

// DeviceScenarios is the body of GET /devices/{id}/scenarios.
type DeviceScenarios struct {
	DeviceID    string       `json:"device_id"`
	MinAge      int          `json:"min_age"`
	Holdings    []Holding    `json:"holdings"`
	Assignments []Assignment `json:"assignments"`
}

// Assignment is the scenario version a created or running session of the
// device plays, and whether the device holds it.
type Assignment struct {
	SessionID  string `json:"session_id"`
	State      string `json:"state"`
	ScenarioID string `json:"scenario_id"`
	Version    int    `json:"version"`
	Installed  bool   `json:"installed"`
}

// MinAge is the body of POST /devices/{id}/min_age.
type MinAge struct {
	MinAge *int `json:"min_age"`
}

func versionJSON(sc store.Scenario) ScenarioVersion {
	problems := sc.Problems
	if problems == nil {
		problems = []scenario.Problem{}
	}
	title := map[string]string(sc.Title)
	if title == nil {
		title = map[string]string{}
	}
	return ScenarioVersion{
		ID: sc.ID, Version: sc.Version, Tier: sc.Tier, Title: title, AgeRating: sc.AgeRating,
		ManifestHash: sc.ManifestHash, Size: sc.Size, Status: sc.Status,
		UploadedAt: sc.UploadedAt, UploadedBy: sc.UploadedBy, PublishedAt: sc.PublishedAt,
		Problems: problems,
	}
}

// latestPublished maps every scenario to its latest published version.
func latestPublished(list []store.Scenario) map[string]int {
	latest := map[string]int{}
	for _, sc := range list {
		if sc.Status == store.ScenarioPublished && sc.Version > latest[sc.ID] {
			latest[sc.ID] = sc.Version
		}
	}
	return latest
}

func holdingsJSON(list []store.Holding, latest map[string]int) []Holding {
	out := make([]Holding, 0, len(list))
	for _, h := range list {
		out = append(out, Holding{
			DeviceID: h.DeviceID, ScenarioID: h.ScenarioID, Version: h.Version,
			InstalledAt: h.InstalledAt, Current: h.Current, Latest: latest[h.ScenarioID],
		})
	}
	return out
}

func (s *Server) listScenarios(w http.ResponseWriter, r *http.Request) {
	list, err := s.opts.Store.ListScenarios(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := ScenarioList{Scenarios: []ScenarioSummary{}}
	for _, sc := range list {
		if n := len(out.Scenarios); n == 0 || out.Scenarios[n-1].ID != sc.ID {
			out.Scenarios = append(out.Scenarios, ScenarioSummary{ID: sc.ID, Current: versionJSON(sc), Drafts: []ScenarioVersion{}})
		}
		summary := &out.Scenarios[len(out.Scenarios)-1]
		version := versionJSON(sc)
		switch {
		case sc.Status == store.ScenarioDraft:
			summary.Drafts = append(summary.Drafts, version)
		case summary.Latest == nil:
			summary.Latest = &version
			summary.Current = version
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getScenario(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	list, err := s.opts.Store.ListScenarios(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	detail := ScenarioDetail{ID: id, Latest: latestPublished(list)[id], Versions: []ScenarioVersion{}}
	for _, sc := range list {
		if sc.ID == id {
			detail.Versions = append(detail.Versions, versionJSON(sc))
		}
	}
	if len(detail.Versions) == 0 {
		s.fail(w, r, fmt.Errorf("%s: %w", id, store.ErrScenarioNotFound))
		return
	}
	holdings, err := s.opts.Store.ScenarioHoldings(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	detail.Holdings = holdingsJSON(holdings, latestPublished(list))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, detail)
}

// uploadLimit is the upload limit in bytes, read at every upload.
func (s *Server) uploadLimit() int64 {
	mb := DefaultMaxUploadMB
	if s.opts.Settings != nil {
		mb = s.opts.Settings.Config().Content.MaxUploadMB
	}
	return int64(mb) << 20
}

// uploadScenario keeps a package as a draft of the id and version its
// manifest names: 201 for a new draft, 200 for a replaced one, both with the
// problems of the draft; 422 when the package cannot be stored at all.
func (s *Server) uploadScenario(w http.ResponseWriter, r *http.Request) {
	if s.opts.Content == nil {
		s.fail(w, r, errors.New("scenario content is not wired"))
		return
	}
	limit := s.uploadLimit()
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartOverhead)
	parts, err := r.MultipartReader()
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: the body must be multipart/form-data with the field %s", errBadRequest, UploadField))
		return
	}
	var part *multipart.Part
	for {
		part, err = parts.NextPart()
		if err != nil || part.FormName() == UploadField {
			break
		}
		io.Copy(io.Discard, part)
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		s.refuseSize(w, r, limit)
		return
	case err != nil:
		s.fail(w, r, fmt.Errorf("%w: the body has no field %s", errBadRequest, UploadField))
		return
	}

	token, _ := AdminFrom(r.Context())
	result, err := s.opts.Content.Put(r.Context(), part, limit, token.Name)
	switch {
	case errors.Is(err, content.ErrTooLarge), errors.As(err, &tooBig):
		s.refuseSize(w, r, limit)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	attrs := []any{"scenario", result.Scenario.ID, "version", result.Scenario.Version, "problems", len(result.Problems),
		"size", result.Scenario.Size, "manifest_hash", result.Scenario.ManifestHash}
	if !result.Stored {
		s.audit(r, "scenario upload refused", attrs...)
		writeJSON(w, http.StatusUnprocessableEntity, ErrorBody{Error: ErrorDetail{
			Code:     codeInvalidPackage,
			Message:  fmt.Sprintf("the package was not stored: %d problem(s)", len(result.Problems)),
			Problems: result.Problems,
		}})
		return
	}
	s.audit(r, "scenario uploaded", append(attrs, "status", result.Scenario.Status, "replaced", result.Replaced)...)
	status := http.StatusCreated
	if result.Replaced {
		status = http.StatusOK
	}
	problems := result.Problems
	if problems == nil {
		problems = []scenario.Problem{}
	}
	w.Header().Set("Location", fmt.Sprintf("%s/scenarios/%s", Prefix, result.Scenario.ID))
	writeJSON(w, status, Upload{Replaced: result.Replaced, Scenario: versionJSON(result.Scenario), Problems: problems})
}

func (s *Server) refuseSize(w http.ResponseWriter, r *http.Request, limit int64) {
	s.audit(r, "scenario upload refused", "reason", "too large", "limit_mb", limit>>20)
	writeError(w, http.StatusRequestEntityTooLarge, codeTooLarge,
		fmt.Sprintf("the package is larger than the upload limit of %d MB (content.max_upload_mb)", limit>>20))
}

// versionOf reads the version of a path; anything but a whole number from 1
// names no version.
func versionOf(r *http.Request) (string, int, error) {
	id := r.PathValue("id")
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		return id, 0, fmt.Errorf("%s version %q: %w", id, r.PathValue("version"), store.ErrScenarioNotFound)
	}
	return id, version, nil
}

func (s *Server) publishScenario(w http.ResponseWriter, r *http.Request) {
	id, version, err := versionOf(r)
	if err == nil && s.opts.Content == nil {
		err = errors.New("scenario content is not wired")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sc, err := s.opts.Content.Publish(r.Context(), id, version)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "scenario published", "scenario", id, "version", version, "manifest_hash", sc.ManifestHash)
	writeJSON(w, http.StatusOK, versionJSON(sc))
}

func (s *Server) deleteScenario(w http.ResponseWriter, r *http.Request) {
	id, version, err := versionOf(r)
	if err == nil && s.opts.Content == nil {
		err = errors.New("scenario content is not wired")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.opts.Content.Delete(r.Context(), id, version); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "scenario draft deleted", "scenario", id, "version", version)
	w.WriteHeader(http.StatusNoContent)
}

// downloadPackage serves a package as it was uploaded, with Range, to a
// device for a published version and to an admin for any (D-038).
func (s *Server) downloadPackage(w http.ResponseWriter, r *http.Request) {
	path, sc, ok := s.packagePath(w, r)
	if !ok {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.fail(w, r, fmt.Errorf("the package of %s version %d: %w", sc.ID, sc.Version, err))
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	by := "admin"
	attrs := []any{"scenario", sc.ID, "version", sc.Version, "size", info.Size()}
	if device, isDevice := DeviceFrom(r.Context()); isDevice {
		by = "device"
		attrs = append(attrs, "device", device.ID)
	} else if token, isAdmin := AdminFrom(r.Context()); isAdmin {
		attrs = append(attrs, "admin_token", token.ID, "admin_name", token.Name)
	}
	if rng := r.Header.Get("Range"); rng != "" {
		attrs = append(attrs, "range", rng)
	}
	s.log.Info("package download", append(attrs, "by", by)...)

	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%d.zip"`, sc.ID, sc.Version))
	h.Set("X-Manifest-SHA256", sc.ManifestHash)
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// scenarioCover serves the cover of a version for the catalogue.
func (s *Server) scenarioCover(w http.ResponseWriter, r *http.Request) {
	path, _, ok := s.packagePath(w, r)
	if !ok {
		return
	}
	cover, size, err := scenario.OpenZipFile(path, scenario.CoverName)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, codeNotFound, "this version has no cover")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer cover.Close()
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		io.Copy(w, cover)
	}
}

// packagePath finds the package of the version in the path. A device only
// sees published versions.
func (s *Server) packagePath(w http.ResponseWriter, r *http.Request) (string, store.Scenario, bool) {
	id, version, err := versionOf(r)
	if err == nil && s.opts.Content == nil {
		err = errors.New("scenario content is not wired")
	}
	if err != nil {
		s.fail(w, r, err)
		return "", store.Scenario{}, false
	}
	sc, err := s.opts.Store.GetScenario(r.Context(), id, version)
	if err != nil {
		s.fail(w, r, err)
		return "", store.Scenario{}, false
	}
	if _, isDevice := DeviceFrom(r.Context()); isDevice && sc.Status != store.ScenarioPublished {
		s.fail(w, r, fmt.Errorf("%s version %d: %w", id, version, store.ErrScenarioNotFound))
		return "", store.Scenario{}, false
	}
	path, err := s.opts.Content.Path(id, version)
	if err != nil {
		s.fail(w, r, err)
		return "", store.Scenario{}, false
	}
	return path, sc, true
}

// assignScenario sets the scenario version of a session and announces it to
// every device of the session that does not hold it (D-038, D-039).
func (s *Server) assignScenario(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	var body ScenarioAssignment
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	if body.ID == "" || body.Version < 1 {
		s.fail(w, r, fmt.Errorf("%w: id and a version from 1 are required", errBadRequest))
		return
	}
	ctx := r.Context()
	if err := s.opts.Store.AssignScenario(ctx, sessionID, body.ID, body.Version); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "scenario assigned", "session", sessionID, "scenario", body.ID, "version", body.Version)
	announcements := s.announce(r, "", sessionID)
	session, err := s.opts.Store.GetSession(ctx, sessionID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SessionAssignment{Session: sessionJSON(session), Announcements: announcements})
}

// announce tells the devices what they should fetch, narrowed to a device
// and a session.
func (s *Server) announce(r *http.Request, deviceID, sessionID string) []Announcement {
	out := []Announcement{}
	if s.opts.Link == nil {
		return out
	}
	results, err := s.opts.Link.AnnouncePending(r.Context(), deviceID, sessionID)
	if err != nil {
		s.log.Error("could not announce content", "session", sessionID, "device", deviceID, "error", err)
		return out
	}
	for _, a := range results {
		item := Announcement{DeviceID: a.DeviceID, SessionID: a.SessionID, ScenarioID: a.ScenarioID, Version: a.Version, State: AnnouncedTaken}
		switch {
		case errors.Is(a.Err, link.ErrDeviceOffline):
			item.State = AnnouncedWaiting
		case a.Err != nil:
			item.State, item.Error = AnnouncedFailed, a.Err.Error()
		}
		out = append(out, item)
	}
	return out
}

func (s *Server) deviceScenarios(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	device, err := s.opts.Store.GetDevice(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := s.opts.Store.ListScenarios(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	holdings, err := s.opts.Store.DeviceScenarios(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sessions, err := s.opts.Store.ListSessions(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := DeviceScenarios{DeviceID: id, MinAge: device.MinAge, Holdings: holdingsJSON(holdings, latestPublished(list)), Assignments: []Assignment{}}
	for _, session := range sessions {
		if session.ScenarioVersion == 0 || session.State == store.SessionStopped || !slices.Contains(session.Devices, id) {
			continue
		}
		held := false
		for _, h := range holdings {
			held = held || h.Current && h.ScenarioID == session.Scenario && h.Version == session.ScenarioVersion
		}
		out.Assignments = append(out.Assignments, Assignment{
			SessionID: session.ID, State: session.State, ScenarioID: session.Scenario,
			Version: session.ScenarioVersion, Installed: held,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setMinAge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body MinAge
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	if body.MinAge == nil || !slices.Contains(store.MinAges(), *body.MinAge) {
		s.fail(w, r, fmt.Errorf("%w: min_age must be one of %v", errBadRequest, store.MinAges()))
		return
	}
	if err := s.opts.Store.SetMinAge(r.Context(), id, *body.MinAge); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "device age set", "device", id, "min_age", *body.MinAge)
	s.writeDevice(w, r, id, http.StatusOK)
}
