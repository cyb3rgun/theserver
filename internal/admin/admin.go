// Package admin is the first admin interface of theserver (D-027): server
// rendered html/template pages with HTMX, served by the binary, no build step.
//
// Every button and every value on a page goes through API v1: the handlers
// here turn a page action into an in process API request, authorized by the
// admin token behind the session cookie, and render the JSON answer as HTML.
// The pages hold no logic of their own.
package admin

import (
	"crypto/sha512"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/internal/version"
)

// HTMXVersion is the vendored HTMX release; static/htmx.min.js is its
// htmx.min.js, byte for byte (D-027).
const HTMXVersion = "4.0.0"

// HTMXSHA256 is the SHA-256 of static/htmx.min.js, recorded in D-027 and
// checked by a test.
const HTMXSHA256 = "e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f"

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Options wire the admin pages.
type Options struct {
	// API is the /api/v1 handler, called in process.
	API http.Handler
	// Store checks admin tokens at login and behind every session cookie.
	Store *store.Store
	// Key signs the session cookie; at least 32 bytes, see LoadOrCreateKey.
	Key    []byte
	Logger *slog.Logger
	// Now is the clock of the session cookie; time.Now when nil.
	Now func() time.Time
	// Language is the language of a login that has not chosen one; English
	// when nil (D-031).
	Language func() string
	// SessionLifetime is how long a new login lasts; SessionLifetime when
	// nil. It is asked at every login, so a change applies to the next one.
	SessionLifetime func() time.Duration
}

// Admin serves /admin.
type Admin struct {
	opts      Options
	log       *slog.Logger
	pages     map[string]map[string]*template.Template // by language, then page
	integrity string
	scripts   map[string]string // subresource integrity of our own scripts, by file
	handler   http.Handler
}

var pageNames = []string{"login", "devices", "sessions", "ranking", "settings"}

