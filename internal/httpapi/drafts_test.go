package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/content"
	"github.com/cyb3rgun/theserver/internal/mediakind/mediakindtest"
	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/scenario/scenariotest"
)

// putMedia sends data as the file field of a multipart body.
func (h *apiHarness) putMedia(draft, name string, data []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile(MediaField, name)
	if err != nil {
		h.t.Fatal(err)
	}
	part.Write(data)
	form.Close()
	req := httptest.NewRequest(http.MethodPost, Prefix+"/drafts/"+draft+"/media", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.token)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

// draftCodes lists the problem codes of a draft, in order.
func draftCodes(problems []scenario.Problem) []string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Code)
	}
	return out
}

// videoManifest is a complete VIDEO manifest as the editor would build it,
// with one zone and one appearance, as a JSON merge patch.
const videoManifest = `{
  "scenario": {"id": "range-test", "tier": "video",
    "title": {"en": "Range test", "de": "Standtest"},
    "description": {"en": "One plate.", "de": "Eine Scheibe."},
    "age_rating": "12", "duration_s": 12, "author": "tests", "licence": "private"},
  "display": {"canvas": {"w": 1080, "h": 1920}, "orientation": "portrait", "fit": "cover"},
  "rules": {"points_per_hit_default": 50, "miss_penalty": 10, "timeout_counts_as_hit": true,
    "timeout_penalty": 100, "lives": 3, "score_cap": 0},
  "zone": [{"id": "z-plate", "name": {"en": "Plate", "de": "Scheibe"}, "shape": "circle",
    "points": [[540, 960]], "radius": 120, "points_value": 100, "zone_class": "object"}],
  "appearance": [{"id": "a-plate", "t_start_ms": 1000, "t_end_ms": 5000, "zones": ["z-plate"],
    "required_hits": 1, "on_timeout": "penalty"}],
  "reaction": {"immediate": {"flash": true, "hitmarker": "ring"}},
  "media": {"main": "media/clip.mp4"}
}`

