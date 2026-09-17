package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/cyb3rgun/theserver/internal/content"
	"github.com/cyb3rgun/theserver/internal/mediakind"
	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/store"
)

// MediaField is the multipart field of POST /drafts/{id}/media that carries
// the file.
const MediaField = "file"

// EditorTiers are the tiers the editor of S01 works in (S01-B08); the admin
// pages offer these and the API takes no other.
func EditorTiers() []string {
	return []string{scenario.TierVideo, scenario.TierInteractive}
}

// Draft is one editor draft as API v1 shows it (D-041). Manifest is the
// manifest as the editor changes it, without the files table, which the
// server writes at publish (D-042).
type Draft struct {
	ID               string             `json:"id"`
	ScenarioID       string             `json:"scenario_id"`
	Tier             string             `json:"tier"`
	Title            map[string]string  `json:"title"`
	Manifest         json.RawMessage    `json:"manifest,omitempty"`
	Media            []DraftMedia       `json:"media"`
	CreatedAt        int64              `json:"created_at"`
	CreatedBy        string             `json:"created_by"`
	UpdatedAt        int64              `json:"updated_at"`
	UpdatedBy        string             `json:"updated_by"`
	PublishedVersion int                `json:"published_version"`
	NextVersion      int                `json:"next_version"`
	Problems         []scenario.Problem `json:"problems"`
}

