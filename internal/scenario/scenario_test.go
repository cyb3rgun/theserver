package scenario_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/scenario/scenariotest"
)

var update = flag.Bool("update", false, "write the broken fixtures under testdata/invalid again")

func load(t *testing.T, name string) *scenario.Package {
	t.Helper()
	pkg, err := scenario.Load(scenariotest.Dir(name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return pkg
}

func TestValidFixtures(t *testing.T) {
	video := load(t, scenariotest.Video)
	if problems := video.Validate(); len(problems) != 0 {
		t.Errorf("the VIDEO fixture has problems: %v", problems)
	}
	info := video.Manifest.Scenario
	if info.ID != "night-range" || info.Version != 1 || info.Tier != scenario.TierVideo || info.AgeRating != "12" ||
		info.Title.In("de") != "Nachtschießstand" || info.Title.In("fr") != "Night Range" {
		t.Errorf("the VIDEO fixture reads %+v", info)
	}
	if len(video.Files) != 3 || video.Files["cover.png"] == "" || video.Manifest.Media.Main != "media/main.mp4" {
		t.Errorf("the VIDEO files are %v", video.Files)
	}
	circle := video.Manifest.Zones[0]
	if circle.Shape != "circle" || circle.Radius != 120 || *circle.PointsValue != 100 || video.Manifest.Zones[1].PointsValue != nil {
		t.Errorf("the zones read %+v", video.Manifest.Zones)
	}

	interactive := load(t, scenariotest.Interactive)
	if problems := interactive.Validate(); len(problems) != 0 {
		t.Errorf("the INTERACTIVE fixture has problems: %v", problems)
	}
	m := interactive.Manifest
	if m.Scenario.Tier != scenario.TierInteractive || m.Scenario.AgeRating != "18" || len(m.Media.State) != 3 ||
		len(m.Zones[0].Keyframes) != 2 || m.Zones[2].Keyframes[1].Radius != 70 || len(m.Reaction.Followups) != 3 ||
		m.Reaction.Followups[1].Then != "back:walk" || m.Reaction.Immediate.Blood != "media/blood-1.webm" {
		t.Errorf("the INTERACTIVE fixture reads %+v", m)
	}
}

// pkg is a package as a map of paths to contents, which a breakage edits.
type pkg map[string][]byte

// replace changes the manifest where it holds old, which must be there once.
func (p pkg) replace(t *testing.T, old, new string) {
	t.Helper()
	text := string(p[scenario.ManifestName])
	if n := strings.Count(text, old); n != 1 {
		t.Fatalf("the manifest holds %q %d times", old, n)
	}
	p[scenario.ManifestName] = []byte(strings.Replace(text, old, new, 1))
}

// breakages make one broken fixture per problem code from a valid one. The
// fixtures under testdata/invalid are what these write; run the test with
// -update after changing one.
var breakages = []struct {
	code string
	base string
	edit func(t *testing.T, p pkg)
}{
	{scenario.CodeNoManifest, scenariotest.Video, func(t *testing.T, p pkg) {
		delete(p, scenario.ManifestName)
	}},
	{scenario.CodeBadManifest, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, "[display]", "[display")
	}},
	{scenario.CodeUnknownField, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `fit         = "contain"`, `fit         = "contain"
colour      = "red"`)
	}},
	{scenario.CodeMissingSection, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `[rules]
points_per_hit_default = 50
miss_penalty           = 10
timeout_counts_as_hit  = false
timeout_penalty        = 0
lives                  = 0
score_cap              = 1000

`, "")
	}},
	{scenario.CodeBadID, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `id          = "night-range"`, `id          = "Night Range"`)
	}},
	{scenario.CodeDuplicateID, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `id            = "a-middle"`, `id            = "a-left"`)
	}},
	{scenario.CodeBadVersion, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `version     = 1`, `version     = 0`)
	}},
	{scenario.CodeBadTier, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `tier        = "video"`, `tier        = "hologram"`)
	}},
	{scenario.CodeMissingText, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `title       = { en = "Night Range", de = "Nachtschießstand" }`, `title       = { en = "Night Range" }`)
	}},
	{scenario.CodeNoAgeRating, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `age_rating  = "12"
`, "")
	}},
	{scenario.CodeBadAgeRating, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `age_rating  = "12"`, `age_rating  = "17"`)
	}},
	{scenario.CodeBadValue, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `on_timeout    = "end"`, `on_timeout    = "explode"`)
	}},
	{scenario.CodeNoCanvas, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `canvas      = { w = 1080, h = 1920 }
`, "")
	}},
	{scenario.CodeBadShape, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `points       = [[440, 840], [640, 1080]]`, `points       = [[440, 840], [640, 1080], [540, 900]]`)
	}},
	{scenario.CodeKeyframesUnordered, scenariotest.Interactive, func(t *testing.T, p pkg) {
		p.replace(t, `t_ms   = 5000`, `t_ms   = 2000`)
	}},
	{scenario.CodeBadTimeWindow, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `t_end_ms      = 8000`, `t_end_ms      = 1500`)
	}},
	{scenario.CodeZoneWithoutAppearance, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `[[appearance]]
id            = "a-left"`, `[[zone]]
id           = "z-plate-spare"
name         = { en = "Spare plate", de = "Ersatzscheibe" }
shape        = "circle"
points       = [[540, 1500]]
radius       = 80
zone_class   = "object"

[[appearance]]
id            = "a-left"`)
	}},
	{scenario.CodeZoneShared, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `zones         = ["z-plate-middle"]`, `zones         = ["z-plate-middle", "z-plate-left"]`)
	}},
	{scenario.CodeAppearanceUnknownZone, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `zones         = ["z-plate-left"]`, `zones         = ["z-plate-left", "z-plate-back"]`)
	}},
	{scenario.CodeAppearanceWithoutZones, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `[[zone]]
id           = "z-plate-left"
name         = { en = "Left plate", de = "Linke Scheibe" }
shape        = "circle"
points       = [[270, 960]]
radius       = 120
points_value = 100
zone_class   = "object"

`, "")
		p.replace(t, `zones         = ["z-plate-left"]`, `zones         = []`)
	}},
	{scenario.CodeUnknownAppearance, scenariotest.Interactive, func(t *testing.T, p pkg) {
		p.replace(t, `appearance  = "a-zombie-2"`, `appearance  = "a-zombie-9"`)
	}},
	{scenario.CodeUnknownMediaState, scenariotest.Interactive, func(t *testing.T, p pkg) {
		p.replace(t, `on_timeout    = "nothing"
media_state   = "walk"`, `on_timeout    = "nothing"
media_state   = "crawl"`)
	}},
	{scenario.CodeNotInTier, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `radius       = 120
points_value = 100
zone_class   = "object"
`, `radius       = 120
points_value = 100
zone_class   = "object"

[[zone.keyframe]]
t_ms   = 2000
points = [[270, 960]]
radius = 120
`)
	}},
	{scenario.CodeTierMediaMismatch, scenariotest.Video, func(t *testing.T, p pkg) {
		p.replace(t, `main = "media/main.mp4"
`, `main = "media/main.mp4"

[media.state]
walk = "media/main.mp4"
`)
	}},
	{scenario.CodeMissingMedia, scenariotest.Video, func(t *testing.T, p pkg) {
		delete(p, "media/impact.ogg")
	}},
	{scenario.CodeHashMismatch, scenariotest.Video, func(t *testing.T, p pkg) {
		p["media/main.mp4"] = []byte("a different video than the manifest lists\n")
	}},
	{scenario.CodeUnlistedFile, scenariotest.Video, func(t *testing.T, p pkg) {
		p["media/notes.txt"] = []byte("notes the manifest does not list\n")
	}},
}

