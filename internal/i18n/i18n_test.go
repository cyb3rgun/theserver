package i18n

import (
	"bytes"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// Every key exists in every catalogue. A key added on one side only fails
// here, with the keys that are missing named per language.
func TestCataloguesHaveTheSameKeys(t *testing.T) {
	all := map[string]bool{}
	for _, lang := range Languages() {
		if len(Keys(lang)) == 0 {
			t.Fatalf("catalogue %s is empty", lang)
		}
		for _, key := range Keys(lang) {
			all[key] = true
		}
	}
	for _, lang := range Languages() {
		var missing []string
		for key := range all {
			if !Has(lang, key) {
				missing = append(missing, key)
			}
		}
		slices.Sort(missing)
		if len(missing) > 0 {
			t.Errorf("catalogue %s lacks %d keys: %s", lang, len(missing), strings.Join(missing, ", "))
		}
	}
}

var verb = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[a-zA-Z%]`)

func verbs(text string) []string {
	var found []string
	for _, v := range verb.FindAllString(text, -1) {
		if v != "%%" {
			found = append(found, v)
		}
	}
	return found
}

// A translation takes the same arguments as the English text, in the same
// order unless it names them.
func TestTranslationsUseTheSameVerbs(t *testing.T) {
	for _, key := range Keys(Fallback) {
		want := verbs(T(Fallback, key))
		for _, lang := range Languages() {
			got := verbs(catalogs[lang][key])
			if !slices.Equal(got, want) {
				t.Errorf("%s in %s uses %v, English uses %v", key, lang, got, want)
			}
		}
	}
}

func TestNoEmptyOrUntranslatedTexts(t *testing.T) {
	dashes := string([]rune{0x2013, 0x2014})
	for _, lang := range Languages() {
		for _, key := range Keys(lang) {
			text := catalogs[lang][key]
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s %s is empty", lang, key)
			}
			if strings.ContainsAny(text, dashes) {
				t.Errorf("%s %s contains a dash", lang, key)
			}
		}
	}
	same := 0
	for _, key := range Keys("de") {
		if catalogs["de"][key] == catalogs["en"][key] {
			same++
		}
	}
	// Words such as online, Controller or Firmware are the same in both
	// languages; most texts are not.
	if same*4 > len(Keys("de")) {
		t.Errorf("%d of %d German texts equal the English ones", same, len(Keys("de")))
	}
}

func TestT(t *testing.T) {
	if got := T("en", "admin.nav.devices"); got != "Devices" {
		t.Errorf("en devices is %q", got)
	}
	if got := T("de", "admin.nav.devices"); got != "Geräte" {
		t.Errorf("de devices is %q", got)
	}
	if got := T("de", "admin.devices.reset_done", "tgt-01", 2); !strings.Contains(got, "tgt-01") || !strings.Contains(got, "Epoche 2") {
		t.Errorf("formatted text is %q", got)
	}
	if got := T("en", "admin.percent", "65"); got != "65 %" {
		t.Errorf("percent is %q", got)
	}
}

func TestMissingKeysFallBackAndAreReported(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)

	if got := T("fr", "admin.nav.devices"); got != "Devices" {
		t.Errorf("an unknown language gives %q, want the English text", got)
	}
	if got := T("de", "admin.no.such.key"); got != "admin.no.such.key" {
		t.Errorf("a missing key renders as %q, want the key", got)
	}
	T("de", "admin.no.such.key")
	if n := strings.Count(logs.String(), `key=admin.no.such.key`); n != 1 {
		t.Errorf("the missing key was logged %d times, want once:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "lang=fr") {
		t.Errorf("the log lacks the warnings:\n%s", logs.String())
	}
}

func TestResolve(t *testing.T) {
	cases := []struct{ cookie, def, want string }{
		{"de", "en", "de"},
		{"", "de", "de"},
		{"fr", "de", "de"},
		{"", "", "en"},
		{"xx", "yy", "en"},
		{"en", "de", "en"},
	}
	for _, c := range cases {
		if got := Resolve(c.cookie, c.def); got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", c.cookie, c.def, got, c.want)
		}
	}
	if !Supported("de") || Supported("DE") || Supported("") {
		t.Error("Supported accepts the wrong languages")
	}
}

func TestLoadRejectsBrokenCatalogues(t *testing.T) {
	good := "[admin]\ntitle = \"x\"\n"
	cases := map[string]fstest.MapFS{
		"missing language": {"catalog/en.toml": {Data: []byte(good)}},
		"not toml":         {"catalog/en.toml": {Data: []byte(good)}, "catalog/de.toml": {Data: []byte("[admin\n")}},
		"not text":         {"catalog/en.toml": {Data: []byte(good)}, "catalog/de.toml": {Data: []byte("[admin]\ntitle = 5\n")}},
	}
	for name, fsys := range cases {
		if _, err := load(fsys); err == nil {
			t.Errorf("%s: load accepted it", name)
		}
	}
	loaded, err := load(fstest.MapFS{
		"catalog/en.toml": {Data: []byte(good)},
		"catalog/de.toml": {Data: []byte("[admin.nav]\ndevices = \"Geräte\"\n")},
	})
	if err != nil || loaded["de"]["admin.nav.devices"] != "Geräte" || loaded["en"]["admin.title"] != "x" {
		t.Errorf("load flattened to %v, %v", loaded, err)
	}
}
