package admin

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/simtarget"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/journal"
	"github.com/cyb3rgun/theserver/pkg/scenario"
	"github.com/cyb3rgun/theserver/pkg/scenario/scenariotest"
)

// upload posts data as the package field, as the upload form does.
func (h *harness) upload(data []byte) *httptest.ResponseRecorder {
	h.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("package", "package.zip")
	if err != nil {
		h.t.Fatal(err)
	}
	part.Write(data)
	form.Close()
	req := httptest.NewRequest(http.MethodPost, "/admin/scenarios/upload", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.AddCookie(h.cookie)
	if h.lang != "" {
		req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: h.lang})
	}
	rec := httptest.NewRecorder()
	h.admin.ServeHTTP(rec, req)
	return rec
}

// redirected expects a redirect and returns where to.
func redirected(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("answered %d, want a redirect: %s", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

// status expects a page with the status and returns it.
func (h *harness) status(method, path string, form url.Values, want int) string {
	h.t.Helper()
	rec := h.do(method, path, form, true)
	if rec.Code != want {
		h.t.Fatalf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	return rec.Body.String()
}

// withVersion is a fixture zipped with its manifest at another version.
func withVersion(t *testing.T, name, version string) []byte {
	t.Helper()
	files := scenariotest.Files(t, scenariotest.Dir(name))
	manifest := string(files[scenario.ManifestName])
	files[scenario.ManifestName] = []byte(strings.Replace(manifest, "version     = 1\n", "version     = "+version+"\n", 1))
	data, err := scenariotest.ZipFiles(files, "")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// publish puts a fixture into the content store and publishes it.
func (h *harness) publish(name string) {
	h.t.Helper()
	ctx := context.Background()
	result, err := h.content.Put(ctx, bytes.NewReader(scenariotest.Zip(h.t, name)), 1<<30, "founder")
	if err != nil || !result.Stored {
		h.t.Fatalf("put %s: %+v, %v", name, result, err)
	}
	if _, err := h.content.Publish(ctx, result.Scenario.ID, result.Scenario.Version); err != nil {
		h.t.Fatal(err)
	}
}

// The catalogue, the upload with its check in both languages, publish and
// delete (D-036, D-037, D-040).
func TestScenarioCatalogueUploadAndCheck(t *testing.T) {
	h := newHarness(t)
	video := scenariotest.Zip(t, scenariotest.Video)

	empty := h.html("GET", "/admin/scenarios", nil)
	contains(t, empty, "<h1>Scenarios</h1>", "No scenarios yet.", `action="/admin/scenarios/upload"`, `enctype="multipart/form-data"`,
		`name="package"`, `id="upload-progress"`, `src="/admin/static/scenarios.js" integrity="sha384-`,
		"Packages up to 2048 MB are accepted.", `href="/admin/scenarios" class="active"`)

	if to := redirected(t, h.upload(video)); to != "/admin/scenarios/night-range?uploaded=1" {
		t.Fatalf("the upload went to %s", to)
	}
	detail := h.html("GET", "/admin/scenarios/night-range?uploaded=1", nil)
	contains(t, detail, "<h1>Night Range</h1>", "Version 1 was uploaded and checked; it is a draft.",
		`<span class="badge draft">draft</span>`, `<span class="badge ok">valid</span>`, "No problems. This draft can be published.",
		`action="/admin/scenarios/night-range/1/publish">`, `<button class="button primary" type="submit">Publish</button>`,
		`data-confirm="Delete draft version 1? The package is removed."`, `href="/admin/scenarios/night-range/1/package.zip"`,
		"No device has reported this scenario yet.", "12 and up", "Video", `<span class="lang">de</span> Nachtschießstand`)

	catalogue := h.html("GET", "/admin/scenarios", nil)
	contains(t, catalogue, `<tr id="scenario-night-range">`, "<strong>Night Range</strong>", "<code>night-range</code>",
		`src="/admin/scenarios/night-range/1/cover.png"`, "none yet", `<span class="badge draft">version 1</span>`,
		"12 and up", `class="number">2.1 KB</td>`)
	h.lang = "de"
	contains(t, h.html("GET", "/admin/scenarios", nil), "<strong>Nachtschießstand</strong>", "noch keine", "ab 12",
		`<span class="badge draft">Version 1</span>`, `class="number">2,1 KB</td>`, "<h1>Szenarien</h1>")
	h.lang = ""

	cover := h.do("GET", "/admin/scenarios/night-range/1/cover.png", nil, true)
	wantCover, _ := os.ReadFile(filepath.Join(scenariotest.Dir(scenariotest.Video), scenario.CoverName))
	if cover.Code != 200 || cover.Header().Get("Content-Type") != "image/png" || !bytes.Equal(cover.Body.Bytes(), wantCover) {
		t.Errorf("the cover answered %d with %s", cover.Code, cover.Header().Get("Content-Type"))
	}
	if none := h.do("GET", "/admin/scenarios/night-range/9/cover.png", nil, true); none.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("a missing cover answered %s", none.Header().Get("Content-Type"))
	}

	// A draft with a problem replaces the first one; its check is in the
	// language of the page, with field and detail.
	if to := redirected(t, h.upload(scenariotest.Zip(t, scenariotest.Broken(scenario.CodeBadAgeRating)))); to != "/admin/scenarios/night-range?uploaded=1&replaced=1" {
		t.Fatalf("the broken upload went to %s", to)
	}
	broken := h.html("GET", "/admin/scenarios/night-range?uploaded=1&replaced=1", nil)
	contains(t, broken, "Version 1 was uploaded again and checked; it replaces the earlier draft.",
		`<a class="badge problems" href="#check-1">problems: 1</a>`, "Check of version 1",
		"This draft cannot be published.", `<strong>The age rating must be 0, 6, 12, 16 or 18.</strong> <code>scenario.age_rating</code><small class="detail">the age rating &#34;17&#34; is not one of 0, 6, 12, 16, 18</small>`,
		`<button class="button primary" type="submit" disabled>Publish</button>`)
	h.lang = "de"
	contains(t, h.html("GET", "/admin/scenarios/night-range", nil), "Prüfung von Version 1", "Dieser Entwurf kann nicht veröffentlicht werden.",
		"<strong>Die Altersfreigabe muss 0, 6, 12, 16 oder 18 sein.</strong>", `<small class="detail">the age rating &#34;17&#34;`)
	refused := h.status("POST", "/admin/scenarios/night-range/1/publish", url.Values{}, http.StatusConflict)
	contains(t, refused, `<p class="error" role="alert">Dieser Entwurf hat Probleme und kann nicht veröffentlicht werden.<small class="detail">night-range version 1 has 1 problem(s)`)
	h.lang = ""

	// A package that cannot be stored is shown on the catalogue.
	notStored := h.upload(scenariotest.Zip(t, scenariotest.Broken(scenario.CodeBadID)))
	if notStored.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a package with a bad id answered %d", notStored.Code)
	}
	contains(t, notStored.Body.String(), `<p class="error" role="alert">The package was not stored.<small class="detail">the package was not stored: 1 problem(s)</small></p>`,
		`<ul class="problems" id="upload-problems">`, "<strong>An id may only hold lower case letters, digits, hyphens and underscores.</strong> <code>scenario.id</code>",
		`<tr id="scenario-night-range">`)
	h.lang = "de"
	notStored = h.upload(scenariotest.Zip(t, scenariotest.Broken(scenario.CodeBadPackage)))
	contains(t, notStored.Body.String(), "Das Paket wurde nicht gespeichert.", "<strong>Der Upload ist kein lesbares Szenario-Paket.</strong>")
	h.lang = ""

	// The valid package again, published; then a second version as a
	// draft, deleted.
	redirected(t, h.upload(video))
	if to := redirected(t, h.do("POST", "/admin/scenarios/night-range/1/publish", url.Values{}, true)); to != "/admin/scenarios/night-range?published=1" {
		t.Errorf("publish went to %s", to)
	}
	published := h.html("GET", "/admin/scenarios/night-range?published=1", nil)
	contains(t, published, "Version 1 is published. It stays as it is from now on.", `<span class="badge published">published</span>`)
	if strings.Contains(published, "/1/publish") || strings.Contains(published, "/1/delete") {
		t.Error("the published version still offers publish or delete")
	}
	redirected(t, h.upload(withVersion(t, scenariotest.Video, "2")))
	contains(t, h.html("GET", "/admin/scenarios", nil), `<span class="badge published">version 1</span>`, `<span class="badge draft">version 2</span>`)
	if to := redirected(t, h.do("POST", "/admin/scenarios/night-range/2/delete", url.Values{}, true)); to != "/admin/scenarios/night-range?deleted=2" {
		t.Errorf("delete went to %s", to)
	}
	contains(t, h.html("GET", "/admin/scenarios/night-range?deleted=2", nil), "Draft version 2 was deleted.")
	gone := h.status("POST", "/admin/scenarios/night-range/1/delete", url.Values{}, http.StatusConflict)
	contains(t, gone, "A published version stays as it is.")

	// A scenario with only a draft goes away with it.
	redirected(t, h.upload(scenariotest.Zip(t, scenariotest.Interactive)))
	if to := redirected(t, h.do("POST", "/admin/scenarios/zombie-alley/1/delete", url.Values{}, true)); to != "/admin/scenarios?deleted=zombie-alley+1" {
		t.Errorf("deleting the last draft went to %s", to)
	}
	contains(t, h.html("GET", "/admin/scenarios?deleted=zombie-alley+1", nil), "Draft zombie-alley 1 was deleted; the scenario has no version left.")

	download := h.do("GET", "/admin/scenarios/night-range/1/package.zip", nil, true, "Range", "bytes=0-9")
	if download.Code != http.StatusPartialContent || !bytes.Equal(download.Body.Bytes(), video[:10]) {
		t.Errorf("the download answered %d with %d bytes", download.Code, download.Body.Len())
	}
	contains(t, h.status("GET", "/admin/scenarios/nowhere", nil, http.StatusNotFound), "There is no scenario nowhere.")
	h.status("POST", "/admin/scenarios/night-range/1/explode", url.Values{}, http.StatusNotFound)
}

// Sessions choose a published scenario; an age conflict is shown in the
// language of the page (D-039).
func TestSessionsAssignAScenario(t *testing.T) {
	h := newLinkedHarness(t)
	ctx := context.Background()
	h.publish(scenariotest.Video)
	h.publish(scenariotest.Interactive)
	h.device("tgt-01", store.StatusApproved)
	h.device("tgt-03", store.StatusApproved)
	if err := h.st.SetMinAge(ctx, "tgt-03", 16); err != nil {
		t.Fatal(err)
	}
	if err := h.st.CreateSession(ctx, store.Session{ID: "evening"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"tgt-01", "tgt-03"} {
		if err := h.st.AddSessionDevice(ctx, "evening", id); err != nil {
			t.Fatal(err)
		}
	}

	page := h.html("GET", "/admin/sessions", nil)
	contains(t, page, `hx-post="/admin/sessions/evening/scenario"`, `aria-label="Scenario for evening"`,
		`<option value="night-range@1">Night Range (version 1, 12 and up)</option>`,
		`<option value="zombie-alley@1">Zombie Alley (version 1, 18 and up)</option>`)
	if strings.Contains(page, `name="scenario"><`) || strings.Contains(page, `<input name="scenario">`) {
		t.Error("the create form still takes a scenario as text")
	}

	rated := h.html("POST", "/admin/sessions/evening/scenario", url.Values{"scenario": {"zombie-alley@1"}})
	contains(t, rated, `<p class="error" role="alert">The scenario is rated above the age set for a device of this session.<small class="detail">zombie-alley version 1 is rated 18, but device tgt-03 is set for 16</small></p>`)
	h.lang = "de"
	contains(t, h.html("POST", "/admin/sessions/evening/scenario", url.Values{"scenario": {"zombie-alley@1"}}),
		"Das Szenario ist für ein höheres Alter freigegeben, als ein Gerät dieser Sitzung erlaubt.")
	contains(t, h.html("POST", "/admin/sessions/evening/scenario", url.Values{"scenario": {""}}), "Wählen Sie zuerst ein veröffentlichtes Szenario.")
	h.lang = ""

	assigned := h.html("POST", "/admin/sessions/evening/scenario", url.Values{"scenario": {"night-range@1"}})
	contains(t, assigned, "Session evening plays Night Range, version 1.",
		`<li>Offline now, announced after connecting: tgt-01, tgt-03.</li>`,
		`<a href="/admin/scenarios/night-range">Night Range</a> <span class="muted">version 1</span>`)
	session, _ := h.st.GetSession(ctx, "evening")
	if session.Scenario != "night-range" || session.ScenarioVersion != 1 {
		t.Errorf("the session plays %s %d", session.Scenario, session.ScenarioVersion)
	}
}

// The device page shows what a device holds and sets its age (D-039).
func TestDevicePage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publish(scenariotest.Video)
	h.device("tgt-01", store.StatusApproved)
	if err := h.st.CreateSession(ctx, store.Session{ID: "evening"}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.AddSessionDevice(ctx, "evening", "tgt-01"); err != nil {
		t.Fatal(err)
	}
	if err := h.st.AssignScenario(ctx, "evening", "night-range", 1); err != nil {
		t.Fatal(err)
	}

	contains(t, h.html("GET", "/admin/devices", nil), `<a href="/admin/devices/tgt-01/view"><strong>tgt-01</strong></a>`)
	page := h.html("GET", "/admin/devices/tgt-01/view", nil)
	contains(t, page, "<h1>Device tgt-01</h1>", `href="/admin/devices" class="active"`, `<option value="18" selected>18 and up</option>`,
		`<option value="0">all ages</option>`, "evening", `<span class="badge outdated">not installed yet</span>`,
		"The device has not reported any scenario yet.")

	if err := h.st.RecordDeviceScenarios(ctx, "tgt-01", []store.Holding{{ScenarioID: "night-range", Version: 1}, {ScenarioID: "old-range", Version: 3}}); err != nil {
		t.Fatal(err)
	}
	page = h.html("GET", "/admin/devices/tgt-01/view", nil)
	contains(t, page, `<span class="badge ok">installed</span>`, `<a href="/admin/scenarios/night-range">Night Range</a><br><code>night-range</code>`,
		`<span class="badge ok">current</span>`, `<a href="/admin/scenarios/old-range">old-range</a>`, `<span class="badge">held</span>`)
	h.lang = "de"
	contains(t, h.html("GET", "/admin/devices/tgt-01/view", nil), "<h1>Gerät tgt-01</h1>", "installiert", "aktuell", "ab 18", "ohne Altersgrenze")

	if to := redirected(t, h.do("POST", "/admin/devices/tgt-01/age", url.Values{"min_age": {"12"}}, true)); to != "/admin/devices/tgt-01/view?age=12" {
		t.Errorf("setting the age went to %s", to)
	}
	contains(t, h.html("GET", "/admin/devices/tgt-01/view?age=12", nil), "Gerät tgt-01 ist eingestellt auf ab 12.", `<option value="12" selected>ab 12</option>`)
	h.lang = ""
	young := h.status("POST", "/admin/devices/tgt-01/age", url.Values{"min_age": {"6"}}, http.StatusConflict)
	contains(t, young, `The scenario is rated above the age set for a device of this session.<small class="detail">night-range version 1 is rated 12, but device tgt-01 is set for 6</small>`)
	odd := h.status("POST", "/admin/devices/tgt-01/age", url.Values{"min_age": {"old"}}, http.StatusBadRequest)
	contains(t, odd, "The request was not accepted.")
	contains(t, h.status("GET", "/admin/devices/nobody/view", nil, http.StatusNotFound), "There is no device nobody.")
}

// Publish and assign through the pages: the connected simulated target gets
// the announcement, downloads, checks and reports the package installed, and
// the pages show it (D-038).
func TestScenarioChainThroughThePages(t *testing.T) {
	if testing.Short() {
		t.Skip("runs for a few seconds")
	}
	h := newLinkedHarness(t)
	ctx := context.Background()
	h.device("tgt-01", store.StatusApproved)

	redirected(t, h.upload(scenariotest.Zip(t, scenariotest.Interactive)))
	redirected(t, h.do("POST", "/admin/scenarios/zombie-alley/1/publish", url.Values{}, true))
	h.html("POST", "/admin/sessions", url.Values{"id": {"evening"}})
	h.html("POST", "/admin/sessions/evening/devices", url.Values{"device_id": {"tgt-01"}})

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan simtarget.Stats, 1)
	j, err := journal.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var stats *simtarget.Stats
	stop := func() simtarget.Stats {
		if stats == nil {
			cancel()
			got := <-done
			stats = &got
		}
		return *stats
	}
	defer stop()
	go func() {
		stats, err := simtarget.Run(runCtx, simtarget.Options{
			Server: h.server, DeviceID: "tgt-01", Token: "tgt-01", Insecure: true,
			Rate: 2, Health: time.Hour, Reconnect: 100 * time.Millisecond,
			ContentDir: filepath.Join(t.TempDir(), "content"),
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, j)
		if err != nil {
			t.Errorf("simtarget: %v", err)
		}
		done <- stats
	}()
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("%s did not happen in time", what)
	}
	waitFor("the target online", func() bool { return h.online("tgt-01") })

	assigned := h.html("POST", "/admin/sessions/evening/scenario", url.Values{"scenario": {"zombie-alley@1"}})
	contains(t, assigned, "Session evening plays Zombie Alley, version 1.", "<li>Announced to: tgt-01. The targets fetch the package now.</li>")
	waitFor("the installed package", func() bool {
		held, err := h.st.HoldsScenario(ctx, "tgt-01", "zombie-alley", 1)
		return err == nil && held
	})

	contains(t, h.html("GET", "/admin/devices/tgt-01/view", nil), `<span class="badge ok">installed</span>`, `<span class="badge ok">current</span>`)
	contains(t, h.html("GET", "/admin/scenarios/zombie-alley", nil), `<tr id="holding-tgt-01-1">`, `<span class="badge ok">current</span>`)
	if got := stop(); got.Installs != 1 {
		t.Errorf("the target installed %d packages", got.Installs)
	}
}

func (h *harness) online(id string) bool {
	for _, s := range h.link.Online() {
		if s.DeviceID == id {
			return true
		}
	}
	return false
}
