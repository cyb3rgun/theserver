package content

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/scenario/scenariotest"
	"github.com/cyb3rgun/theserver/internal/store"
)

func newContent(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	db, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := New(filepath.Join(t.TempDir(), "content"), db)
	if err != nil {
		t.Fatal(err)
	}
	return c, db
}

// withVersion is the VIDEO fixture zipped with its manifest at version.
func withVersion(t *testing.T, version string) []byte {
	t.Helper()
	files := scenariotest.Files(t, scenariotest.Dir(scenariotest.Video))
	manifest := string(files[scenario.ManifestName])
	if !strings.Contains(manifest, "version     = 1\n") {
		t.Fatal("the VIDEO fixture has no version line")
	}
	files[scenario.ManifestName] = []byte(strings.Replace(manifest, "version     = 1\n", "version     = "+version+"\n", 1))
	data, err := scenariotest.ZipFiles(files, "")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func put(t *testing.T, c *Store, data []byte) Result {
	t.Helper()
	result, err := c.Put(t.Context(), bytes.NewReader(data), 1<<20, "founder")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	return result
}

// files lists what the content directory holds, with forward slashes.
func files(t *testing.T, c *Store) []string {
	t.Helper()
	var list []string
	err := filepath.WalkDir(c.Dir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == c.Dir() {
			return err
		}
		rel, _ := filepath.Rel(c.Dir(), path)
		if d.IsDir() {
			rel += "/"
		}
		list = append(list, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestPutStoresADraftAsUploaded(t *testing.T) {
	c, db := newContent(t)
	upload := scenariotest.Zip(t, scenariotest.Video)
	result := put(t, c, upload)
	if !result.Stored || result.Replaced || len(result.Problems) != 0 {
		t.Fatalf("the VIDEO fixture came to %+v", result)
	}
	pkg, err := scenario.Load(scenariotest.Dir(scenariotest.Video))
	if err != nil {
		t.Fatal(err)
	}
	sc := result.Scenario
	if sc.ID != "night-range" || sc.Version != 1 || sc.Status != store.ScenarioDraft || sc.Tier != scenario.TierVideo ||
		sc.ManifestHash != scenario.Hash(pkg.Manifest) || sc.Size != int64(len(upload)) || sc.UploadedBy != "founder" ||
		sc.AgeRating != "12" || sc.Title.In("de") != "Nachtschießstand" || sc.UploadedAt == 0 {
		t.Errorf("stored %+v", sc)
	}
	path, err := c.Path("night-range", 1)
	if err != nil {
		t.Fatal(err)
	}
	if kept, err := os.ReadFile(path); err != nil || !bytes.Equal(kept, upload) {
		t.Errorf("the package on disk is not the upload: %v", err)
	}
	if got := files(t, c); !slices.Equal(got, []string{".incoming/", "night-range/", "night-range/1/", "night-range/1/package.zip"}) {
		t.Errorf("the content directory holds %v", got)
	}
	if indexed, err := db.GetScenario(t.Context(), "night-range", 1); err != nil || indexed.ManifestHash != sc.ManifestHash {
		t.Errorf("the index holds %+v, %v", indexed, err)
	}
}

func TestPutRefusesWhatCannotBeStored(t *testing.T) {
	c, db := newContent(t)
	for _, code := range []string{scenario.CodeBadPackage, scenario.CodeNoManifest, scenario.CodeBadManifest, scenario.CodeBadID, scenario.CodeBadVersion} {
		result := put(t, c, scenariotest.Zip(t, scenariotest.Broken(code)))
		if result.Stored || len(result.Problems) != 1 || result.Problems[0].Code != code {
			t.Errorf("%s came to %+v", code, result)
		}
	}
	if got := files(t, c); !slices.Equal(got, []string{".incoming/"}) {
		t.Errorf("refused uploads left %v", got)
	}
	if list, _ := db.ListScenarios(t.Context()); len(list) != 0 {
		t.Errorf("refused uploads are indexed: %+v", list)
	}
}

// A draft with problems is kept, so the page can show them, and cannot be
// published.
func TestDraftWithProblemsIsKeptButNotPublished(t *testing.T) {
	c, _ := newContent(t)
	result := put(t, c, scenariotest.Zip(t, scenariotest.Broken(scenario.CodeHashMismatch)))
	if !result.Stored || len(result.Problems) != 1 || result.Problems[0].Code != scenario.CodeHashMismatch ||
		len(result.Scenario.Problems) != 1 {
		t.Fatalf("the broken draft came to %+v", result)
	}
	if _, err := c.Publish(t.Context(), "night-range", 1); !errors.Is(err, store.ErrHasProblems) {
		t.Errorf("publishing it gave %v", err)
	}
	fixed := put(t, c, scenariotest.Zip(t, scenariotest.Video))
	if !fixed.Stored || !fixed.Replaced || len(fixed.Scenario.Problems) != 0 {
		t.Fatalf("the fixed upload came to %+v", fixed)
	}
	if _, err := c.Publish(t.Context(), "night-range", 1); err != nil {
		t.Errorf("publishing the fixed draft: %v", err)
	}
}

// Put, publish, put again: the next upload has to carry version 2, and the
// published version stays byte for byte (D-037).
func TestPublishedVersionIsNeverOverwritten(t *testing.T) {
	c, db := newContent(t)
	ctx := t.Context()
	put(t, c, scenariotest.Zip(t, scenariotest.Video))
	if _, err := c.Publish(ctx, "night-range", 1); err != nil {
		t.Fatal(err)
	}
	path, _ := c.Path("night-range", 1)
	before, _ := os.ReadFile(path)
	stamp := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	other, err := scenariotest.ZipFiles(scenariotest.Files(t, scenariotest.Dir(scenariotest.Video)), "night-range/")
	if err != nil {
		t.Fatal(err)
	}
	again := put(t, c, other)
	if again.Stored || len(again.Problems) != 1 || again.Problems[0].Code != scenario.CodeVersionTaken ||
		!strings.Contains(again.Problems[0].Detail, "needs version 2") {
		t.Errorf("uploading version 1 again came to %+v", again)
	}
	after, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if !bytes.Equal(before, after) || !info.ModTime().Equal(stamp) {
		t.Error("the published package was written again")
	}
	if err := c.Delete(ctx, "night-range", 1); !errors.Is(err, store.ErrPublished) {
		t.Errorf("deleting the published version gave %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the published package is gone: %v", err)
	}

	next := put(t, c, withVersion(t, "2"))
	if !next.Stored || next.Scenario.Version != 2 || next.Scenario.Status != store.ScenarioDraft {
		t.Fatalf("version 2 came to %+v", next)
	}
	list, _ := db.ListScenarios(ctx)
	if len(list) != 2 || list[0].Version != 2 || list[1].Version != 1 || list[1].Status != store.ScenarioPublished {
		t.Errorf("the index is %+v", list)
	}
	if got := files(t, c); !slices.Equal(got, []string{".incoming/", "night-range/", "night-range/1/", "night-range/1/package.zip", "night-range/2/", "night-range/2/package.zip"}) {
		t.Errorf("the content directory holds %v", got)
	}
}

func TestDeleteRemovesADraft(t *testing.T) {
	c, db := newContent(t)
	put(t, c, scenariotest.Zip(t, scenariotest.Video))
	if err := c.Delete(t.Context(), "night-range", 1); err != nil {
		t.Fatal(err)
	}
	if got := files(t, c); !slices.Equal(got, []string{".incoming/"}) {
		t.Errorf("the deleted draft left %v", got)
	}
	if _, err := db.GetScenario(t.Context(), "night-range", 1); !errors.Is(err, store.ErrScenarioNotFound) {
		t.Errorf("the index still has it: %v", err)
	}
	if err := c.Delete(t.Context(), "night-range", 1); !errors.Is(err, store.ErrScenarioNotFound) {
		t.Errorf("deleting it again gave %v", err)
	}
}

// failingReader delivers some bytes and then fails, like a dropped upload.
type failingReader struct {
	data []byte
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, errors.New("connection reset")
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// An upload that fails on the way leaves nothing behind: not a file, not an
// index row, and a replaced draft is back.
func TestFailedUploadLeavesNothingBehind(t *testing.T) {
	c, db := newContent(t)
	ctx := t.Context()
	upload := scenariotest.Zip(t, scenariotest.Video)

	_, err := c.Put(ctx, &failingReader{data: upload[:len(upload)/2]}, 1<<20, "founder")
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("a dropped upload gave %v", err)
	}
	_, err = c.Put(ctx, bytes.NewReader(upload), int64(len(upload)-1), "founder")
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("an upload over the limit gave %v", err)
	}
	if _, err := c.Put(ctx, bytes.NewReader(upload), int64(len(upload)), "founder"); err != nil {
		t.Errorf("an upload of exactly the limit gave %v", err)
	}
	if err := c.Delete(ctx, "night-range", 1); err != nil {
		t.Fatal(err)
	}
	if got := files(t, c); !slices.Equal(got, []string{".incoming/"}) {
		t.Errorf("failed uploads left %v", got)
	}

	// The index refuses the row: the package goes again, and a draft it
	// would have replaced comes back.
	put(t, c, scenariotest.Zip(t, scenariotest.Broken(scenario.CodeHashMismatch)))
	path, _ := c.Path("night-range", 1)
	draft, _ := os.ReadFile(path)
	if _, err := db.DB().Exec(`CREATE TRIGGER refuse BEFORE UPDATE ON scenarios BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put(ctx, bytes.NewReader(upload), 1<<20, "founder"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("the refused row gave %v", err)
	}
	if kept, _ := os.ReadFile(path); !bytes.Equal(kept, draft) {
		t.Error("the draft was not put back")
	}
	if got := files(t, c); !slices.Equal(got, []string{".incoming/", "night-range/", "night-range/1/", "night-range/1/package.zip"}) {
		t.Errorf("the refused replacement left %v", got)
	}
	if _, err := db.DB().Exec(`DROP TRIGGER refuse; CREATE TRIGGER refuse BEFORE INSERT ON scenarios BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put(ctx, bytes.NewReader(withVersion(t, "2")), 1<<20, "founder"); err == nil {
		t.Fatal("the refused insert was accepted")
	}
	if got := files(t, c); !slices.Equal(got, []string{".incoming/", "night-range/", "night-range/1/", "night-range/1/package.zip"}) {
		t.Errorf("the refused new version left %v", got)
	}
	if list, _ := db.ListScenarios(ctx); len(list) != 1 || len(list[0].Problems) != 1 {
		t.Errorf("the index is %+v", list)
	}

	// What a crash left in the incoming directory goes at the next start.
	leftover := filepath.Join(c.Dir(), incoming, "upload-1")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, PackageName), upload, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c.Dir(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the leftover upload is still there: %v", err)
	}
}

func TestPathChecksTheName(t *testing.T) {
	c, _ := newContent(t)
	path, err := c.Path("night-range", 3)
	if err != nil || path != filepath.Join(c.Dir(), "night-range", "3", PackageName) {
		t.Errorf("Path gave %q, %v", path, err)
	}
	for _, id := range []string{"..", "../x", "a/b", "", "A"} {
		if _, err := c.Path(id, 1); !errors.Is(err, store.ErrScenarioNotFound) {
			t.Errorf("Path(%q) gave %v", id, err)
		}
	}
	if _, err := c.Path("night-range", 0); err == nil {
		t.Error("version 0 has a path")
	}
}
