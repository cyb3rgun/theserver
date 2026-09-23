package admin

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The look of the admin is made of tokens (D-072, D-075): every colour lives
// in static/tokens.css, and every text reaches 4.5 to 1 against the surface
// it is written on, in both sets.

// The theme of a page comes from the cookie and is on the html element
// before the first byte of the body, so a dark screen never flashes white
// (D-073).
func TestThemeSwitchAndCookie(t *testing.T) {
	h := newHarness(t)

	// Without a cookie the page follows the browser and the system.
	page := h.html("GET", "/admin/devices", nil)
	contains(t, page, `<html lang="en" data-theme="auto">`, `id="theme"`,
		`<option value="auto" selected>Automatic</option>`, "Light", "Dark")
	if strings.Index(page, "data-theme") > strings.Index(page, "<body") {
		t.Error("the theme is set after the body starts, which is a flash on a dark screen")
	}
	if !strings.Contains(page, `<link rel="stylesheet" href="/admin/static/tokens.css">`) {
		t.Error("the page does not load the token file")
	}

	// The switch keeps the choice in a cookie of its own.
	rec := h.do("POST", "/admin/theme", url.Values{"theme": {"dark"}, "back": {"/admin/devices"}}, true)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/devices" {
		t.Fatalf("the switch answered %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != ThemeCookieName || cookies[0].Value != "dark" ||
		!cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode ||
		cookies[0].Path != "/admin" {
		t.Fatalf("the theme cookie is %+v", cookies)
	}

	// The choice survives a reload, on every page and on the login page.
	h.theme = "dark"
	for _, path := range []string{"/admin/devices", "/admin/settings", "/admin/sites"} {
		if got := h.html("GET", path, nil); !strings.Contains(got, `data-theme="dark"`) {
			t.Errorf("%s does not carry the chosen theme", path)
		}
	}
	login := h.do("GET", "/admin/login", nil, false)
	if !strings.Contains(login.Body.String(), `data-theme="dark"`) {
		t.Error("the login page does not carry the chosen theme")
	}

	// A cookie that names no theme is read as auto, not as an error.
	h.theme = "purple"
	contains(t, h.html("GET", "/admin/devices", nil), `data-theme="auto"`)

	// The mark and the favicon are in place (D-074).
	h.theme = ""
	page = h.html("GET", "/admin/devices", nil)
	contains(t, page, `<link rel="icon" href="/admin/static/logo.svg" type="image/svg+xml">`,
		`<svg class="mark"`, `fill="var(--mark-ground)"`, `stroke="var(--mark-accent)"`)
	icon := h.do("GET", "/admin/static/logo.svg", nil, true)
	if icon.Code != http.StatusOK {
		t.Fatalf("the favicon answered %d", icon.Code)
	}
	if !strings.Contains(icon.Body.String(), "<svg") || !strings.Contains(icon.Body.String(), "CYB3RGUN") {
		t.Errorf("the favicon is not the mark: %s", icon.Body.String()[:80])
	}
}

// tokenFile is the one file a colour may stand in.
const tokenFile = "tokens.css"

// colours finds a hex colour, an rgb or an hsl call anywhere in a file.
var colours = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`)

// No stylesheet and no script of our own carries a colour of its own; the
// founder changes the look by editing one file (D-072).
func TestEveryColourIsAToken(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("static", "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no static files: %v", err)
	}
	checked := 0
	for _, file := range files {
		name := filepath.Base(file)
		switch {
		case name == tokenFile:
			continue
		case name == "htmx.min.js":
			// Vendored, not ours to edit.
			continue
		case !strings.HasSuffix(name, ".css") && !strings.HasSuffix(name, ".js"):
			continue
		}
		checked++
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if found := colours.FindString(line); found != "" {
				t.Errorf("%s:%d writes the colour %s; put it into %s and use var(--token)",
					name, i+1, found, tokenFile)
			}
		}
	}
	if checked < 4 {
		t.Fatalf("only %d files were checked; the glob no longer matches the static files", checked)
	}

	tokens, err := os.ReadFile(filepath.Join("static", tokenFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--ground", "--panel", "--text", "--muted", "--line", "--accent",
		"--ok", "--warn", "--danger", `[data-theme="dark"]`, "prefers-color-scheme: dark"} {
		if !strings.Contains(string(tokens), want) {
			t.Errorf("%s does not hold %s", tokenFile, want)
		}
	}
}

// Every text token reaches 4.5 to 1 against the surface it is written on, in
// the light set and in the dark one (D-075).
func TestBothThemesAreReadable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("static", tokenFile))
	if err != nil {
		t.Fatal(err)
	}
	sets := map[string]map[string]string{
		"light":     tokensOf(t, string(data), ":root,\n:root[data-theme=\"light\"] {"),
		"dark":      tokensOf(t, string(data), ":root[data-theme=\"dark\"] {"),
		"auto dark": tokensOf(t, string(data), ":root:not([data-theme=\"light\"]) {"),
	}
	pairs := [][2]string{
		{"--text", "--ground"}, {"--text", "--panel"}, {"--text", "--panel-raised"},
		{"--muted", "--ground"}, {"--muted", "--panel"}, {"--muted", "--panel-raised"},
		{"--accent", "--ground"}, {"--accent", "--panel"},
		{"--ok", "--ok-soft"}, {"--ok", "--panel"},
		{"--warn", "--warn-soft"}, {"--warn", "--panel"},
		{"--danger", "--danger-soft"}, {"--danger", "--panel"},
		{"--accent-text", "--accent"}, {"--inverse-text", "--text"},
		{"--stage-text", "--stage"}, {"--mark-accent", "--mark-ground"},
	}
	for name, set := range sets {
		if len(set) < 20 {
			t.Fatalf("the %s set holds only %d tokens", name, len(set))
		}
		for _, pair := range pairs {
			fg, bg := set[pair[0]], set[pair[1]]
			if fg == "" || bg == "" {
				t.Fatalf("the %s set lacks %s or %s", name, pair[0], pair[1])
			}
			if got := contrast(t, fg, bg); got < 4.5 {
				t.Errorf("%s: %s on %s is %.2f to 1, want at least 4.5", name, pair[0], pair[1], got)
			}
		}
	}

	// The dark set of the automatic choice and of the manual one are the
	// same values, so a page does not change when a cookie is set.
	for key, value := range sets["dark"] {
		if sets["auto dark"][key] != value {
			t.Errorf("token %s is %q when the theme is chosen and %q when it follows the system",
				key, value, sets["auto dark"][key])
		}
	}
}

// tokensOf reads the custom properties of the block that starts at marker.
func tokensOf(t *testing.T, css, marker string) map[string]string {
	t.Helper()
	start := strings.Index(css, marker)
	if start < 0 {
		t.Fatalf("%s does not hold the block %q", tokenFile, marker)
	}
	end := strings.Index(css[start:], "}")
	if end < 0 {
		t.Fatalf("the block %q does not end", marker)
	}
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):\s*([^;]+);`).FindAllStringSubmatch(css[start:start+end], -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// contrast is the WCAG ratio of two colours, the lighter over the darker.
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := luminance(t, a), luminance(t, b)
	high, low := math.Max(la, lb), math.Min(la, lb)
	return (high + 0.05) / (low + 0.05)
}

