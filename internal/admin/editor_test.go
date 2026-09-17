package admin

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/editor"
	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/mediakind/mediakindtest"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// otherAdmin adds a second admin token and returns its id, so a test can
// put two people on one draft.
func (h *harness) otherAdmin(name string) string {
	h.t.Helper()
	row, _, err := h.st.AddAdminToken(h.t.Context(), name)
	if err != nil {
		h.t.Fatal(err)
	}
	return row.ID
}

// as sends the next requests as the admin token id, in the language lang.
func (h *harness) as(id, lang string) {
	h.cookie = NewSessionCookie(h.key, id, time.Now())
	h.lang = lang
}

// draft opens a draft through the catalogue form and returns its id.
func (h *harness) draft(id, tier string) string {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/admin/editor", url.Values{
		"id": {id}, "tier": {tier}, "title": {"Test " + id},
	}, true)
	target := redirected(h.t, rec)
	if !strings.HasPrefix(target, "/admin/editor/d-") {
		h.t.Fatalf("a new draft led to %q", target)
	}
	return strings.TrimPrefix(target, "/admin/editor/")
}

// putMedia sends a file to the media panel of a draft.
func (h *harness) putMedia(draft, name string, data []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		h.t.Fatal(err)
	}
	part.Write(data)
	form.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/editor/"+draft+"/media", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.AddCookie(h.cookie)
	if h.lang != "" {
		req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: h.lang})
	}
	rec := httptest.NewRecorder()
	h.admin.ServeHTTP(rec, req)
	return rec
}

// json sends a body to the editor as the script does.
func (h *harness) json(method, path, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(h.cookie)
	rec := httptest.NewRecorder()
	h.admin.ServeHTTP(rec, req)
	return rec
}

// The editor page stands on the draft: the addresses of every action, the
// property panel from the field registry, the media panel, the check and the
// shortcuts (D-041 to D-046).
func TestEditorPageShowsTheDraft(t *testing.T) {
	h := newHarness(t)
	id := h.draft("range-test", "video")
	page := h.html("GET", "/admin/editor/"+id, nil)

	contains(t, page,
		`<h1>`+i18n.T("en", "admin.editor.title"),
		`data-draft="/admin/editor/`+id+`/draft"`,
		`data-patch="/admin/editor/`+id+`/patch"`,
		`id="stage-canvas"`, `id="track"`, `id="panel"`, `id="media"`, `id="problems"`,
		`<script src="/admin/static/editor.js"`,
	)
	// Every field of the panel group carries its label and its why, and the
	// help sits at the field.
	for _, f := range editor.Of(editor.GroupScenario) {
		texts := f.In("en")
		contains(t, page, texts.Label, texts.Description, texts.Why, `id="field-`+f.ID()+`"`)
	}
	// The shortcuts are on the page, as the briefing asks.
	for _, s := range shortcutsIn("en", false) {
		contains(t, page, "<kbd>"+s.Keys+"</kbd>", s.What)
	}
	// A fresh draft cannot be published yet and says why.
	contains(t, page, `id="editor-publish" disabled`, i18n.T("en", "scenario.problem.missing_section"))

	// Another group comes from the same registry.
	panel := h.html("GET", "/admin/editor/"+id+"/panel?select=rules", nil)
	for _, f := range editor.Of(editor.GroupRules) {
		contains(t, panel, f.In("en").Label, `name="`+f.Key+`"`)
	}
}