// breakPackage is the broken package of the code bad_package: a zip with a
// name that leaves the package.
func breakPackage(t *testing.T) []byte {
	t.Helper()
	p := scenariotest.Files(t, scenariotest.Dir(scenariotest.Video))
	p["../escape.txt"] = []byte("a file outside the package\n")
	data, err := scenariotest.ZipFiles(p, "")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// broken builds the fixture of b: the base with its first comment line
// naming the breakage, then edited.
func broken(t *testing.T, code, base string, edit func(*testing.T, pkg)) pkg {
	t.Helper()
	p := pkg(scenariotest.Files(t, scenariotest.Dir(base)))
	text := string(p[scenario.ManifestName])
	first, rest, _ := strings.Cut(text, "\n")
	if !strings.HasPrefix(first, "# Scenario fixture: a valid") {
		t.Fatalf("the manifest of %s starts with %q", base, first)
	}
	p[scenario.ManifestName] = []byte("# Scenario fixture: broken with exactly the problem " + code +
		", written by TestBrokenFixtures from testdata/" + base + ".\n" + rest)
	edit(t, p)
	return p
}

func TestBrokenFixtures(t *testing.T) {
	for _, b := range breakages {
		t.Run(b.code, func(t *testing.T) {
			want := broken(t, b.code, b.base, b.edit)
			dir := scenariotest.Dir(scenariotest.Broken(b.code))
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				for name, data := range want {
					file := filepath.Join(dir, filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := scenariotest.Files(t, dir); !reflect.DeepEqual(got, map[string][]byte(want)) {
				t.Fatalf("testdata/%s is not what the breakage writes; run go test ./internal/scenario -update", scenariotest.Broken(b.code))
			}
			expectOnly(t, problemsOf(t, dir), b.code)
		})
	}

	t.Run(scenario.CodeBadPackage, func(t *testing.T) {
		want := breakPackage(t)
		file := scenariotest.Dir(scenariotest.Broken(scenario.CodeBadPackage))
		if *update {
			if err := os.WriteFile(file, want, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("testdata/%s is not what the breakage writes (%v); run go test ./internal/scenario -update", scenariotest.Broken(scenario.CodeBadPackage), err)
		}
		expectOnly(t, problemsOf(t, file), scenario.CodeBadPackage)
	})
}

// problemsOf loads a package, a zip when the name says so, and returns its
// problems, a load error as the one problem.
func problemsOf(t *testing.T, path string) []scenario.Problem {
	t.Helper()
	var (
		p   *scenario.Package
		err error
	)
	if strings.HasSuffix(path, ".zip") {
		p, err = scenario.LoadZip(path)
	} else {
		p, err = scenario.Load(path)
	}
	var le *scenario.LoadError
	switch {
	case errors.As(err, &le):
		return []scenario.Problem{le.Problem}
	case err != nil:
		t.Fatalf("load %s: %v", path, err)
	}
	return p.Validate()
}

func expectOnly(t *testing.T, problems []scenario.Problem, code string) {
	t.Helper()
	if len(problems) != 1 || problems[0].Code != code {
		t.Fatalf("want exactly the problem %s, got %d: %v", code, len(problems), problems)
	}
	if problems[0].Detail == "" || problems[0].Field == "" && code != scenario.CodeBadPackage {
		t.Errorf("the problem says too little: %+v", problems[0])
	}
}

// Every code but the one of the server has a broken fixture, and every code
// has a text in every language of the catalogues (D-040).
func TestEveryCodeHasAFixtureAndTexts(t *testing.T) {
	fixtures := []string{scenario.CodeBadPackage}
	for _, b := range breakages {
		fixtures = append(fixtures, b.code)
	}
	for _, code := range scenario.Codes() {
		if code != scenario.CodeVersionTaken && !slices.Contains(fixtures, code) {
			t.Errorf("the code %s has no broken fixture", code)
		}
		for _, lang := range i18n.Languages() {
			if !i18n.Has(lang, "scenario.problem."+code) {
				t.Errorf("catalogue %s has no text for the problem %s", lang, code)
			}
		}
	}
	if len(fixtures) != len(scenario.Codes())-1 {
		t.Errorf("%d fixtures for %d codes", len(fixtures), len(scenario.Codes()))
	}
	for _, key := range i18n.Keys(i18n.Fallback) {
		if code, ok := strings.CutPrefix(key, "scenario.problem."); ok && !slices.Contains(scenario.Codes(), code) {
			t.Errorf("the catalogue has a text for the unknown code %s", code)
		}
	}
	entries, err := os.ReadDir(scenariotest.Dir("invalid"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(fixtures) {
		t.Errorf("testdata/invalid holds %d entries for %d fixtures", len(entries), len(fixtures))
	}
}

func TestZipAndDirectoryLoadsAreEqual(t *testing.T) {
	for _, name := range []string{scenariotest.Video, scenariotest.Interactive, scenariotest.Broken(scenario.CodeUnknownField)} {
		fromDir := load(t, name)
		files := scenariotest.Files(t, scenariotest.Dir(name))
		files[scenario.SignatureName] = []byte("reserved for the signature\n")
		for _, prefix := range []string{"", "package/"} {
			data, err := scenariotest.ZipFiles(files, prefix)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "package.zip")
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			fromZip, err := scenario.LoadZip(file)
			if err != nil {
				t.Fatalf("%s with prefix %q: %v", name, prefix, err)
			}
			if !reflect.DeepEqual(fromDir, fromZip) {
				t.Errorf("%s with prefix %q differs:\n%+v\n%+v", name, prefix, fromDir, fromZip)
			}
			if scenario.Hash(fromZip.Manifest) != scenario.Hash(fromDir.Manifest) {
				t.Errorf("%s: the hashes differ", name)
			}
			read, err := scenario.ReadZip(bytes.NewReader(data), int64(len(data)))
			if err != nil || !reflect.DeepEqual(read, fromZip) {
				t.Errorf("ReadZip differs from LoadZip: %v", err)
			}
		}
	}

	file := scenariotest.WriteZip(t, scenariotest.Video, t.TempDir())
	rc, size, err := scenario.OpenZipFile(file, scenario.CoverName)
	if err != nil {
		t.Fatal(err)
	}
	cover, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(scenariotest.Dir(scenariotest.Video), scenario.CoverName))
	if int64(len(want)) != size || !bytes.Equal(want, cover) {
		t.Errorf("the cover from the zip has %d bytes, the file %d", size, len(want))
	}
	if _, _, err := scenario.OpenZipFile(file, "media/none.mp4"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file gave %v", err)
	}
}

// The expected hash of the INTERACTIVE fixture pins the canonical form: a
// change here changes the hash of every package, which devices compare.
const interactiveHash = "76a6a3090a6d1451c508531006ec525a1cc508ef7fb88d66b5e5d86d2cca6218"

func TestHashIsStable(t *testing.T) {
	p := load(t, scenariotest.Interactive)
	hash := scenario.Hash(p.Manifest)
	if len(hash) != 64 {
		t.Fatalf("the hash is %q", hash)
	}
	if hash != interactiveHash {
		t.Errorf("the hash of the INTERACTIVE fixture is %s, pinned %s", hash, interactiveHash)
	}

	// The same manifest with other map orders, without comments and with
	// other spacing.
	text := string(scenariotest.Files(t, scenariotest.Dir(scenariotest.Interactive))[scenario.ManifestName])
	var lines, files, states []string
	section := ""
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "["):
			section = line
		case section == "[files]" && line != "":
			files = append([]string{line}, files...)
			continue
		case section == "[media.state]" && line != "":
			states = append([]string{strings.Join(strings.Fields(line), " ")}, states...)
			continue
		}
		lines = append(lines, line)
		if line == "[files]" {
			lines = append(lines, "FILES")
		}
		if line == "[media.state]" {
			lines = append(lines, "STATES")
		}
	}
	other := strings.Join(lines, "\n")
	other = strings.Replace(other, "FILES", strings.Join(files, "\n"), 1)
	other = strings.Replace(other, "STATES", strings.Join(states, "\n"), 1)
	other = strings.Replace(other, `title       = { en = "Zombie Alley", de = "Zombie-Gasse" }`, `title = { de = "Zombie-Gasse", en = "Zombie Alley" }`, 1)
	if other == text {
		t.Fatal("the reordered manifest is the same text")
	}
	reordered, err := scenario.ParseManifest([]byte(other))
	if err != nil {
		t.Fatal(err)
	}
	if got := scenario.Hash(reordered); got != hash {
		t.Errorf("reordering maps changed the hash to %s", got)
	}

	// Maps built in another order hash the same.
	m := p.Manifest
	m.Files = map[string]string{}
	keys := slices.Sorted(maps.Keys(p.Manifest.Files))
	slices.Reverse(keys)
	for _, k := range keys {
		m.Files[k] = p.Manifest.Files[k]
	}
	if scenario.Hash(m) != hash {
		t.Error("a files table filled in another order hashes differently")
	}

	// Any other change does not.
	changes := map[string]func(m *scenario.Manifest){
		"a file hash": func(m *scenario.Manifest) {
			m.Files = map[string]string{"media/walk.mp4": strings.Repeat("0", 64)}
		},
		"the zone order": func(m *scenario.Manifest) {
			m.Zones = []scenario.Zone{m.Zones[1], m.Zones[0], m.Zones[2]}
		},
		"a title": func(m *scenario.Manifest) {
			m.Scenario.Title = scenario.Text{"en": "Zombie Alley", "de": "Zombiegasse"}
		},
		"the version": func(m *scenario.Manifest) { m.Scenario.Version = 2 },
	}
	for name, change := range changes {
		changed := load(t, scenariotest.Interactive).Manifest
		change(&changed)
		if scenario.Hash(changed) == hash {
			t.Errorf("changing %s kept the hash", name)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	zipOf := func(files map[string][]byte, prefix string) []byte {
		data, err := scenariotest.ZipFiles(files, prefix)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	video := scenariotest.Files(t, scenariotest.Dir(scenariotest.Video))
	backslash := "media" + string(rune(92)) + "main.mp4"

	twoTops := map[string][]byte{"a/manifest.toml": video[scenario.ManifestName], "b/cover.png": video["cover.png"]}
	withBackslash := map[string][]byte{scenario.ManifestName: video[scenario.ManifestName], backslash: []byte("x")}
	withColon := map[string][]byte{scenario.ManifestName: video[scenario.ManifestName], "c:/media/main.mp4": []byte("x")}
	cases := []struct {
		name string
		load func() error
		code string
		says string
	}{
		{"not a zip", func() error { _, err := scenario.LoadZip(write("text.zip", []byte("not a zip"))); return err }, scenario.CodeBadPackage, "not a readable zip"},
		{"missing zip", func() error { _, err := scenario.LoadZip(filepath.Join(dir, "none.zip")); return err }, scenario.CodeBadPackage, "cannot be opened"},
		{"missing directory", func() error { _, err := scenario.Load(filepath.Join(dir, "none")); return err }, scenario.CodeBadPackage, "cannot be read"},
		{"a file for a directory", func() error { _, err := scenario.Load(write("file", nil)); return err }, scenario.CodeBadPackage, "is not a directory"},
		{"two top directories", func() error { _, err := scenario.LoadZip(write("two.zip", zipOf(twoTops, ""))); return err }, scenario.CodeNoManifest, "one top directory"},
		{"a backslash", func() error { _, err := scenario.LoadZip(write("backslash.zip", zipOf(withBackslash, ""))); return err }, scenario.CodeBadPackage, "not a relative path"},
		{"a drive", func() error { _, err := scenario.LoadZip(write("drive.zip", zipOf(withColon, ""))); return err }, scenario.CodeBadPackage, "not a relative path"},
		{"not TOML", func() error { _, err := scenario.ParseManifest([]byte("[scenario]\nid = ")); return err }, scenario.CodeBadManifest, "line 2"},
		{"a wrong type", func() error { _, err := scenario.ParseManifest([]byte("[scenario]\nversion = \"one\"\n")); return err }, scenario.CodeBadManifest, "version"},
	}
	for _, c := range cases {
		var le *scenario.LoadError
		err := c.load()
		if !errors.As(err, &le) || le.Problem.Code != c.code || !strings.Contains(le.Problem.Detail, c.says) {
			t.Errorf("%s: got %v, want %s saying %q", c.name, err, c.code, c.says)
		}
	}

	// A zip that names a file twice, and one with a directory entry.
	for _, c := range []struct {
		names []string
		code  string
	}{
		{[]string{scenario.ManifestName, "media/main.mp4", "media/main.mp4"}, scenario.CodeBadPackage},
		{[]string{scenario.ManifestName, "media/", "media/main.mp4"}, ""},
	} {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		for _, name := range c.names {
			f, err := w.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if name != "media/" {
				f.Write(video[name])
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := scenario.ReadZip(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		var le *scenario.LoadError
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%v: %v", c.names, err)
		case c.code != "" && (!errors.As(err, &le) || le.Problem.Code != c.code || !strings.Contains(le.Problem.Detail, "twice")):
			t.Errorf("%v: got %v, want %s", c.names, err, c.code)
		}
	}
}

func TestValidateRules(t *testing.T) {
	sum := func(s string) string { return strings.Repeat(s, 64)[:64] }
	base := func() (scenario.Manifest, map[string]string) {
		m := load(t, scenariotest.Interactive).Manifest
		files := map[string]string{}
		for k, v := range m.Files {
			files[k] = v
		}
		return m, files
	}
	codes := func(problems []scenario.Problem) string {
		var out []string
		for _, p := range problems {
			out = append(out, p.Field+" "+p.Code)
		}
		return strings.Join(out, "; ")
	}
	cases := []struct {
		name   string
		change func(m *scenario.Manifest, files map[string]string)
		want   string
	}{
		{"a polygon keyframe with another count", func(m *scenario.Manifest, _ map[string]string) {
			m.Zones[0].Keyframes[1].Points = [][]int{{1, 1}, {2, 1}, {2, 2}}
		}, "zone[0].keyframe[1].points bad_shape"},
		{"a circle without a radius", func(m *scenario.Manifest, _ map[string]string) {
			m.Zones[2].Radius = 0
		}, "zone[2].radius bad_shape"},
		{"a point off the canvas", func(m *scenario.Manifest, _ map[string]string) {
			m.Zones[1].Points = [][]int{{480, 420}, {1081, 800}}
		}, "zone[1].points[1] bad_shape"},
		{"a radius on a polygon", func(m *scenario.Manifest, _ map[string]string) {
			m.Zones[0].Radius = 4
		}, "zone[0].radius bad_shape"},
		{"a point with three numbers", func(m *scenario.Manifest, _ map[string]string) {
			m.Zones[1].Points = [][]int{{480, 420, 1}, {660, 800}}
		}, "zone[1].points[0] bad_shape"},
		{"a rect without area", func(m *scenario.Manifest, _ map[string]string) {
			m.Zones[1].Points = [][]int{{480, 420}, {480, 800}}
		}, "zone[1].points bad_shape"},
		{"a keyframe after the end", func(m *scenario.Manifest, _ map[string]string) {
			m.Zones[2].Keyframes[1].TMs = 20001
		}, "zone[2].keyframe[1].t_ms bad_time_window"},
		{"a window past the end", func(m *scenario.Manifest, _ map[string]string) {
			m.Appearances[1].TEndMs = 20001
		}, "appearance[1] bad_time_window"},
		{"a description in one language", func(m *scenario.Manifest, _ map[string]string) {
			m.Scenario.Description = scenario.Text{"en": "Two zombies."}
		}, "scenario.description.de missing_text"},
		{"no title", func(m *scenario.Manifest, _ map[string]string) {
			m.Scenario.Title = nil
		}, "scenario.title.en missing_text; scenario.title.de missing_text"},
		{"a second follow up for a zone class", func(m *scenario.Manifest, _ map[string]string) {
			m.Reaction.Followups = append(m.Reaction.Followups, m.Reaction.Followups[0])
		}, "reaction.followup[3] duplicate_id"},
		{"back to an unknown state", func(m *scenario.Manifest, _ map[string]string) {
			m.Reaction.Followups[1].Then = "back:crawl"
		}, "reaction.followup[1].then unknown_media_state"},
		{"then without meaning", func(m *scenario.Manifest, _ map[string]string) {
			m.Reaction.Followups[1].Then = "later"
		}, "reaction.followup[1].then bad_value"},
		{"a follow up without time", func(m *scenario.Manifest, _ map[string]string) {
			m.Reaction.Followups[0].DurationMs = 0
		}, "reaction.followup[0].duration_ms bad_value"},
		{"an unknown hitmarker", func(m *scenario.Manifest, _ map[string]string) {
			m.Reaction.Immediate.Hitmarker = "star"
		}, "reaction.immediate.hitmarker bad_value"},
		{"negative lives", func(m *scenario.Manifest, _ map[string]string) {
			m.Rules.Lives = -1
		}, "rules.lives bad_value"},
		{"no required hit", func(m *scenario.Manifest, _ map[string]string) {
			m.Appearances[0].RequiredHits = 0
		}, "appearance[0].required_hits bad_value"},
		{"no duration", func(m *scenario.Manifest, _ map[string]string) {
			m.Scenario.DurationS = 0
		}, "scenario.duration_s bad_value"},
		{"a media state with capitals", func(m *scenario.Manifest, files map[string]string) {
			m.Media.State["Walk"] = m.Media.State["walk"]
		}, "media.state.Walk bad_id"},
		{"a duplicate zone id", func(m *scenario.Manifest, _ map[string]string) {
			m.Zones[2].ID = "z-head"
			m.Appearances[1].Zones = []string{"z-head"}
		}, "zone[2].id duplicate_id; zone[0] zone_shared; zone[2] zone_shared"},
		{"a zone twice in one appearance", func(m *scenario.Manifest, _ map[string]string) {
			m.Appearances[0].Zones = []string{"z-head", "z-torso", "z-head"}
		}, ""},
		{"a manifest in the files table", func(m *scenario.Manifest, _ map[string]string) {
			m.Files[scenario.ManifestName] = sum("a")
		}, `files["manifest.toml"] bad_value`},
		{"a hash in capitals", func(m *scenario.Manifest, _ map[string]string) {
			m.Files["cover.png"] = strings.ToUpper(m.Files["cover.png"])
		}, `files["cover.png"] hash_mismatch`},
		{"a listed file that is gone", func(_ *scenario.Manifest, files map[string]string) {
			delete(files, "cover.png")
		}, `files["cover.png"] missing_media`},
		{"a named file that is not listed", func(m *scenario.Manifest, _ map[string]string) {
			delete(m.Files, "media/walk.mp4")
		}, "media.state.walk missing_media"},
		{"the signature in the files table", func(m *scenario.Manifest, _ map[string]string) {
			m.Files[scenario.SignatureName] = sum("b")
		}, `files["SIGNATURE"] bad_value`},
	}
	for _, c := range cases {
		m, files := base()
		c.change(&m, files)
		if got := codes(scenario.Validate(m, files)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// VIDEO has no follow up and no media state; REALTIME and LAYERED are
	// valid in their own form.
	video := load(t, scenariotest.Video)
	m := load(t, scenariotest.Video).Manifest
	m.Reaction.Followups = []scenario.Followup{{Appearance: "a-left", ZoneClass: "object", MediaState: "x", DurationMs: 1, Then: "end"}}
	m.Appearances[0].MediaState = "walk"
	if got := codes(scenario.Validate(m, video.Files)); got != "appearance[0].media_state not_in_tier; reaction.followup not_in_tier" {
		t.Errorf("VIDEO extras: %q", got)
	}

	realtime := video.Manifest
	realtime.Scenario.Tier = scenario.TierRealtime
	realtime.Media = scenario.Media{Realtime: &scenario.Realtime{World: "alley", Characters: []string{"zombie-a"}}}
	realtime.Reaction.Immediate = nil
	realtime.Reaction.Followups = []scenario.Followup{{Appearance: "a-left", ZoneClass: "object", MediaState: "shatter", DurationMs: 500, Then: "back:idle"}}
	realtime.Files = map[string]string{"cover.png": video.Files["cover.png"]}
	if got := codes(scenario.Validate(realtime, map[string]string{"cover.png": video.Files["cover.png"]})); got != "" {
		t.Errorf("REALTIME: %q", got)
	}
	realtime.Media.Realtime.World = ""
	if got := codes(scenario.Validate(realtime, map[string]string{"cover.png": video.Files["cover.png"]})); got != "media.realtime.world tier_media_mismatch" {
		t.Errorf("REALTIME without a world: %q", got)
	}

	layered := load(t, scenariotest.Interactive).Manifest
	layered.Scenario.Tier = scenario.TierLayered
	states := layered.Media.State
	layered.Media = scenario.Media{
		Background: &scenario.Background{Clip: "media/walk.mp4"},
		Layer:      map[string]scenario.Layer{"zombie": {States: states, Z: 10}},
	}
	files := map[string]string{}
	for k, v := range layered.Files {
		files[k] = v
	}
	if got := codes(scenario.Validate(layered, files)); got != "" {
		t.Errorf("LAYERED: %q", got)
	}
	layered.Media.Layer = map[string]scenario.Layer{"zombie": {}}
	if got := codes(scenario.Validate(layered, files)); !strings.HasPrefix(got, "media.layer.zombie.states tier_media_mismatch; appearance[0].media_state unknown_media_state") {
		t.Errorf("LAYERED without states: %q", got)
	}

	// The same input gives the same problems in the same order.
	broken := load(t, scenariotest.Broken(scenario.CodeZoneShared))
	broken.Manifest.Scenario.ID = ""
	first := scenario.Validate(broken.Manifest, broken.Files)
	for range 20 {
		if again := scenario.Validate(broken.Manifest, broken.Files); !reflect.DeepEqual(first, again) {
			t.Fatalf("the order changed: %v, %v", first, again)
		}
	}
}

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		"night-range": true, "z_head-2": true, "0": true, strings.Repeat("a", 64): true,
		"": false, "Night": false, "-a": false, "a b": false, "a.b": false, "a/b": false, strings.Repeat("a", 65): false, "zoé": false,
	} {
		if scenario.ValidID(id) != want {
			t.Errorf("ValidID(%q) = %v", id, !want)
		}
	}
}
