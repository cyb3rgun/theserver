package admin

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/store"
)

// syncBuffer collects log output from the handlers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Every page renders in every language without a missing text: i18n logs a
// warning for each key it cannot find, and this test fails on any.
func TestEveryPageInEveryLanguage(t *testing.T) {
	logs := &syncBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	defer slog.SetDefault(old)

	h := newHarness(t)
	h.device("tgt-01", store.StatusApproved)
	h.device("tgt-02", store.StatusPending)
	if err := h.st.CreateSession(context.Background(), store.Session{ID: "s-1"}); err != nil {
		t.Fatal(err)
	}

	titles := map[string]map[string]string{
		"en": {"/admin/devices": "Devices", "/admin/sessions": "Sessions", "/admin/ranking": "Ranking", "/admin/settings": "Settings"},
		"de": {"/admin/devices": "Geräte", "/admin/sessions": "Sitzungen", "/admin/ranking": "Rangliste", "/admin/settings": "Einstellungen"},
	}
	for _, lang := range i18n.Languages() {
		h.lang = lang
		for path, title := range titles[lang] {
			page := h.html("GET", path, nil)
			contains(t, page, `<html lang="`+lang+`">`, "<h1>"+title+"</h1>", i18n.T(lang, "admin.logout"))
		}
		for _, fragment := range []string{"/admin/devices/table", "/admin/ranking/table"} {
			h.html("GET", fragment, nil)
		}
		h.html("POST", "/admin/devices/tgt-02/approve", url.Values{})
		h.html("POST", "/admin/devices/tgt-01/token", url.Values{})
		h.html("POST", "/admin/sessions/s-1/devices", url.Values{"device_id": {"tgt-01"}})

		login := h.do("GET", "/admin/login", nil, false)
		contains(t, login.Body.String(), i18n.T(lang, "admin.login.token"), i18n.T(lang, "admin.login.lasts", 12))
		wrong := h.do("POST", "/admin/login", url.Values{"token": {"nope"}}, false)
		contains(t, wrong.Body.String(), i18n.T(lang, "admin.login.invalid"))
	}
	if strings.Contains(logs.String(), "i18n text missing") {
		t.Errorf("pages asked for texts the catalogues do not have:\n%s", logs.String())
	}

	h.lang = "de"
	devices := h.html("GET", "/admin/devices", nil)
	contains(t, devices, "freigegeben", "Zurücksetzen", "Ziel", "Letzte Bestätigung")
	for _, english := range []string{"Approve", "Reset", "Last ack", "approved"} {
		if strings.Contains(devices, ">"+english+"<") {
			t.Errorf("the German devices page shows %q", english)
		}
	}
}

func TestLanguageComesFromCookieThenDefault(t *testing.T) {
	h := newHarness(t)
	def := "de"
	h.admin.opts.Language = func() string { return def }

	cases := []struct {
		cookie string
		def    string
		want   string
	}{
		{"", "de", "Geräte"},
		{"en", "de", "Devices"},
		{"de", "en", "Geräte"},
		{"fr", "de", "Geräte"},
		{"fr", "fr", "Devices"},
		{"", "", "Devices"},
	}
	for _, c := range cases {
		h.lang, def = c.cookie, c.def
		page := h.html("GET", "/admin/devices", nil)
		if !strings.Contains(page, "<h1>"+c.want+"</h1>") {
			t.Errorf("cookie %q, default %q: the page is not titled %s", c.cookie, c.def, c.want)
		}
	}

	h.admin.opts.Language = nil
	h.lang = ""
	contains(t, h.html("GET", "/admin/devices", nil), "<h1>Devices</h1>")
}

var (
	templateKey = regexp.MustCompile(`\{\{[^}]*?\bt "([^"]+)"`)
	wordGroup   = regexp.MustCompile(`\bword "([^"]+)"`)
	goKey       = regexp.MustCompile(`"(admin\.[a-z_.]+)"`)
)

// Every key a template or the Go code of this package names exists in every
// catalogue, so a typo fails here and not in front of an operator.
func TestUsedKeysExist(t *testing.T) {
	used := map[string]string{}
	files, err := filepath.Glob("templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates: %v", err)
	}
	goFiles, _ := filepath.Glob("*.go")
	for _, file := range append(files, goFiles...) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		pattern := templateKey
		if strings.HasSuffix(file, ".go") {
			pattern = goKey
		}
		for _, m := range pattern.FindAllStringSubmatch(text, -1) {
			// A key ending in a dot is a prefix; KeyFileName is a file.
			if !strings.HasSuffix(m[1], ".") && m[1] != KeyFileName {
				used[m[1]] = file
			}
		}
		for _, m := range wordGroup.FindAllStringSubmatch(text, -1) {
			found := false
			for _, key := range i18n.Keys(i18n.Fallback) {
				if strings.HasPrefix(key, m[1]+".") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s translates values of %s, which has no texts", file, m[1])
			}
		}
	}
	if len(used) < 50 {
		t.Fatalf("found only %d keys; the patterns no longer match the sources", len(used))
	}
	for key, file := range used {
		for _, lang := range i18n.Languages() {
			if !i18n.Has(lang, key) {
				t.Errorf("%s uses %s, which catalogue %s lacks", file, key, lang)
			}
		}
	}
}

// No template holds visible English words outside a translation: text
// between tags is either empty, a template action, code, or punctuation.
func TestTemplatesHoldNoLooseText(t *testing.T) {
	files, _ := filepath.Glob("templates/*.html")
	actions := regexp.MustCompile(`\{\{.*?\}\}`)
	code := regexp.MustCompile(`(?s)<code>.*?</code>|<title>.*?</title>`)
	tags := regexp.MustCompile(`(?s)<[^>]*>`)
	letters := regexp.MustCompile(`[A-Za-z]{2,}`)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := code.ReplaceAllString(string(data), "")
		text = tags.ReplaceAllString(text, "\n")
		text = actions.ReplaceAllString(text, "")
		for _, line := range strings.Split(text, "\n") {
			if word := letters.FindString(line); word != "" {
				t.Errorf("%s has untranslated text %q", file, strings.TrimSpace(line))
			}
		}
		attrs := regexp.MustCompile(`(?:placeholder|aria-label|hx-confirm|title)="([^"]*)"`)
		for _, m := range attrs.FindAllStringSubmatch(string(data), -1) {
			if !strings.HasPrefix(m[1], "{{") {
				t.Errorf("%s has an untranslated attribute %q", file, m[0])
			}
		}
	}
}

func TestLanguageCookieName(t *testing.T) {
	if i18n.CookieName == CookieName {
		t.Error("the language cookie must not be the session cookie")
	}
}