func luminance(t *testing.T, value string) float64 {
	t.Helper()
	r, g, b := parseColour(t, value)
	channel := func(c float64) float64 {
		c /= 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

// parseColour reads a hex colour or an rgb call; an rgba call is read
// without its alpha, which is how it looks over its own surface.
func parseColour(t *testing.T, value string) (float64, float64, float64) {
	t.Helper()
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "#") {
		digits := value[1:]
		if len(digits) == 3 {
			digits = fmt.Sprintf("%c%c%c%c%c%c", digits[0], digits[0], digits[1], digits[1], digits[2], digits[2])
		}
		if len(digits) < 6 {
			t.Fatalf("cannot read the colour %q", value)
		}
		parts := make([]float64, 3)
		for i := range parts {
			n, err := strconv.ParseInt(digits[i*2:i*2+2], 16, 32)
			if err != nil {
				t.Fatalf("cannot read the colour %q: %v", value, err)
			}
			parts[i] = float64(n)
		}
		return parts[0], parts[1], parts[2]
	}
	inside := strings.TrimSuffix(strings.TrimPrefix(value[strings.Index(value, "("):], "("), ")")
	fields := strings.Split(inside, ",")
	if len(fields) < 3 {
		t.Fatalf("cannot read the colour %q", value)
	}
	parts := make([]float64, 3)
	for i := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(fields[i]), 64)
		if err != nil {
			t.Fatalf("cannot read the colour %q: %v", value, err)
		}
		parts[i] = n
	}
	return parts[0], parts[1], parts[2]
}