// A draft is opened, filled, given media, validated and published, and the
// package the server writes for it is the package a person would have
// zipped by hand (D-041, D-042).
func TestDraftFromNothingToPublished(t *testing.T) {
	h := newAPI(t)

	created := decode[Draft](t, h.call(http.MethodPost, Prefix+"/drafts",
		`{"id": "range-test", "tier": "video", "title": {"en": "Range test", "de": "Standtest"}}`), http.StatusCreated)
	if created.ID == "" || !strings.HasPrefix(created.ID, "d-") || created.ScenarioID != "range-test" ||
		created.Tier != "video" || created.NextVersion != 1 || created.PublishedVersion != 0 {
		t.Fatalf("the new draft reads %+v", created)
	}
	if codes := draftCodes(created.Problems); !slices.Contains(codes, scenario.CodeMissingSection) ||
		!slices.Contains(codes, scenario.CodeTierMediaMismatch) {
		t.Errorf("a fresh draft has the problems %v", codes)
	}
	draft := created.ID

	// The tiers the editor does not work in, and an id that cannot name a
	// scenario, are refused.
	expectError(t, h.call(http.MethodPost, Prefix+"/drafts", `{"id": "x", "tier": "layered"}`),
		http.StatusBadRequest, codeBadRequest)
	expectError(t, h.call(http.MethodPost, Prefix+"/drafts", `{"id": "Range Test", "tier": "video"}`),
		http.StatusBadRequest, codeBadRequest)

	// One change fills the manifest; the answer names the problems of the
	// parts it touched.
	changed := decode[DraftChanged](t, h.call(http.MethodPatch, Prefix+"/drafts/"+draft, videoManifest), http.StatusOK)
	if len(changed.Draft.Problems) != 1 || changed.Draft.Problems[0].Code != scenario.CodeMissingMedia {
		t.Fatalf("after the change the draft has %v", changed.Draft.Problems)
	}
	if len(changed.Touched) != 1 || changed.Touched[0].Field != "media.main" {
		t.Errorf("the change touched %v", changed.Touched)
	}

	// A field the model does not have is refused and nothing is stored.
	expectError(t, h.call(http.MethodPatch, Prefix+"/drafts/"+draft, `{"scenario": {"colour": "red"}}`),
		http.StatusBadRequest, codeBadManifest)
	expectError(t, h.call(http.MethodPatch, Prefix+"/drafts/"+draft, `{"scenario": {"duration_s": "twelve"}}`),
		http.StatusBadRequest, codeBadManifest)
	if now := decode[Draft](t, h.call(http.MethodGet, Prefix+"/drafts/"+draft, ""), http.StatusOK); len(now.Problems) != 1 {
		t.Errorf("a refused change left the draft with %v", now.Problems)
	}

	// The media the manifest names is uploaded; the container and the codec
	// are read from the file (D-045).
	withMedia := decode[DraftChanged](t, h.putMedia(draft, "clip.mp4", mediakindtest.Clip()), http.StatusCreated)
	if len(withMedia.Draft.Media) != 1 {
		t.Fatalf("the draft holds %v", withMedia.Draft.Media)
	}
	file := withMedia.Draft.Media[0]
	if file.Name != "clip.mp4" || file.Path != "media/clip.mp4" || file.Container != "mp4" ||
		file.Codec != "h264" || file.Use != "clip" || file.Size == 0 {
		t.Errorf("the media file reads %+v", file)
	}
	if len(withMedia.Draft.Problems) != 0 {
		t.Errorf("with its media the draft still has %v", withMedia.Draft.Problems)
	}

	// What the browser measured comes in a second call.
	measured := decode[DraftChanged](t, h.call(http.MethodPatch, Prefix+"/drafts/"+draft+"/media/clip.mp4",
		`{"duration_ms": 12000, "width": 1080, "height": 1920}`), http.StatusOK)
	if got := measured.Draft.Media[0]; got.DurationMs != 12000 || got.Width != 1080 || got.Height != 1920 {
		t.Errorf("the measured file reads %+v", got)
	}

	// A file that is not a media file at all is refused, and nothing is kept.
	expectError(t, h.putMedia(draft, "notes.txt", []byte(strings.Repeat("plain text\n", 8))),
		http.StatusUnsupportedMediaType, codeBadMedia)
	expectError(t, h.putMedia(draft, "song.mp4", mediakindtest.MP4("hvc1")),
		http.StatusUnsupportedMediaType, codeBadMedia)
	if now := decode[Draft](t, h.call(http.MethodGet, Prefix+"/drafts/"+draft, ""), http.StatusOK); len(now.Media) != 1 {
		t.Errorf("a refused upload left %v", now.Media)
	}

	// Validation says what a publish would find.
	if problems := decode[DraftProblems](t, h.call(http.MethodPost, Prefix+"/drafts/"+draft+"/validate", ""),
		http.StatusOK); len(problems.Problems) != 0 {
		t.Fatalf("the draft does not validate: %v", problems.Problems)
	}

	published := decode[ScenarioVersion](t, h.call(http.MethodPost, Prefix+"/drafts/"+draft+"/publish", ""), http.StatusOK)
	if published.ID != "range-test" || published.Version != 1 || published.Status != "published" ||
		len(published.Problems) != 0 || published.ManifestHash == "" {
		t.Fatalf("the published version reads %+v", published)
	}

	// The package on disk is a package like any other: it loads, it
	// validates, and its files carry the hashes the manifest lists.
	path, err := h.content.Path("range-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := scenario.LoadZip(path)
	if err != nil {
		t.Fatalf("the package the server wrote: %v", err)
	}
	if problems := pkg.Validate(); len(problems) != 0 {
		t.Errorf("the written package has problems: %v", problems)
	}
	if got := scenario.Hash(pkg.Manifest); got != published.ManifestHash {
		t.Errorf("the manifest hash of the package is %s, the index says %s", got, published.ManifestHash)
	}
	if pkg.Manifest.Media.Main != "media/clip.mp4" || len(pkg.Files) != 1 || pkg.Files["media/clip.mp4"] == "" {
		t.Errorf("the package holds %v", pkg.Files)
	}
	if pkg.Manifest.Scenario.Version != 1 || pkg.Manifest.Scenario.AgeRating != "12" {
		t.Errorf("the manifest reads %+v", pkg.Manifest.Scenario)
	}

	// The draft stays and now works towards the next version.
	after := decode[Draft](t, h.call(http.MethodGet, Prefix+"/drafts/"+draft, ""), http.StatusOK)
	if after.PublishedVersion != 1 || after.NextVersion != 2 {
		t.Errorf("after the publish the draft reads %+v", after)
	}
	list := decode[DraftList](t, h.call(http.MethodGet, Prefix+"/drafts", ""), http.StatusOK)
	if len(list.Drafts) != 1 || list.Drafts[0].ID != draft || len(list.Drafts[0].Manifest) != 0 {
		t.Errorf("the list holds %+v", list.Drafts)
	}

	// The catalogue shows the published version.
	catalogue := decode[ScenarioList](t, h.call(http.MethodGet, Prefix+"/scenarios", ""), http.StatusOK)
	if len(catalogue.Scenarios) != 1 || catalogue.Scenarios[0].Current.Version != 1 {
		t.Errorf("the catalogue holds %+v", catalogue.Scenarios)
	}

	// Deleting the draft takes its directory with it.
	dir := h.content.DraftMediaDir(draft)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the media directory: %v", err)
	}
	if rec := h.call(http.MethodDelete, Prefix+"/drafts/"+draft, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete answered %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Dir(dir)); !os.IsNotExist(err) {
		t.Errorf("the draft directory is still there: %v", err)
	}
	expectError(t, h.call(http.MethodGet, Prefix+"/drafts/"+draft, ""), http.StatusNotFound, codeNotFound)
}

