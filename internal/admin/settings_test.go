package admin

import (
	"crypto/sha512"
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/config"
	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/settings"
)

// withSettings rebuilds the API and the admin pages of h on rt.
func (h *harness) withSettings(rt *config.Runtime) {
	h.t.Helper()
	api := httpapi.New(httpapi.Options{Store: h.st, Settings: rt, Logger: quiet()})
	a, err := New(Options{API: api.API(), Store: h.st, Key: h.key, Logger: quiet()})
	if err != nil {
		h.t.Fatal(err)
	}
	h.admin, h.settings = a, rt
}

// post sends a form and expects a redirect, which it returns.
func (h *harness) post(path string, form url.Values) string {
	h.t.Helper()
	rec := h.do("POST", path, form, true)
	if rec.Code != http.StatusSeeOther {
		h.t.Fatalf("POST %s answered %d: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

// formOf returns the values the settings page would send unchanged.
func formOf(t *testing.T, page string) url.Values {
	t.Helper()
	form := url.Values{}
	inputs := regexp.MustCompile(`<input type="(?:text|number)" id="[^"]+" name="([^"]+)" value="([^"]*)"`)
	for _, m := range inputs.FindAllStringSubmatch(page, -1) {
		form.Set(m[1], m[2])
	}
	selects := regexp.MustCompile(`(?s)<select id="s-[^"]+" name="([^"]+)"[^>]*>(.*?)</select>`)
	selected := regexp.MustCompile(`<option value="([^"]*)" selected>`)
	for _, m := range selects.FindAllStringSubmatch(page, -1) {
		if s := selected.FindStringSubmatch(m[2]); s != nil {
			form.Set(m[1], s[1])
		}
	}
	if len(form) != len(settings.All()) {
		t.Fatalf("the page offers %d of %d settings: %v", len(form), len(settings.All()), form)
	}
	return form
}

func TestSettingsPageRendersEverySetting(t *testing.T) {
	h := newHarness(t)
	page := h.html("GET", "/admin/settings", nil)
	for _, s := range settings.All() {
		text := s.TextIn("en")
		id := "s-" + strings.ReplaceAll(s.Key, ".", "-")
		contains(t, page,
			`<label for="`+id+`">`+template.HTMLEscapeString(text.Label)+`</label>`,
			`name="`+s.Key+`"`,
			`<span class="tip" role="tooltip" id="tip-`+id+`">`+template.HTMLEscapeString(text.Description)+`</span>`,
			`<summary>Why?</summary><p>`+template.HTMLEscapeString(text.Why)+`</p>`,
			`<code class="key">`+s.Key+`</code>`,
			`name="reset" value="`+s.Key+`" disabled>Reset to default</button>`,
		)
		field := regexp.MustCompile(`(?s)<div class="field[^"]*" id="field-` + id + `">.*?</details>`).FindString(page)
		if s.Restart != strings.Contains(field, "Needs a restart") {
			t.Errorf("%s shows the restart note wrongly", s.Key)
		}
		if s.Kind == settings.Enum && !strings.Contains(field, "<select") {
			t.Errorf("%s is not a select", s.Key)
		}
		if (s.Kind == settings.Int || s.Kind == settings.Duration) && !strings.Contains(field, `type="number"`) {
			t.Errorf("%s is not a number field", s.Key)
		}
		if s.Unit != "" && !strings.Contains(field, `<span class="unit">`+s.Unit+`</span>`) {
			t.Errorf("%s does not show its unit", s.Key)
		}
	}
	for _, section := range settings.Sections() {
		contains(t, page, `<fieldset class="settings-section" id="section-`+section+`">`, "<legend>"+i18n.T("en", "admin.settings.section."+section)+"</legend>")
	}
	contains(t, page,
		`<dt>Current value</dt><dd>5000 ms</dd>`, `<dt>Default</dt><dd>(empty)</dd>`, `<dt>Range</dt><dd>1 to 1024</dd>`,
		`<span class="badge source-default">default</span>`, `min="1" max="1024"`,
		`<option value="de">German</option>`, "0 unsaved changes", `data-singular="%d unsaved change"`, `data-plural="%d unsaved changes"`,
		`id="save-button" type="submit">Save</button>`, `novalidate`, h.configPath,
		`<script src="/admin/static/settings.js" integrity="sha384-`)
	if strings.Contains(page, "restart-banner") {
		t.Error("a fresh server shows the restart banner")
	}
	formOf(t, page)

	script := h.do("GET", "/admin/static/settings.js", nil, false)
	sum := sha512.Sum384(script.Body.Bytes())
	integrity := "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
	if script.Code != http.StatusOK || !strings.Contains(page, `integrity="`+integrity+`"`) ||
		!strings.Contains(script.Body.String(), "unsaved") {
		t.Errorf("settings.js answered %d and does not match the integrity of the page", script.Code)
	}
}

func TestSaveThroughThePageChangesTheFile(t *testing.T) {
	h := newHarness(t)
	form := formOf(t, h.html("GET", "/admin/settings", nil))

	if to := h.post("/admin/settings", form); to != "/admin/settings?unchanged=1" {
		t.Errorf("an unchanged form goes to %s", to)
	}
	contains(t, h.html("GET", "/admin/settings?unchanged=1", nil), "Nothing to save; no setting was changed.")

	form.Set("log.level", "debug")
	form.Set("link.ack_batch", "64")
	to := h.post("/admin/settings", form)
	if to != "/admin/settings?saved=2&now=2&later=0" {
		t.Errorf("the save goes to %s", to)
	}
	data, err := os.ReadFile(h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nlevel = \"debug\"\n") || !strings.Contains(string(data), "\nack_batch = 64\n") {
		t.Errorf("the file holds:\n%s", data)
	}
	if cfg := h.settings.Config(); cfg.Log.Level != "debug" || cfg.Link.AckBatch != 64 {
		t.Errorf("in effect: %+v", cfg)
	}
	page := h.html("GET", to, nil)
	contains(t, page, "Saved: 2 changed, 2 in effect now, 0 after a restart.",
		`<option value="debug" selected>`, `value="64" data-initial="64"`, `<span class="badge source-file">file</span>`)
}

func TestRestartBannerAfterSavingARestartSetting(t *testing.T) {
	h := newHarness(t)
	form := formOf(t, h.html("GET", "/admin/settings", nil))
	form.Set("server.listen_addr", "127.0.0.1:9000")
	form.Set("log.format", "json")
	if to := h.post("/admin/settings", form); to != "/admin/settings?saved=2&now=0&later=2" {
		t.Errorf("the save goes to %s", to)
	}
	page := h.html("GET", "/admin/settings", nil)
	banner := regexp.MustCompile(`(?s)<section class="banner restart" role="status" id="restart-banner">.*?</section>`).FindString(page)
	if banner == "" {
		t.Fatalf("no restart banner:\n%s", page)
	}
	contains(t, banner, "Restart needed", "<li>Listen address</li>", "<li>Log format</li>")
	contains(t, page,
		`<dt>Current value</dt><dd>:8443</dd>`,
		`<dt>After the restart</dt><dd><strong>127.0.0.1:9000</strong></dd>`,
		`value="127.0.0.1:9000" data-initial="127.0.0.1:9000"`)
	if h.settings.Config().Server.ListenAddr != ":8443" {
		t.Error("a restart setting took effect at once")
	}
}

func TestRefusedSaveShowsReasonsNextToTheFields(t *testing.T) {
	h := newHarness(t)
	before, _ := os.ReadFile(h.configPath)
	form := formOf(t, h.html("GET", "/admin/settings", nil))
	form.Set("link.ack_batch", "0")
	form.Set("log.level", "loud")
	form.Set("server.listen_addr", "nowhere")
	form.Set("link.ping_interval_s", "")
	form.Set("log.format", "json")

	for lang, wants := range map[string][]string{
		"en": {"Nothing was saved. Correct the settings marked below.", "Allowed is 1 to 1024.",
			"Allowed are: debug, info, warn, error.", "Enter an address with a port, such as :8443 or 127.0.0.1:8443.",
			"Enter a whole number of s, or a duration such as 2s."},
		"de": {"Nichts wurde gespeichert.", "Erlaubt ist 1 bis 1024.", "Erlaubt sind: debug, info, warn, error.",
			"Geben Sie eine Adresse mit Port an", "Geben Sie eine ganze Zahl in s an oder eine Dauer wie 2s."},
	} {
		h.lang = lang
		rec := h.do("POST", "/admin/settings", form, true)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: a refused save answered %d", lang, rec.Code)
		}
		page := rec.Body.String()
		contains(t, page, wants...)
		contains(t, page, `<div class="field invalid" id="field-s-link-ack_batch">`, `value="0" data-initial="32"`, `value="nowhere" data-initial=":8443"`)
		if strings.Count(page, `class="field-error"`) != 4 {
			t.Errorf("%s: %d field errors, want 4", lang, strings.Count(page, `class="field-error"`))
		}
	}
	after, _ := os.ReadFile(h.configPath)
	if string(before) != string(after) || h.settings.Config() != config.Default() {
		t.Error("a refused save changed something")
	}

	h.lang = "en"
	pair := formOf(t, h.html("GET", "/admin/settings", nil))
	pair.Set("tls.cert_file", "a.crt")
	rec := h.do("POST", "/admin/settings", pair, true)
	if rec.Code != http.StatusBadRequest || strings.Count(rec.Body.String(), "Set the certificate file and the key file together") != 2 {
		t.Errorf("a lone certificate answered %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestPerFieldReset(t *testing.T) {
	h := newHarness(t)
	form := formOf(t, h.html("GET", "/admin/settings", nil))
	form.Set("link.ack_batch", "64")
	h.post("/admin/settings", form)

	to := h.post("/admin/settings/reset", url.Values{"reset": {"link.ack_batch"}})
	if to != "/admin/settings?reset=link.ack_batch" {
		t.Errorf("the reset goes to %s", to)
	}
	h.lang = "de"
	contains(t, h.html("GET", to, nil), "Bestätigungsmenge steht wieder auf dem Standard.")
	if h.settings.Config().Link.AckBatch != 32 {
		t.Error("the batch is not back at its default")
	}
	data, _ := os.ReadFile(h.configPath)
	if !strings.Contains(string(data), "\n# ack_batch = 32\n") {
		t.Errorf("the file still sets the batch:\n%s", data)
	}

	rec := h.do("POST", "/admin/settings/reset", url.Values{"reset": {"no.such"}}, true)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Nichts wurde gespeichert.") {
		t.Errorf("an unknown reset answered %d", rec.Code)
	}
}

func TestLanguageSwitchChangesTheLabels(t *testing.T) {
	h := newHarness(t)
	english := h.html("GET", "/admin/settings", nil)
	contains(t, english, `<html lang="en">`, "Listen address", "Save", `<option value="en" selected>English</option>`)

	rec := h.do("POST", "/admin/language", url.Values{"lang": {"de"}, "back": {"/admin/settings"}}, true)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/settings" {
		t.Fatalf("the switch answered %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != i18n.CookieName || cookies[0].Value != "de" || !cookies[0].HttpOnly ||
		!cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/admin" ||
		cookies[0].MaxAge != int((12*time.Hour).Seconds()) {
		t.Fatalf("the language cookie is %+v", cookies)
	}

	h.lang = cookies[0].Value
	german := h.html("GET", "/admin/settings", nil)
	contains(t, german, `<html lang="de">`, "Adresse und Port", "Speichern", "Warum?", "Aktueller Wert",
		"Braucht einen Neustart", `<option value="de" selected>Deutsch</option>`, `<option value="en">Englisch</option>`,
		"Geräteverbindung", "0 ungespeicherte Änderungen", "Zeit zwischen zwei Pings an ein verbundenes Gerät.")
	for _, s := range settings.All() {
		if english := s.TextIn("en").Label; strings.Contains(german, ">"+english+"<") {
			t.Errorf("the German page shows the English label %q", english)
		}
		contains(t, german, template.HTMLEscapeString(s.TextIn("de").Label), template.HTMLEscapeString(s.TextIn("de").Why))
	}

	for _, back := range []string{"https://example.com/admin/", "//example.com/admin/", "/elsewhere", "/admin/../x", "javascript:alert(1)", ""} {
		rec := h.do("POST", "/admin/language", url.Values{"lang": {"en"}, "back": {back}}, true)
		if to := rec.Header().Get("Location"); to != "/admin/devices" {
			t.Errorf("back %q leads to %q", back, to)
		}
	}
	rec = h.do("POST", "/admin/language", url.Values{"lang": {"fr"}, "back": {"/admin/ranking"}}, false)
	if len(rec.Result().Cookies()) != 0 || rec.Header().Get("Location") != "/admin/ranking" {
		t.Errorf("an unknown language set %v and went to %q", rec.Result().Cookies(), rec.Header().Get("Location"))
	}

	logout := h.do("POST", "/admin/logout", nil, true)
	var cleared bool
	for _, c := range logout.Result().Cookies() {
		if c.Name == i18n.CookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout keeps the language")
	}
}

func TestLockedSettingsAndTheFirstSave(t *testing.T) {
	h := newHarness(t)
	path := h.configPath
	cfg, sources, err := config.LoadWithSources(path,
		func(key string) (string, bool) {
			return map[string]string{"THESERVER_LOG_LEVEL": "warn"}[key], key == "THESERVER_LOG_LEVEL"
		},
		map[string]string{"listen": "127.0.0.1:9443"})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := config.NewRuntime(cfg, sources, quiet())
	if err != nil {
		t.Fatal(err)
	}
	h.withSettings(rt)
	page := h.html("GET", "/admin/settings", nil)
	contains(t, page,
		`<select id="s-log-level" name="log.level" data-initial="warn" disabled>`,
		"Set by the environment variable THESERVER_LOG_LEVEL. Change it there.",
		`name="server.listen_addr" value="127.0.0.1:9443" data-initial="127.0.0.1:9443" spellcheck="false" disabled>`,
		"Set by the command line flag --listen. Change it there.",
		`value="log.level" disabled>`)

	// A form that sends a locked setting anyway changes nothing there.
	form := url.Values{"log.level": {"debug"}, "server.listen_addr": {":1"}, "link.ack_batch": {"16"}}
	if to := h.post("/admin/settings", form); to != "/admin/settings?saved=1&now=1&later=0" {
		t.Errorf("the save goes to %s", to)
	}
	if got := rt.Config(); got.Log.Level != "warn" || got.Server.ListenAddr != "127.0.0.1:9443" || got.Link.AckBatch != 16 {
		t.Errorf("in effect: %+v", got)
	}

	// Started without --config, the first save creates the file in the data
	// directory.
	dir := t.TempDir()
	cfg = config.Default()
	cfg.Server.DataDir = dir
	noFile, err := config.NewRuntime(cfg, config.Sources{}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	h.withSettings(noFile)
	h.lang = "de"
	want := filepath.Join(dir, config.FileName)
	page = h.html("GET", "/admin/settings", nil)
	contains(t, page, "theserver läuft bisher ohne Konfigurationsdatei: Das erste Speichern legt "+template.HTMLEscapeString(want)+" an",
		`id="save-button" type="submit">`)
	if to := h.post("/admin/settings", url.Values{"link.ack_batch": {"16"}}); to != "/admin/settings?saved=1&now=1&later=0" {
		t.Errorf("the first save goes to %s", to)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the first save created no file: %v", err)
	}
	page = h.html("GET", "/admin/settings?saved=1&now=1&later=0", nil)
	contains(t, page, "Speichern schreibt die Änderungen nach "+template.HTMLEscapeString(want)+".", "Gespeichert")
	if strings.Contains(page, "bisher ohne Konfigurationsdatei") {
		t.Error("the page still says there is no file")
	}
}