// DraftMedia is one file in the media directory of a draft. Use is what the
// container and the codec make it good for (D-045); the duration and the
// dimensions are what the browser measured, 0 until it says.
type DraftMedia struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Use        string `json:"use"`
	Container  string `json:"container"`
	Codec      string `json:"codec"`
	Size       int64  `json:"size"`
	DurationMs int    `json:"duration_ms"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	UploadedAt int64  `json:"uploaded_at"`
}

// DraftList is the body of GET /drafts.
type DraftList struct {
	Drafts []Draft `json:"drafts"`
}

// NewDraft is the body of POST /drafts: a fresh draft with an id, a tier and
// a title, or a copy of a published version.
type NewDraft struct {
	ID    string            `json:"id"`
	Tier  string            `json:"tier"`
	Title map[string]string `json:"title"`
	From  *FromVersion      `json:"from"`
}

// FromVersion names the published version a draft copies.
type FromVersion struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// DraftChanged answers a change of a draft with the draft and the problems
// of the fields the change named.
type DraftChanged struct {
	Draft   Draft              `json:"draft"`
	Touched []scenario.Problem `json:"touched"`
}

// DraftProblems is the body of POST /drafts/{id}/validate.
type DraftProblems struct {
	Problems []scenario.Problem `json:"problems"`
}

// Shots is the body of POST /drafts/{id}/trace: what a player fired.
type Shots struct {
	Shots []scenario.Shot `json:"shots"`
}

// TraceOf is the answer: the trace the rule engine of section 7 wrote.
type TraceOf struct {
	Trace scenario.Trace `json:"trace"`
}

// MaxShots is what one trace request may carry.
const MaxShots = 10000

// Measured is the body of PATCH /drafts/{id}/media/{name}: what the browser
// measured on a clip, which the server does not read itself (D-045).
type Measured struct {
	DurationMs int `json:"duration_ms"`
	Width      int `json:"width"`
	Height     int `json:"height"`
}

// draftJSON shapes a draft for the API. withManifest is false in a list.
func (s *Server) draftJSON(r *http.Request, d store.Draft, withManifest bool) Draft {
	out := Draft{
		ID: d.ID, ScenarioID: d.ScenarioID, Media: []DraftMedia{},
		CreatedAt: d.CreatedAt, CreatedBy: d.CreatedBy,
		UpdatedAt: d.UpdatedAt, UpdatedBy: d.UpdatedBy,
		PublishedVersion: d.PublishedVersion, Title: map[string]string{},
		Problems: []scenario.Problem{},
	}
	if m, err := content.DecodeManifest(d.Manifest); err == nil {
		out.Tier = m.Scenario.Tier
		out.Title = map[string]string(m.Scenario.Title)
		if out.Title == nil {
			out.Title = map[string]string{}
		}
	}
	if withManifest {
		out.Manifest = json.RawMessage(d.Manifest)
	}
	for _, name := range d.MediaNames() {
		file := d.Media[name]
		out.Media = append(out.Media, DraftMedia{
			Name: file.Name, Path: content.PackagePath(file.Name),
			Use:       mediakind.UseOf(mediakind.Kind{Container: file.Container, Codec: file.Codec}),
			Container: file.Container, Codec: file.Codec, Size: file.Size,
			DurationMs: file.DurationMs, Width: file.Width, Height: file.Height,
			UploadedAt: file.UploadedAt,
		})
	}
	if s.opts.Content != nil {
		if next, err := s.opts.Content.NextVersion(r.Context(), d.ScenarioID); err == nil {
			out.NextVersion = next
		}
		if problems, err := s.opts.Content.ValidateDraft(r.Context(), d); err == nil && len(problems) > 0 {
			out.Problems = problems
		}
	}
	return out
}

// drafts answers when the content store is not wired.
func (s *Server) content() (*content.Store, error) {
	if s.opts.Content == nil {
		return nil, errors.New("scenario content is not wired")
	}
	return s.opts.Content, nil
}

func (s *Server) listDrafts(w http.ResponseWriter, r *http.Request) {
	list, err := s.opts.Store.ListDrafts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := DraftList{Drafts: []Draft{}}
	for _, d := range list {
		out.Drafts = append(out.Drafts, s.draftJSON(r, d, false))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getDraft(w http.ResponseWriter, r *http.Request) {
	d, err := s.opts.Store.GetDraft(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.draftJSON(r, d, true))
}

// createDraft opens a draft: empty for an id and a tier, or as a copy of a
// published version with its media (D-041).
func (s *Server) createDraft(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var body NewDraft
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	token, _ := AdminFrom(r.Context())
	spec := content.NewDraft{ScenarioID: body.ID, Tier: body.Tier, Title: scenario.Text(body.Title), By: token.Name}
	if body.From != nil {
		sc, err := s.opts.Store.GetScenario(r.Context(), body.From.ID, body.From.Version)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if sc.Status != store.ScenarioPublished {
			s.fail(w, r, fmt.Errorf("%s version %d is a draft; a copy starts from a published version: %w",
				sc.ID, sc.Version, errConflict))
			return
		}
		spec.From, spec.Tier = &sc, sc.Tier
		if spec.ScenarioID == "" {
			spec.ScenarioID = sc.ID
		}
		if len(spec.Title) == 0 {
			spec.Title = sc.Title
		}
	}
	switch {
	case !scenario.ValidID(spec.ScenarioID):
		s.fail(w, r, fmt.Errorf("%w: id must be 1 to 64 lower case letters, digits, hyphens or underscores", errBadRequest))
		return
	case !slices.Contains(EditorTiers(), spec.Tier):
		s.fail(w, r, fmt.Errorf("%w: the editor works in the tiers %s", errBadRequest, strings.Join(EditorTiers(), " and ")))
		return
	}
	d, err := drafts.CreateDraft(r.Context(), spec)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "draft created", "draft", d.ID, "scenario", d.ScenarioID, "tier", spec.Tier, "copied", body.From != nil)
	w.Header().Set("Location", fmt.Sprintf("%s/drafts/%s", Prefix, d.ID))
	writeJSON(w, http.StatusCreated, s.draftJSON(r, d, true))
}

// patchDraft changes the manifest of a draft with a JSON merge patch and
// answers with the draft and the problems of the fields it touched.
func (s *Server) patchDraft(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	patch, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: the change is too large", errBadRequest))
		return
	}
	token, _ := AdminFrom(r.Context())
	d, err := drafts.PatchDraft(r.Context(), r.PathValue("id"), patch, token.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := s.draftJSON(r, d, true)
	writeJSON(w, http.StatusOK, DraftChanged{Draft: out, Touched: touchedProblems(patch, out.Problems)})
}

// touchedProblems picks the problems that lie in the parts of the manifest a
// change named, so the editor can show them where the change was made.
func touchedProblems(patch []byte, problems []scenario.Problem) []scenario.Problem {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(patch, &keys); err != nil {
		return []scenario.Problem{}
	}
	out := []scenario.Problem{}
	for _, p := range problems {
		for key := range keys {
			if p.Field == key || strings.HasPrefix(p.Field, key+".") || strings.HasPrefix(p.Field, key+"[") {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

func (s *Server) deleteDraft(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	if err := drafts.DeleteDraft(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "draft deleted", "draft", id)
	w.WriteHeader(http.StatusNoContent)
}

// uploadDraftMedia takes one media file into the draft. The container and
// the codec decide whether it is taken at all (D-045).
func (s *Server) uploadDraftMedia(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit := s.uploadLimit()
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartOverhead)
	parts, err := r.MultipartReader()
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: the body must be multipart/form-data with the field %s", errBadRequest, MediaField))
		return
	}
	var part *multipart.Part
	name := ""
	for {
		part, err = parts.NextPart()
		if err != nil {
			break
		}
		if part.FormName() == "name" {
			value, _ := io.ReadAll(io.LimitReader(part, 256))
			name = string(value)
			continue
		}
		if part.FormName() == MediaField {
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
		s.fail(w, r, fmt.Errorf("%w: the body has no field %s", errBadRequest, MediaField))
		return
	}
	if name == "" {
		name = part.FileName()
	}
	name = content.SafeMediaName(name)

	token, _ := AdminFrom(r.Context())
	d, file, err := drafts.PutDraftMedia(r.Context(), r.PathValue("id"), name, part, limit, token.Name)
	switch {
	case errors.Is(err, content.ErrTooLarge), errors.As(err, &tooBig):
		s.refuseSize(w, r, limit)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.audit(r, "draft media uploaded", "draft", d.ID, "file", file.Name, "size", file.Size,
		"container", file.Container, "codec", file.Codec)
	writeJSON(w, http.StatusCreated, DraftChanged{Draft: s.draftJSON(r, d, true), Touched: []scenario.Problem{}})
}

// getDraftMedia serves one media file of a draft, with Range, so the editor
// can play it in a video element.
func (s *Server) getDraftMedia(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id, name := r.PathValue("id"), r.PathValue("name")
	d, err := s.opts.Store.GetDraft(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, ok := d.Media[name]; !ok {
		s.fail(w, r, fmt.Errorf("the draft %s has no media file %q: %w", id, name, os.ErrNotExist))
		return
	}
	path, err := drafts.DraftMediaPath(id, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Type", mediaType(d.Media[name].Container))
	http.ServeContent(w, r, name, info.ModTime(), file)
}

// mediaType is the type of a container the browser is told (D-045).
func mediaType(container string) string {
	switch container {
	case mediakind.MP4:
		return "video/mp4"
	case mediakind.WebM:
		return "video/webm"
	case mediakind.Ogg:
		return "audio/ogg"
	case mediakind.PNG:
		return "image/png"
	}
	return "application/octet-stream"
}

// measureDraftMedia keeps what the browser measured on a clip, the second
// call of an upload (D-045).
func (s *Server) measureDraftMedia(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var body Measured
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	if body.DurationMs < 0 || body.Width < 0 || body.Height < 0 {
		s.fail(w, r, fmt.Errorf("%w: a duration and a size are not negative", errBadRequest))
		return
	}
	token, _ := AdminFrom(r.Context())
	measured := content.MeasuredMedia{DurationMs: body.DurationMs, Width: body.Width, Height: body.Height}
	d, _, err := drafts.MeasureDraftMedia(r.Context(), r.PathValue("id"), r.PathValue("name"), measured, token.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, DraftChanged{Draft: s.draftJSON(r, d, true), Touched: []scenario.Problem{}})
}

func (s *Server) deleteDraftMedia(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	token, _ := AdminFrom(r.Context())
	id, name := r.PathValue("id"), r.PathValue("name")
	d, err := drafts.DeleteDraftMedia(r.Context(), id, name, token.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "draft media deleted", "draft", id, "file", name)
	writeJSON(w, http.StatusOK, DraftChanged{Draft: s.draftJSON(r, d, true), Touched: []scenario.Problem{}})
}

// traceDraft plays the shots against the draft with the rule engine of
// docs/scenario.md section 7 and answers with its trace (D-044). The editor
// compares it with the trace its own engine wrote for the same shots.
func (s *Server) traceDraft(w http.ResponseWriter, r *http.Request) {
	d, err := s.opts.Store.GetDraft(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var body Shots
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(body.Shots) > MaxShots {
		s.fail(w, r, fmt.Errorf("%w: a run carries at most %d shots", errBadRequest, MaxShots))
		return
	}
	m, err := content.DecodeManifest(d.Manifest)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, TraceOf{Trace: scenario.Run(m, body.Shots)})
}

// validateDraft says what a publish would find.
func (s *Server) validateDraft(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.opts.Store.GetDraft(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	problems, err := drafts.ValidateDraft(r.Context(), d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if problems == nil {
		problems = []scenario.Problem{}
	}
	writeJSON(w, http.StatusOK, DraftProblems{Problems: problems})
}

// publishDraft writes the package of the draft and publishes it through the
// chain of B07 (D-042); a draft with problems publishes nothing.
func (s *Server) publishDraft(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.content()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	token, _ := AdminFrom(r.Context())
	sc, problems, err := drafts.PublishDraft(r.Context(), id, s.uploadLimit(), token.Name)
	switch {
	case errors.Is(err, content.ErrDraftProblems):
		s.audit(r, "draft publish refused", "draft", id, "problems", len(problems))
		writeJSON(w, http.StatusUnprocessableEntity, ErrorBody{Error: ErrorDetail{
			Code:     codeInvalidPackage,
			Message:  fmt.Sprintf("the draft was not published: %d problem(s)", len(problems)),
			Problems: problems,
		}})
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.audit(r, "draft published", "draft", id, "scenario", sc.ID, "version", sc.Version, "manifest_hash", sc.ManifestHash)
	w.Header().Set("Location", fmt.Sprintf("%s/scenarios/%s", Prefix, sc.ID))
	writeJSON(w, http.StatusOK, versionJSON(sc))
}