// New returns the admin handler.
func New(opts Options) (*Admin, error) {
	if opts.API == nil || opts.Store == nil {
		return nil, errors.New("admin: API and Store are required")
	}
	if len(opts.Key) < minKeyLength {
		return nil, fmt.Errorf("admin: the cookie key needs %d bytes, got %d", minKeyLength, len(opts.Key))
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	htmx, err := staticFS.ReadFile("static/htmx.min.js")
	if err != nil {
		return nil, err
	}
	sum := sha512.Sum384(htmx)

	a := &Admin{
		opts:      opts,
		log:       opts.Logger.With("component", "admin"),
		pages:     map[string]map[string]*template.Template{},
		integrity: "sha384-" + base64.StdEncoding.EncodeToString(sum[:]),
		scripts:   map[string]string{},
	}
	for _, name := range []string{"settings.js"} {
		script, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			return nil, err
		}
		sum := sha512.Sum384(script)
		a.scripts[name] = "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
	}
	for _, lang := range i18n.Languages() {
		a.pages[lang] = map[string]*template.Template{}
		for _, name := range pageNames {
			t, err := template.New(name).Funcs(templateFuncs(lang)).ParseFS(templateFS,
				"templates/layout.html", "templates/fragments.html", "templates/"+name+".html")
			if err != nil {
				return nil, fmt.Errorf("admin: template %s: %w", name, err)
			}
			a.pages[lang][name] = t
		}
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin", a.toDevices)
	mux.HandleFunc("GET /admin/{$}", a.toDevices)
	mux.HandleFunc("GET /admin/login", a.loginPage)
	mux.HandleFunc("POST /admin/login", a.login)
	mux.HandleFunc("POST /admin/logout", a.logout)
	mux.HandleFunc("POST /admin/language", a.setLanguage)
	mux.Handle("GET /admin/static/", staticHandler(http.StripPrefix("/admin/static/", http.FileServerFS(static))))

	mux.Handle("GET /admin/devices", a.page(a.devicesPage))
	mux.Handle("GET /admin/devices/table", a.page(a.devicesTable))
	mux.Handle("POST /admin/devices/{id}/{action}", a.page(a.deviceAction))
	mux.Handle("GET /admin/sessions", a.page(a.sessionsPage))
	mux.Handle("POST /admin/sessions", a.page(a.createSession))
	mux.Handle("POST /admin/sessions/{id}/{action}", a.page(a.sessionAction))
	mux.Handle("GET /admin/ranking", a.page(a.rankingPage))
	mux.Handle("GET /admin/ranking/table", a.page(a.rankingTable))
	mux.Handle("GET /admin/settings", a.page(a.settingsPage))
	mux.Handle("POST /admin/settings", a.page(a.saveSettings))
	mux.Handle("POST /admin/settings/reset", a.page(a.resetSetting))

	a.handler = securityHeaders(http.NewCrossOriginProtection().Handler(mux))
	return a, nil
}

// ServeHTTP serves every admin route.
func (a *Admin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
}

func (a *Admin) toDevices(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/devices", http.StatusSeeOther)
}

// securityHeaders keeps the pages to their own origin: scripts and styles only
// from /admin/static, no frames, no referrer, nothing cached.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/admin/static/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func staticHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

// lang is the language of a request: the language cookie, else the
// configured default, else English (D-031).
func (a *Admin) lang(r *http.Request) string {
	chosen := ""
	if c, err := r.Cookie(i18n.CookieName); err == nil {
		chosen = c.Value
	}
	def := ""
	if a.opts.Language != nil {
		def = a.opts.Language()
	}
	return i18n.Resolve(chosen, def)
}

// layout is what every page template gets besides its own data.
type layout struct {
	Lang         string
	Title        string
	Active       string
	Admin        string
	Integrity    string
	Version      string
	Notice       string
	Error        string
	SessionHours int
	// Back is where the language switch returns to.
	Back string
}

// layout fills the frame of a page; titleKey names its title in the
// catalogue.
func (a *Admin) layout(titleKey, active string, s session) layout {
	back := "/admin/login"
	if active != "" {
		back = "/admin/" + active
	}
	return layout{
		Lang:         s.Lang,
		Title:        i18n.T(s.Lang, titleKey),
		Active:       active,
		Admin:        s.Name,
		Integrity:    a.integrity,
		Version:      version.Version,
		SessionHours: int(a.sessionLifetime().Hours()),
		Back:         back,
	}
}

func (a *Admin) render(w http.ResponseWriter, lang string, status int, page, name string, data any) {
	t, ok := a.pages[lang][page]
	if !ok {
		http.Error(w, "unknown page", http.StatusInternalServerError)
		return
	}
	var buf strings.Builder
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		a.log.Error("admin page failed to render", "page", page, "template", name, "error", err)
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, buf.String())
}

// templateFuncs are the functions of the templates of one language. t
// translates a catalogue key; word translates a value such as a device
// status, and shows the value itself when the catalogue has no word for it.
func templateFuncs(lang string) template.FuncMap {
	return template.FuncMap{
		"t": func(key string, args ...any) string {
			return i18n.T(lang, key, args...)
		},
		"word": func(group, value string) string {
			if key := group + "." + value; i18n.Has(lang, key) {
				return i18n.T(lang, key)
			}
			return value
		},
		// option names an allowed value of a setting, such as a language.
		"option": func(setting, value string) string {
			if key := "admin.settings.option." + setting + "." + value; i18n.Has(lang, key) {
				return i18n.T(lang, key)
			}
			return value
		},
		"when": func(ms int64) string {
			if ms == 0 {
				return i18n.T(lang, "admin.never")
			}
			return time.UnixMilli(ms).Local().Format("2006-01-02 15:04:05")
		},
		"path":  url.PathEscape,
		"query": url.QueryEscape,
		"next":  func(n uint64) uint64 { return n + 1 },
		"percent": func(part, whole int) string {
			share := 0.0
			if whole != 0 {
				share = 100 * float64(part) / float64(whole)
			}
			return i18n.T(lang, "admin.percent", strconv.FormatFloat(share, 'f', 0, 64))
		},
		"plus": func(a, b int) int { return a + b },
	}
}