// A draft with problems publishes nothing at all.
func TestPublishRefusesADraftWithProblems(t *testing.T) {
	h := newAPI(t)
	draft := decode[Draft](t, h.call(http.MethodPost, Prefix+"/drafts",
		`{"id": "half-done", "tier": "video", "title": {"en": "Half", "de": "Halb"}}`), http.StatusCreated).ID
	h.call(http.MethodPatch, Prefix+"/drafts/"+draft, videoManifest)

	body := expectError(t, h.call(http.MethodPost, Prefix+"/drafts/"+draft+"/publish", ""),
		http.StatusUnprocessableEntity, codeInvalidPackage)
	if len(body.Error.Problems) != 1 || body.Error.Problems[0].Code != scenario.CodeMissingMedia {
		t.Errorf("the refusal names %v", body.Error.Problems)
	}
	catalogue := decode[ScenarioList](t, h.call(http.MethodGet, Prefix+"/scenarios", ""), http.StatusOK)
	if len(catalogue.Scenarios) != 0 {
		t.Errorf("a refused publish left %+v", catalogue.Scenarios)
	}
	if list, err := os.ReadDir(h.content.Dir()); err != nil || len(list) != 1 || list[0].Name() != "drafts" {
		t.Errorf("the content directory holds %v (%v)", list, err)
	}
}

// A draft copied from a published version publishes a version that is the
// hand made package again, with the next version number (D-042).
func TestDraftCopiedFromAPublishedVersion(t *testing.T) {
	h := newAPI(t)
	fixture := scenariotest.Zip(t, scenariotest.Video)
	decode[Upload](t, h.upload(fixture), http.StatusCreated)
	decode[ScenarioVersion](t, h.call(http.MethodPost, Prefix+"/scenarios/night-range/1/publish", ""), http.StatusOK)

	created := decode[Draft](t, h.call(http.MethodPost, Prefix+"/drafts",
		`{"from": {"id": "night-range", "version": 1}}`), http.StatusCreated)
	if created.ScenarioID != "night-range" || created.Tier != "video" || created.NextVersion != 2 ||
		len(created.Media) != 3 || len(created.Problems) != 0 {
		t.Fatalf("the copy reads %+v", created)
	}
	names := []string{}
	for _, m := range created.Media {
		names = append(names, m.Path)
	}
	if !slices.Equal(names, []string{"cover.png", "media/impact.ogg", "media/main.mp4"}) {
		t.Errorf("the copy holds %v", names)
	}

	published := decode[ScenarioVersion](t, h.call(http.MethodPost, Prefix+"/drafts/"+created.ID+"/publish", ""), http.StatusOK)
	if published.Version != 2 {
		t.Fatalf("the copy published version %d", published.Version)
	}

	// The package of the editor and the package of the hand is the same
	// scenario, down to the files table; only the version moved on.
	hand := loadFixture(t, scenariotest.Video)
	path, err := h.content.Path("night-range", 2)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := scenario.LoadZip(path)
	if err != nil {
		t.Fatal(err)
	}
	if problems := editor.Validate(); len(problems) != 0 {
		t.Errorf("the package of the editor has problems: %v", problems)
	}
	want := hand.Manifest
	want.Scenario.Version = 2
	if !reflect.DeepEqual(editor.Manifest, want) {
		t.Errorf("the manifest of the editor differs:\n%+v\n%+v", editor.Manifest, want)
	}
	if !reflect.DeepEqual(editor.Files, hand.Files) {
		t.Errorf("the files differ:\n%v\n%v", editor.Files, hand.Files)
	}
	if scenario.Hash(editor.Manifest) != scenario.Hash(want) {
		t.Error("the hashes differ")
	}

	// A copy of a draft version is refused; a copy starts from something
	// that was published.
	decode[Upload](t, h.upload(withManifest(t, scenariotest.Video, "version     = 1", "version     = 3")), http.StatusCreated)
	expectError(t, h.call(http.MethodPost, Prefix+"/drafts", `{"from": {"id": "night-range", "version": 3}}`),
		http.StatusConflict, codeConflict)
	expectError(t, h.call(http.MethodPost, Prefix+"/drafts", `{"from": {"id": "night-range", "version": 9}}`),
		http.StatusNotFound, codeNotFound)
}

