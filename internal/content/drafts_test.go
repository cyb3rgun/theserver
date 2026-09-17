package content

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario"
	"github.com/cyb3rgun/theserver/pkg/scenario/scenariotest"
)

func TestMergePatchFollowsTheRules(t *testing.T) {
	cases := []struct {
		name     string
		document string
		patch    string
		want     string
	}{
		{"a value", `{"a":1}`, `{"a":2}`, `{"a":2}`},
		{"a new key", `{"a":1}`, `{"b":2}`, `{"a":1,"b":2}`},
		{"null removes", `{"a":1,"b":2}`, `{"a":null}`, `{"b":2}`},
		{"objects merge", `{"a":{"x":1,"y":2}}`, `{"a":{"y":3}}`, `{"a":{"x":1,"y":3}}`},
		{"arrays are replaced", `{"a":[1,2,3]}`, `{"a":[4]}`, `{"a":[4]}`},
		{"a value under an array", `{"a":[1]}`, `{"a":{"b":1}}`, `{"a":{"b":1}}`},
		{"nothing", `{"a":1}`, `{}`, `{"a":1}`},
	}
	for _, c := range cases {
		got, err := MergePatch([]byte(c.document), []byte(c.patch))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	for _, patch := range []string{`[1,2]`, `"text"`, `{`, ``} {
		if _, err := MergePatch([]byte(`{"a":1}`), []byte(patch)); !errors.Is(err, ErrBadManifest) {
			t.Errorf("the patch %q gave %v", patch, err)
		}
	}
}

func TestMediaNamesAreMadeSafe(t *testing.T) {
	for name, want := range map[string]string{
		"clip.mp4":                       "clip.mp4",
		"Mein Video (1).MP4":             "mein-video--1-.mp4",
		"C:" + `\` + "x" + `\` + "a.mp4": "a.mp4",
		"../../etc/passwd.png":           "passwd.png",
		"ZOMBIE_walk-2.webm":             "zombie_walk-2.webm",
		"???":                            "",
		"":                               "",
		".hidden":                        "",
		".hidden.mp4":                    "hidden.mp4",
		"no-extension":                   "",
	} {
		if got := SafeMediaName(name); got != want {
			t.Errorf("SafeMediaName(%q) = %q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]bool{
		"clip.mp4": true, "a.b.mp4": true, "cover.png": true,
		"": false, "no dot": false, "sub/clip.mp4": false, ".hidden.mp4": false, "clip.mp4 ": false,
	} {
		if got := ValidMediaName(name); got != want {
			t.Errorf("ValidMediaName(%q) = %v", name, got)
		}
	}
	if PackagePath("cover.png") != "cover.png" || PackagePath("clip.mp4") != "media/clip.mp4" {
		t.Error("a media file lands in the wrong place in the package")
	}
}

// A draft copied from a published version publishes the next version every
// time, and its directory goes when it goes (D-041, D-042).
func TestDraftPublishesTheNextVersionEveryTime(t *testing.T) {
	c, db := newContent(t)
	ctx := t.Context()
	put(t, c, scenariotest.Zip(t, scenariotest.Video))
	if _, err := c.Publish(ctx, "night-range", 1); err != nil {
		t.Fatal(err)
	}
	sc, err := db.GetScenario(ctx, "night-range", 1)
	if err != nil {
		t.Fatal(err)
	}

	d, err := c.CreateDraft(ctx, NewDraft{ScenarioID: "night-range", From: &sc, By: "founder"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(d.Media) != 3 || d.Media["main.mp4"].SHA256 == "" {
		t.Fatalf("the copy holds %v", d.MediaNames())
	}
	if next, err := c.NextVersion(ctx, "night-range"); err != nil || next != 2 {
		t.Errorf("the next version is %d (%v)", next, err)
	}
	if problems, err := c.ValidateDraft(ctx, d); err != nil || len(problems) != 0 {
		t.Fatalf("the copy does not validate: %v (%v)", problems, err)
	}

	for _, want := range []int{2, 3} {
		published, problems, err := c.PublishDraft(ctx, d.ID, 1<<20, "founder")
		if err != nil || len(problems) != 0 {
			t.Fatalf("publish: %v %v", err, problems)
		}
		if published.Version != want || published.Status != store.ScenarioPublished {
			t.Fatalf("published %+v, want version %d", published, want)
		}
		again, err := db.GetDraft(ctx, d.ID)
		if err != nil || again.PublishedVersion != want {
			t.Fatalf("the draft after the publish: %+v %v", again, err)
		}
	}

	// A draft that names a file it does not have publishes nothing.
	broken, err := c.PatchDraft(ctx, d.ID, []byte(`{"media":{"main":"media/gone.mp4"}}`), "founder")
	if err != nil {
		t.Fatal(err)
	}
	if _, problems, err := c.PublishDraft(ctx, broken.ID, 1<<20, "founder"); !errors.Is(err, ErrDraftProblems) ||
		len(problems) != 1 || problems[0].Code != scenario.CodeMissingMedia {
		t.Errorf("a broken draft published %v (%v)", problems, err)
	}
	if _, err := db.GetScenario(ctx, "night-range", 4); !errors.Is(err, store.ErrScenarioNotFound) {
		t.Errorf("a refused publish left a version: %v", err)
	}

	// The draft directory holds the media and goes with the draft.
	dir := c.DraftMediaDir(d.ID)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("the media directory holds %v (%v)", entries, err)
	}
	if err := c.DeleteDraft(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(dir)); !os.IsNotExist(err) {
		t.Errorf("the draft directory is still there: %v", err)
	}
	if !slices.Contains(files(t, c), "drafts/") {
		t.Error("the drafts directory is gone")
	}
}

// The media of a draft is checked at upload, not at publish (D-045).
func TestDraftMediaIsCheckedAtUpload(t *testing.T) {
	c, _ := newContent(t)
	ctx := t.Context()
	d, err := c.CreateDraft(ctx, NewDraft{ScenarioID: "kinds", Tier: scenario.TierVideo, By: "founder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.PutDraftMedia(ctx, d.ID, "notes.txt", strings.NewReader(strings.Repeat("text\n", 8)), 1<<20, "founder"); !errors.Is(err, ErrBadMedia) {
		t.Errorf("a text file gave %v", err)
	}
	if _, _, err := c.PutDraftMedia(ctx, d.ID, "sub/clip.mp4", strings.NewReader("x"), 1<<20, "founder"); !errors.Is(err, ErrBadName) {
		t.Errorf("a name with a directory gave %v", err)
	}
	if entries, err := os.ReadDir(c.DraftMediaDir(d.ID)); err != nil || len(entries) != 0 {
		t.Errorf("a refused upload left %v (%v)", entries, err)
	}
}