// The page carries a control for every endpoint of the draft API, and each
// one answers.
func TestEveryDraftEndpointIsReachableFromThePage(t *testing.T) {
	h := newHarness(t)
	id := h.draft("reachable", "interactive")
	// One change, so that the history has a version the page can restore.
	if rec := h.json(http.MethodPost, "/admin/editor/"+id+"/patch", `{"rules":{"lives":4}}`); rec.Code != http.StatusOK {
		t.Fatalf("the first change answered %d: %s", rec.Code, rec.Body.String())
	}
	page := h.html("GET", "/admin/editor/"+id, nil)
	restore := regexp.MustCompile(`action="(/admin/editor/[^"]*/history/\d+/restore)"`).FindStringSubmatch(page)
	if restore == nil {
		t.Fatal("the page carries no restore form")
	}
	h.putMedia(id, "clip.mp4", mediakindtest.Clip())
	media := h.html("GET", "/admin/editor/"+id+"/media", nil)
	catalogue := h.html("GET", "/admin/scenarios", nil)
	preview := h.html("GET", "/admin/editor/"+id+"/preview", nil)

	attributes := regexp.MustCompile(`data-(draft|patch|panel|media|file|measure|validate|publish|trace|lock|unlock|history)="([^"]*)"`)
	found := map[string]string{}
	for _, m := range attributes.FindAllStringSubmatch(page+preview, -1) {
		found[m[1]] = m[2]
	}
	for _, name := range []string{"draft", "patch", "panel", "media", "file", "measure", "validate", "publish", "trace", "lock", "unlock", "history"} {
		if found[name] == "" {
			t.Fatalf("the page carries no address for %s", name)
		}
	}

	// Every endpoint of API v1 below /drafts, with the control that reaches
	// it and what that control answers.
	cases := []struct {
		api     string
		control string
		carries string // what the page holds
		method  string
		path    string
		body    string
	}{
		{"GET /drafts", "the draft list on the catalogue", "/admin/editor/" + id, http.MethodGet, "/admin/scenarios", ""},
		{"POST /drafts", "the new scenario form", `action="/admin/editor"`, http.MethodPost, "/admin/editor", ""},
		{"GET /drafts/{id}", "data-draft", found["draft"], http.MethodGet, found["draft"], ""},
		{"PATCH /drafts/{id}", "data-patch", found["patch"], http.MethodPost, found["patch"], `{"rules":{"lives":4}}`},
		{"DELETE /drafts/{id}", "the delete form", "/admin/editor/" + id + "/delete", http.MethodPost, "/admin/editor/" + id + "/delete", ""},
		{"GET /drafts/{id}/history", "data-history", found["history"], http.MethodGet, found["history"], ""},
		{"POST /drafts/{id}/history/{version}/restore", "the restore form", restore[1], http.MethodPost, restore[1], ""},
		{"POST /drafts/{id}/lock", "data-lock", found["lock"], http.MethodPost, found["lock"], ""},
		{"DELETE /drafts/{id}/lock", "data-unlock", found["unlock"], http.MethodPost, found["unlock"], ""},
		{"POST /drafts/{id}/media", "the upload form", found["media"], http.MethodPost, found["media"], ""},
		{"GET /drafts/{id}/media/{name}", "data-file", found["file"], http.MethodGet, found["file"] + "clip.mp4", ""},
		{"PATCH /drafts/{id}/media/{name}", "data-measure", found["measure"], http.MethodPost, found["measure"] + "clip.mp4/measure", `{"duration_ms":1000,"width":64,"height":64}`},
		{"DELETE /drafts/{id}/media/{name}", "the delete form of a file", found["media"] + "/clip.mp4/delete", http.MethodPost, found["media"] + "/clip.mp4/delete", ""},
		{"POST /drafts/{id}/trace", "data-trace on the preview", found["trace"], http.MethodPost, found["trace"], `{"shots":[{"t_ms":10,"x":1,"y":1}]}`},
		{"POST /drafts/{id}/validate", "data-validate", found["validate"], http.MethodPost, found["validate"], ""},
		{"POST /drafts/{id}/publish", "data-publish", found["publish"], http.MethodPost, found["publish"], ""},
	}

	// The list is complete: every route of the API is in it.
	var routes []string
	for _, r := range httpapi.Routes() {
		if strings.HasPrefix(r.Path, "/drafts") {
			routes = append(routes, r.Method+" "+r.Path)
		}
	}
	for _, c := range cases {
		if !slices.Contains(routes, c.api) {
			t.Errorf("the test names %s, which is not a route", c.api)
		}
	}
	for _, route := range routes {
		if !slices.ContainsFunc(cases, func(c struct {
			api     string
			control string
			carries string
			method  string
			path    string
			body    string
		}) bool {
			return c.api == route
		}) {
			t.Errorf("no control on the page reaches %s", route)
		}
	}

	// The control the page carries is there, and it answers.
	for _, c := range cases {
		if c.path == "" {
			t.Errorf("%s: the control %s is empty", c.api, c.control)
			continue
		}
		if where := page + media + catalogue + preview; !strings.Contains(where, c.carries) {
			t.Errorf("%s: the page does not carry %s (%s)", c.api, c.carries, c.control)
		}
		if c.api == "POST /drafts/{id}/publish" || c.api == "DELETE /drafts/{id}" || c.api == "POST /drafts" {
			continue // these two end the draft or make another one
		}
		var rec *httptest.ResponseRecorder
		if c.body != "" {
			rec = h.json(c.method, c.path, c.body)
		} else {
			rec = h.do(c.method, c.path, nil, true)
		}
		if rec.Code >= 400 {
			t.Errorf("%s: %s %s answered %d: %s", c.api, c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// The preview plays the draft with the rule engine of section 7 in the
// browser and can compare its trace with the one of the server (D-044).
func TestPreviewPage(t *testing.T) {
	h := newHarness(t)
	id := h.draft("preview-test", "interactive")
	page := h.html("GET", "/admin/editor/"+id+"/preview", nil)
	contains(t, page,
		`<h1>`+i18n.T("en", "admin.preview.title"),
		`id="preview-canvas"`, `id="preview-score"`, `id="score-lives"`,
		`data-trace="/admin/editor/`+id+`/trace"`,
		`<script src="/admin/static/rules.js"`, `<script src="/admin/static/preview.js"`,
		i18n.T("en", "admin.preview.check"),
	)
	for _, name := range []string{"rules.js", "preview.js"} {
		script := h.do(http.MethodGet, "/admin/static/"+name, nil, true)
		if script.Code != http.StatusOK {
			t.Fatalf("%s answered %d", name, script.Code)
		}
		sum := sha512.Sum384(script.Body.Bytes())
		hash := strings.ReplaceAll(base64.StdEncoding.EncodeToString(sum[:]), "+", "&#43;")
		if !strings.Contains(page, `integrity="sha384-`+hash+`"`) {
			t.Errorf("the preview does not carry the hash of %s", name)
		}
	}

	// The trace of the server is the one the engine of pkg/scenario
	// writes; the browser compares it with its own.
	answer := h.json(http.MethodPost, "/admin/editor/"+id+"/trace", `{"shots":[{"t_ms":500,"x":10,"y":10}]}`)
	if answer.Code != http.StatusOK {
		t.Fatalf("the trace answered %d: %s", answer.Code, answer.Body.String())
	}
	contains(t, answer.Body.String(), `"kind":"miss"`, `"ended_ms"`)
}

// The integrity hash in the page is the hash of the file that is served, so
// a changed script cannot run (D-043).
func TestEditorScriptIntegrity(t *testing.T) {
	h := newHarness(t)
	id := h.draft("integrity", "video")
	page := h.html("GET", "/admin/editor/"+id, nil)

	script := h.do(http.MethodGet, "/admin/static/editor.js", nil, true)
	if script.Code != http.StatusOK {
		t.Fatalf("editor.js answered %d", script.Code)
	}
	sum := sha512.Sum384(script.Body.Bytes())
	hash := base64.StdEncoding.EncodeToString(sum[:])
	// html/template writes a plus of the base64 as an entity; a browser
	// reads the attribute back before it checks the hash.
	want := `integrity="sha384-` + strings.ReplaceAll(hash, "+", "&#43;") + `"`
	if !strings.Contains(page, want) {
		t.Errorf("the page does not carry the hash of editor.js: %s", want)
	}
	if script.Body.Len() < 4000 {
		t.Errorf("editor.js is only %d bytes", script.Body.Len())
	}
}

// A change in the panel becomes a change of the draft, and a field the model
// refuses says so at the panel.
func TestEditorPanelChangesTheDraft(t *testing.T) {
	h := newHarness(t)
	id := h.draft("panel-test", "video")

	form := url.Values{
		"select":                       {"rules"},
		"rules.points_per_hit_default": {"70"},
		"rules.miss_penalty":           {"5"},
		"rules.timeout_counts_as_hit":  {"true"},
		"rules.timeout_penalty":        {"120"},
		"rules.lives":                  {"2"},
		"rules.score_cap":              {"0"},
	}
	panel := h.html("POST", "/admin/editor/"+id+"/field", form)
	contains(t, panel, `value="70"`, `value="120"`)

	draft := h.do(http.MethodGet, "/admin/editor/"+id+"/draft", nil, true).Body.String()
	contains(t, draft, `"points_per_hit_default":70`, `"lives":2`)

	// A number that is not a number is refused, and the draft keeps its
	// value.
	form.Set("rules.lives", "many")
	refused := h.html("POST", "/admin/editor/"+id+"/field", form)
	contains(t, refused, i18n.T("en", "admin.editor.field_refused"))
	contains(t, h.do(http.MethodGet, "/admin/editor/"+id+"/draft", nil, true).Body.String(), `"lives":2`)
}

// The editor page of a draft that is gone leads back to the catalogue.
func TestEditorOfAMissingDraft(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/admin/editor/d-nothing", nil, true)
	if target := redirected(t, rec); !strings.Contains(target, "draft_gone") {
		t.Errorf("a missing draft led to %q", target)
	}
}

// The keyframe control knows the tier. Zones move in the interactive and the
// layered tier; in the video tier the buttons are not on the page at all and
// a hint says why, so nobody can write a keyframe the check then refuses as
// not_in_tier.
func TestKeyframeControlFollowsTheTier(t *testing.T) {
	h := newHarness(t)
	video := h.html("GET", "/admin/editor/"+h.draft("still-range", "video"), nil)
	moving := h.html("GET", "/admin/editor/"+h.draft("walking-range", "interactive"), nil)

	for _, button := range []string{`data-act="keyframe"`, `data-act="keyframe-delete"`} {
		if strings.Contains(video, button) {
			t.Errorf("the video editor carries %s", button)
		}
		if !strings.Contains(moving, button) {
			t.Errorf("the interactive editor lacks %s", button)
		}
	}
	// The hint stands in their place, in the language of the page.
	contains(t, video, `id="keyframe-hint"`, i18n.T("en", "admin.editor.keyframe_fixed"))
	if strings.Contains(moving, `id="keyframe-hint"`) {
		t.Error("the interactive editor shows the hint for a tier without keyframes")
	}
	h.lang = "de"
	contains(t, h.html("GET", "/admin/editor/"+h.draft("stille-bahn", "video"), nil),
		i18n.T("de", "admin.editor.keyframe_fixed"))
	h.lang = ""

	// The keyboard list follows the buttons: no shortcut for a key that does
	// nothing in this tier.
	for _, key := range []string{"admin.editor.key.keyframe.what", "admin.editor.key.keyframe_delete.what"} {
		if strings.Contains(video, i18n.T("en", key)) {
			t.Errorf("the video editor documents the shortcut %s", key)
		}
		if !strings.Contains(moving, i18n.T("en", key)) {
			t.Errorf("the interactive editor lacks the shortcut %s", key)
		}
	}
	if got, want := len(shortcutsIn("en", false))+2, len(shortcutsIn("en", true)); got != want {
		t.Errorf("the tier drops %d shortcuts, wanted 2", want-len(shortcutsIn("en", false)))
	}

	// The rule the button now follows is the rule of the validator: a
	// keyframe in a video scenario stays a problem.
	id := h.draft("still-check", "video")
	h.putMedia(id, "clip.mp4", mediakindtest.Clip())
	if rec := h.json(http.MethodPost, "/admin/editor/"+id+"/media/clip.mp4/measure",
		`{"duration_ms":2000,"width":64,"height":64}`); rec.Code >= 400 {
		t.Fatalf("the measurement answered %d: %s", rec.Code, rec.Body.String())
	}
	rec := h.json(http.MethodPost, "/admin/editor/"+id+"/patch",
		`{"zone":[{"id":"z-1","shape":"rect","points":[[0,0],[10,0],[10,10],[0,10]],"zone_class":"none","keyframe":[{"t_ms":0,"points":[[0,0],[10,0],[10,10],[0,10]]}]}]}`)
	if rec.Code >= 400 {
		t.Fatalf("the patch answered %d: %s", rec.Code, rec.Body.String())
	}
	problems := h.html("POST", "/admin/editor/"+id+"/validate", nil)
	contains(t, problems, i18n.T("en", "scenario.problem."+scenario.CodeNotInTier))
}

// The editor takes the lock when it opens (D-050). A second admin sees who
// holds the draft, in the language of their page, cannot change it, and
// takes it over with a confirmation; the first page then holds nothing.
func TestEditorLockNoticeAndTakeOver(t *testing.T) {
	h := newHarness(t)
	id := h.draft("shared-range", "video")

	// The page that opened the draft holds it and says nothing about a lock.
	mine := h.html("GET", "/admin/editor/"+id, nil)
	if strings.Contains(mine, `id="editor-lock"`) || strings.Contains(mine, `data-readonly="1"`) {
		t.Error("the page of the holder shows the lock notice")
	}
	contains(t, mine, `data-lock="/admin/editor/`+id+`/lock"`, `data-unlock="/admin/editor/`+id+`/unlock"`,
		`data-lock-every="`+strconv.FormatInt(httpapi.DraftLockTTL.Milliseconds()/3, 10)+`"`)

	// The refresh of the script answers the lock itself.
	rec := h.do(http.MethodPost, "/admin/editor/"+id+"/lock", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("the refresh answered %d: %s", rec.Code, rec.Body.String())
	}
	var refreshed httpapi.DraftLock
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	if !refreshed.Held || !refreshed.Mine {
		t.Errorf("the refresh reads %+v", refreshed)
	}

	// A second admin, in German and in English.
	other := h.otherAdmin("mausi")
	for _, lang := range []string{"en", "de"} {
		h.as(other, lang)
		page := h.html("GET", "/admin/editor/"+id, nil)
		contains(t, page, `id="editor-lock"`, `data-readonly="1"`,
			i18n.T(lang, "admin.editor.locked", "founder"),
			i18n.T(lang, "admin.editor.take_over"),
			`data-confirm="`+i18n.T(lang, "admin.editor.confirm_take_over")+`"`)
	}
	// They are refused every change while the other holds it.
	if rec := h.json(http.MethodPost, "/admin/editor/"+id+"/patch", `{"rules":{"lives":5}}`); rec.Code != http.StatusConflict {
		t.Errorf("a change without the lock answered %d", rec.Code)
	}
	if rec := h.do(http.MethodPost, "/admin/editor/"+id+"/lock", nil, true); rec.Code != http.StatusConflict {
		t.Errorf("a refresh without the lock answered %d", rec.Code)
	}

	// Taking over lands on the editor, which now holds the draft.
	taken := h.do(http.MethodPost, "/admin/editor/"+id+"/lock", url.Values{"take_over": {"1"}}, true)
	if target := redirected(t, taken); target != "/admin/editor/"+id+"?taken=1" {
		t.Fatalf("the takeover led to %q", target)
	}
	page := h.html("GET", "/admin/editor/"+id, nil)
	if strings.Contains(page, `id="editor-lock"`) || strings.Contains(page, `data-readonly="1"`) {
		t.Error("the page of the new holder still shows the lock notice")
	}
	if rec := h.json(http.MethodPost, "/admin/editor/"+id+"/patch", `{"rules":{"lives":5}}`); rec.Code != http.StatusOK {
		t.Errorf("the new holder was refused with %d: %s", rec.Code, rec.Body.String())
	}

	// The first admin is now the one outside, and sees the new name.
	h.as(h.id, "en")
	contains(t, h.html("GET", "/admin/editor/"+id, nil), i18n.T("en", "admin.editor.locked", "mausi"))

	// Leaving releases the lock; a page that opens afterwards holds it.
	h.as(other, "en")
	if rec := h.do(http.MethodPost, "/admin/editor/"+id+"/unlock", nil, true); rec.Code != http.StatusNoContent {
		t.Fatalf("the release answered %d", rec.Code)
	}
	h.as(h.id, "en")
	free := h.html("GET", "/admin/editor/"+id, nil)
	if strings.Contains(free, `id="editor-lock"`) {
		t.Error("a released draft still shows the lock notice")
	}
}

// The history panel of the editor (D-051): the versions the server kept, a
// restore that brings a deleted zone back, and the two buttons undo and redo
// with their shortcuts on the page.
func TestEditorHistoryAndRestore(t *testing.T) {
	h := newHarness(t)
	id := h.draft("history-range", "video")

	page := h.html("GET", "/admin/editor/"+id, nil)
	contains(t, page, `id="editor-undo"`, `id="editor-redo"`,
		i18n.T("en", "admin.editor.undo"), i18n.T("en", "admin.editor.redo"),
		`id="history"`, i18n.T("en", "admin.editor.no_history"),
		i18n.T("en", "admin.editor.history_hint", store.DraftHistoryDepth),
		i18n.T("en", "admin.editor.key.undo.what"))

	// Two zones, then one deleted.
	both := `{"zone":[{"id":"z-plate","shape":"circle","points":[[540,960]],"radius":100,"zone_class":"none"},` +
		`{"id":"z-gong","shape":"circle","points":[[300,600]],"radius":80,"zone_class":"none"}]}`
	if rec := h.json(http.MethodPost, "/admin/editor/"+id+"/patch", both); rec.Code != http.StatusOK {
		t.Fatalf("the first change answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := h.json(http.MethodPost, "/admin/editor/"+id+"/patch",
		`{"zone":[{"id":"z-plate","shape":"circle","points":[[540,960]],"radius":100,"zone_class":"none"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("the delete answered %d: %s", rec.Code, rec.Body.String())
	}

	// The panel names the part of the scenario each change touched, in the
	// language of the page.
	for _, lang := range []string{"en", "de"} {
		h.lang = lang
		list := h.html("GET", "/admin/editor/"+id+"/history", nil)
		contains(t, list, i18n.T(lang, "admin.editor.group.zone"), i18n.T(lang, "admin.editor.restore"),
			`data-confirm="`+i18n.T(lang, "admin.editor.confirm_restore")+`"`, "founder")
	}
	h.lang = ""

	// Restoring the newest version brings the zone back.
	versions := regexp.MustCompile(`/history/(\d+)/restore`).FindAllStringSubmatch(
		h.html("GET", "/admin/editor/"+id+"/history", nil), -1)
	if len(versions) < 2 {
		t.Fatalf("the history holds %d versions", len(versions))
	}
	restored := h.html("POST", "/admin/editor/"+id+"/history/"+versions[0][1]+"/restore", nil)
	contains(t, restored, i18n.T("en", "admin.editor.restore"))
	var draft httpapi.Draft
	rec := h.do(http.MethodGet, "/admin/editor/"+id+"/draft", nil, true)
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(draft.Manifest), "z-gong") {
		t.Errorf("the restored draft holds %s", draft.Manifest)
	}

	// A version that is gone says so and leaves the draft alone.
	gone := h.html("POST", "/admin/editor/"+id+"/history/999999/restore", nil)
	contains(t, gone, i18n.T("en", "admin.editor.restore_failed"))
}