// loadFixture reads a fixture package for a comparison.
func loadFixture(t *testing.T, name string) *scenario.Package {
	t.Helper()
	pkg, err := scenario.Load(scenariotest.Dir(name))
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// The names a browser sends are made safe, and a name that cannot be saved
// is refused.
func TestDraftMediaNames(t *testing.T) {
	h := newAPI(t)
	draft := decode[Draft](t, h.call(http.MethodPost, Prefix+"/drafts",
		`{"id": "names", "tier": "interactive", "title": {"en": "Names", "de": "Namen"}}`), http.StatusCreated).ID

	changed := decode[DraftChanged](t, h.putMedia(draft, "C:\\fakepath\\Mein Video (1).MP4", mediakindtest.Clip()), http.StatusCreated)
	if len(changed.Draft.Media) != 1 || changed.Draft.Media[0].Name != "mein-video--1-.mp4" {
		t.Fatalf("the name became %+v", changed.Draft.Media)
	}
	// The same name again replaces the file.
	again := decode[DraftChanged](t, h.putMedia(draft, "mein-video--1-.mp4", mediakindtest.MP4("av01")), http.StatusCreated)
	if len(again.Draft.Media) != 1 || again.Draft.Media[0].Codec != "av1" {
		t.Errorf("the second upload gave %+v", again.Draft.Media)
	}
	// A name with nothing usable in it is refused.
	expectError(t, h.putMedia(draft, "???", mediakindtest.Clip()), http.StatusBadRequest, codeBadRequest)

	// Deleting a file leaves the manifest as it is, so validation names it.
	h.call(http.MethodPatch, Prefix+"/drafts/"+draft, `{"media": {"state": {"walk": "media/mein-video--1-.mp4"}}}`)
	after := decode[DraftChanged](t, h.call(http.MethodDelete, Prefix+"/drafts/"+draft+"/media/mein-video--1-.mp4", ""), http.StatusOK)
	if len(after.Draft.Media) != 0 {
		t.Errorf("the file is still listed: %v", after.Draft.Media)
	}
	if codes := draftCodes(after.Draft.Problems); !slices.Contains(codes, scenario.CodeMissingMedia) {
		t.Errorf("after the delete the draft has %v", codes)
	}
	expectError(t, h.call(http.MethodDelete, Prefix+"/drafts/"+draft+"/media/gone.mp4", ""), http.StatusNotFound, codeNotFound)
}

// The manifest of a draft is kept as the model writes it, so nothing the
// model does not know survives a change.
func TestDraftManifestIsNormalized(t *testing.T) {
	h := newAPI(t)
	draft := decode[Draft](t, h.call(http.MethodPost, Prefix+"/drafts",
		`{"id": "normal", "tier": "video", "title": {"en": "Normal", "de": "Normal"}}`), http.StatusCreated)
	var first map[string]json.RawMessage
	if err := json.Unmarshal(draft.Manifest, &first); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"scenario", "display", "rules", "zone", "appearance", "reaction", "media"} {
		if _, ok := first[key]; !ok {
			t.Errorf("the manifest of a new draft has no %s", key)
		}
	}
	changed := decode[DraftChanged](t, h.call(http.MethodPatch, Prefix+"/drafts/"+draft.ID,
		`{"rules": {"lives": 5}, "scenario": {"duration_s": 30}}`), http.StatusOK)
	m, err := content.DecodeManifest(changed.Draft.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Rules.Lives != 5 || m.Scenario.DurationS != 30 || m.Rules.PointsPerHitDefault != 50 {
		t.Errorf("the merged manifest reads %+v %+v", m.Scenario, m.Rules)
	}
	// null removes a value.
	changed = decode[DraftChanged](t, h.call(http.MethodPatch, Prefix+"/drafts/"+draft.ID,
		`{"display": {"fit": null}}`), http.StatusOK)
	m, _ = content.DecodeManifest(changed.Draft.Manifest)
	if m.Display.Fit != "" {
		t.Errorf("the fit is still %q", m.Display.Fit)
	}
}
