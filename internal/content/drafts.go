package content

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cyb3rgun/theserver/internal/mediakind"
	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/store"
)

// draftsDir is the directory below the content directory that holds the
// editor drafts (D-041): content/drafts/<draft id>/media/<file>. Scenario
// ids cannot hold a slash, and "drafts" is a scenario id a person could
// pick, so a draft directory never meets a version directory: a version
// lives one level deeper, under its number.
const draftsDir = "drafts"

// mediaPrefix is where the media of a draft lands in the package; the cover
// is the one file at the root.
const mediaPrefix = "media/"

var (
	// ErrBadMedia refuses a file whose container or codec no use takes
	// (D-045).
	ErrBadMedia = errors.New("the file is not a media file the editor takes")
	// ErrBadName refuses a media name that is not a plain file name.
	ErrBadName = errors.New("not a usable file name")
	// ErrBadManifest refuses a change that does not fit the scenario model.
	ErrBadManifest = errors.New("the change does not fit the scenario model")
	// ErrDraftProblems refuses to publish a draft that does not validate.
	ErrDraftProblems = errors.New("the draft has problems")
)

// A NewDraft describes the draft to create: a fresh one for a scenario id
// and a tier, or a copy of a published version.
type NewDraft struct {
	ScenarioID string
	Tier       string
	Title      scenario.Text
	// From copies a published version, its manifest and its media; Tier and
	// Title are then taken from it.
	From *store.Scenario
	By   string
}

// DraftsDir is the directory that holds the drafts.
func (c *Store) DraftsDir() string {
	return filepath.Join(c.dir, draftsDir)
}

// DraftMediaDir is the media directory of one draft.
func (c *Store) DraftMediaDir(id string) string {
	return filepath.Join(c.DraftsDir(), id, "media")
}

// CreateDraft makes the directory of a new draft and its row (D-041).
func (c *Store) CreateDraft(ctx context.Context, spec NewDraft) (store.Draft, error) {
	m := scenario.Manifest{
		Scenario: scenario.Info{
			ID: spec.ScenarioID, Version: 1, Tier: spec.Tier, Title: spec.Title,
			AgeRating: "18", Author: spec.By, Licence: "private",
		},
		Display: &scenario.Display{Canvas: &scenario.Canvas{W: 1080, H: 1920}, Orientation: "portrait", Fit: "cover"},
		Rules:   &scenario.Rules{PointsPerHitDefault: 50, TimeoutCountsAsHit: true, TimeoutPenalty: 100, Lives: 3},
	}
	media := map[string]store.DraftMedia{}
	id := draftID()
	dir := c.DraftMediaDir(id)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return store.Draft{}, fmt.Errorf("draft directory of %s: %w", id, err)
	}
	if spec.From != nil {
		copied, files, err := c.copyVersion(*spec.From, dir)
		if err != nil {
			os.RemoveAll(filepath.Dir(dir))
			return store.Draft{}, err
		}
		m, media = copied, files
		m.Scenario.ID = spec.ScenarioID
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		return store.Draft{}, err
	}
	d, err := c.db.CreateDraft(ctx, store.Draft{
		ID: id, ScenarioID: spec.ScenarioID, Manifest: manifest, Media: media, CreatedBy: spec.By,
	})
	if err != nil {
		os.RemoveAll(filepath.Dir(dir))
		return store.Draft{}, err
	}
	return d, nil
}

// copyVersion unpacks a published package into the media directory of a new
// draft and returns its manifest without the files table, which the server
// writes again at publish (D-042).
func (c *Store) copyVersion(sc store.Scenario, dir string) (scenario.Manifest, map[string]store.DraftMedia, error) {
	file, err := c.Path(sc.ID, sc.Version)
	if err != nil {
		return scenario.Manifest{}, nil, err
	}
	pkg, err := scenario.LoadZip(file)
	if err != nil {
		return scenario.Manifest{}, nil, fmt.Errorf("copy %s version %d: %w", sc.ID, sc.Version, err)
	}
	media := map[string]store.DraftMedia{}
	for _, name := range slices.Sorted(maps.Keys(pkg.Files)) {
		plain := mediaName(name)
		if plain == "" {
			continue
		}
		rc, _, err := scenario.OpenZipFile(file, name)
		if err != nil {
			return scenario.Manifest{}, nil, err
		}
		size, sum, err := writeMedia(filepath.Join(dir, plain), rc)
		rc.Close()
		if err != nil {
			return scenario.Manifest{}, nil, err
		}
		kind, _ := mediakind.OfFile(filepath.Join(dir, plain))
		media[plain] = store.DraftMedia{
			Name: plain, Size: size, SHA256: sum,
			Container: kind.Container, Codec: kind.Codec, UploadedAt: c.db.Now(),
		}
	}
	m := pkg.Manifest
	m.Files = nil
	return m, media, nil
}

// mediaName maps a path inside a package to the plain name it has in the
// media directory of a draft, empty when the editor does not keep it.
func mediaName(inPackage string) string {
	switch {
	case inPackage == scenario.CoverName:
		return scenario.CoverName
	case strings.HasPrefix(inPackage, mediaPrefix):
		name := strings.TrimPrefix(inPackage, mediaPrefix)
		if ValidMediaName(name) {
			return name
		}
	}
	return ""
}

// PackagePath is where a media file of a draft lands in the package.
func PackagePath(name string) string {
	if name == scenario.CoverName {
		return name
	}
	return mediaPrefix + name
}

// ValidMediaName reports whether name is a plain file name the editor
// accepts: letters, digits, dot, hyphen and underscore, no directory.
func ValidMediaName(name string) bool {
	if name == "" || len(name) > 96 || name != path.Base(name) || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			return false
		}
	}
	return strings.Contains(name, ".")
}

// SafeMediaName turns the name a browser sends into one the media directory
// takes: the plain file name, lower case, with everything else as a hyphen.
// An empty result means the name cannot be saved at all.
func SafeMediaName(name string) string {
	if cut := strings.LastIndexAny(name, "/"+string(rune(92))); cut >= 0 {
		name = name[cut+1:]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 96 {
		out = out[len(out)-96:]
	}
	if !ValidMediaName(out) {
		return ""
	}
	return out
}

// PutDraftMedia writes an uploaded file into the media directory of a draft.
// The container and the codec must be ones a use takes (D-045), else the
// file is refused with ErrBadMedia and nothing is kept.
func (c *Store) PutDraftMedia(ctx context.Context, id, name string, r io.Reader, limit int64, by string) (store.Draft, store.DraftMedia, error) {
	if !ValidMediaName(name) {
		return store.Draft{}, store.DraftMedia{}, fmt.Errorf("%q: %w", name, ErrBadName)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := c.db.GetDraft(ctx, id)
	if err != nil {
		return store.Draft{}, store.DraftMedia{}, err
	}
	dir := c.DraftMediaDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return store.Draft{}, store.DraftMedia{}, err
	}
	tmp := filepath.Join(dir, ".incoming-"+randomName())
	size, sum, err := writeLimitedMedia(tmp, r, limit)
	if err != nil {
		os.Remove(tmp)
		return store.Draft{}, store.DraftMedia{}, err
	}
	kind, err := mediakind.OfFile(tmp)
	if err == nil && mediakind.UseOf(kind) == "" {
		err = fmt.Errorf("%s: %w", kind, ErrBadMedia)
	}
	if err != nil {
		os.Remove(tmp)
		if errors.Is(err, mediakind.ErrUnknown) {
			return store.Draft{}, store.DraftMedia{}, fmt.Errorf("%w: %w", ErrBadMedia, err)
		}
		return store.Draft{}, store.DraftMedia{}, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return store.Draft{}, store.DraftMedia{}, err
	}
	file := store.DraftMedia{
		Name: name, Size: size, SHA256: sum,
		Container: kind.Container, Codec: kind.Codec, UploadedAt: c.db.Now(),
	}
	d.Media[name] = file
	d, err = c.db.UpdateDraft(ctx, d, by)
	return d, file, err
}

// DraftMediaPath is the file of one media name of a draft, for the editor
// to play. It checks only that the name can be a file; whether the draft
// holds it is the index's to say.
func (c *Store) DraftMediaPath(id, name string) (string, error) {
	if !scenario.ValidID(id) || !ValidMediaName(name) {
		return "", fmt.Errorf("%q %q: %w", id, name, os.ErrNotExist)
	}
	return filepath.Join(c.DraftMediaDir(id), name), nil
}

// MeasuredMedia is what the browser measured on a clip; the server does not
// decode media, so it takes these numbers from the page (D-045).
type MeasuredMedia struct {
	DurationMs int
	Width      int
	Height     int
}

// MeasureDraftMedia records what the browser measured on a media file.
func (c *Store) MeasureDraftMedia(ctx context.Context, id, name string, measured MeasuredMedia, by string) (store.Draft, store.DraftMedia, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := c.db.GetDraft(ctx, id)
	if err != nil {
		return store.Draft{}, store.DraftMedia{}, err
	}
	file, ok := d.Media[name]
	if !ok {
		return store.Draft{}, store.DraftMedia{}, fmt.Errorf("the draft %s has no media file %q: %w", id, name, os.ErrNotExist)
	}
	file.DurationMs, file.Width, file.Height = measured.DurationMs, measured.Width, measured.Height
	d.Media[name] = file
	d, err = c.db.UpdateDraft(ctx, d, by)
	return d, file, err
}

// DeleteDraftMedia removes one media file of a draft. The manifest keeps
// what it says; validation then names the file as missing.
func (c *Store) DeleteDraftMedia(ctx context.Context, id, name, by string) (store.Draft, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := c.db.GetDraft(ctx, id)
	if err != nil {
		return store.Draft{}, err
	}
	if _, ok := d.Media[name]; !ok {
		return store.Draft{}, fmt.Errorf("the draft %s has no media file %q: %w", id, name, os.ErrNotExist)
	}
	if err := os.Remove(filepath.Join(c.DraftMediaDir(id), name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return store.Draft{}, err
	}
	delete(d.Media, name)
	return c.db.UpdateDraft(ctx, d, by)
}

// DeleteDraft removes a draft and everything below its directory.
func (c *Store) DeleteDraft(ctx context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.db.DeleteDraft(ctx, id); err != nil {
		return err
	}
	if !scenario.ValidID(id) {
		return nil
	}
	dir := filepath.Join(c.DraftsDir(), id)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove %s: %w", dir, err)
	}
	return nil
}

// PatchDraft changes the manifest of a draft with a JSON merge patch (RFC
// 7386): a key with a value replaces it, a key with null removes it, arrays
// are replaced as a whole. The result must fit the scenario model, else
// nothing is stored and the field is named. A change that is stored writes
// the state before it into the history of the draft (D-051).
func (c *Store) PatchDraft(ctx context.Context, id string, patch []byte, by string) (store.Draft, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, err := c.db.GetDraft(ctx, id)
	if err != nil {
		return store.Draft{}, err
	}
	merged, err := MergePatch(d.Manifest, patch)
	if err != nil {
		return store.Draft{}, err
	}
	m, err := DecodeManifest(merged)
	if err != nil {
		return store.Draft{}, err
	}
	// The manifest is stored as the model writes it, so the draft never
	// keeps a shape the model does not have.
	normalized, err := json.Marshal(m)
	if err != nil {
		return store.Draft{}, err
	}
	before := d.Manifest
	d.Manifest = normalized
	changed, err := c.db.UpdateDraft(ctx, d, by)
	if err != nil {
		return store.Draft{}, err
	}
	// A change that moved nothing is no version of its own; the editor sends
	// one whenever a shape is dragged, and a history of identical states
	// would push the interesting ones out.
	if !bytes.Equal(before, normalized) {
		if err := c.db.AddDraftVersion(ctx, store.DraftVersion{
			DraftID: id, Patch: json.RawMessage(patch), Manifest: before, By: by,
		}); err != nil {
			return store.Draft{}, err
		}
	}
	return changed, nil
}

// RestoreDraftVersion writes the manifest of one history entry back into the
// draft (D-051). The state before the restore becomes a version of its own,
// so a restore can itself be undone.
func (c *Store) RestoreDraftVersion(ctx context.Context, id string, version int64, by string) (store.Draft, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, err := c.db.DraftVersionOf(ctx, id, version)
	if err != nil {
		return store.Draft{}, err
	}
	d, err := c.db.GetDraft(ctx, id)
	if err != nil {
		return store.Draft{}, err
	}
	m, err := DecodeManifest(v.Manifest)
	if err != nil {
		return store.Draft{}, err
	}
	normalized, err := json.Marshal(m)
	if err != nil {
		return store.Draft{}, err
	}
	before := d.Manifest
	d.Manifest = normalized
	changed, err := c.db.UpdateDraft(ctx, d, by)
	if err != nil {
		return store.Draft{}, err
	}
	if !bytes.Equal(before, normalized) {
		if err := c.db.AddDraftVersion(ctx, store.DraftVersion{
			DraftID: id, Patch: json.RawMessage(`{"restored":true}`), Manifest: before, By: by,
		}); err != nil {
			return store.Draft{}, err
		}
	}
	return changed, nil
}

// DraftHistory lists the versions of a draft, the newest first.
func (c *Store) DraftHistory(ctx context.Context, id string) ([]store.DraftVersion, error) {
	if _, err := c.db.GetDraft(ctx, id); err != nil {
		return nil, err
	}
	return c.db.DraftHistory(ctx, id)
}

// MergePatch applies a JSON merge patch to a JSON object.
func MergePatch(document, patch []byte) ([]byte, error) {
	var target, changes any
	if err := json.Unmarshal(document, &target); err != nil {
		return nil, fmt.Errorf("%w: the draft is not JSON", ErrBadManifest)
	}
	if err := json.Unmarshal(patch, &changes); err != nil {
		return nil, fmt.Errorf("%w: the change is not JSON", ErrBadManifest)
	}
	if _, ok := changes.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: a change is a JSON object", ErrBadManifest)
	}
	return json.Marshal(merge(target, changes))
}

// merge is RFC 7386: objects are merged key by key, null removes a key,
// anything else replaces what was there.
func merge(target, patch any) any {
	changes, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	into, ok := target.(map[string]any)
	if !ok {
		into = map[string]any{}
	}
	for key, value := range changes {
		if value == nil {
			delete(into, key)
			continue
		}
		into[key] = merge(into[key], value)
	}
	return into
}

// DecodeManifest reads a manifest from the JSON of a draft, refusing fields
// the model does not have.
func DecodeManifest(data []byte) (scenario.Manifest, error) {
	var m scenario.Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return scenario.Manifest{}, fmt.Errorf("%w: %s", ErrBadManifest, err)
	}
	return m, nil
}

// DraftPackage is what a draft would publish as: the manifest with the files
// table the server computes, and the files with their hashes (D-042).
func (c *Store) DraftPackage(d store.Draft, version int) (scenario.Manifest, map[string]string, error) {
	m, err := DecodeManifest(d.Manifest)
	if err != nil {
		return scenario.Manifest{}, nil, err
	}
	m.Scenario.Version = version
	files := map[string]string{}
	for _, name := range d.MediaNames() {
		files[PackagePath(name)] = d.Media[name].SHA256
	}
	m.Files = files
	return m, files, nil
}

// ValidateDraft says what a publish would find, without writing anything.
func (c *Store) ValidateDraft(ctx context.Context, d store.Draft) ([]scenario.Problem, error) {
	version, err := c.NextVersion(ctx, d.ScenarioID)
	if err != nil {
		return nil, err
	}
	m, files, err := c.DraftPackage(d, version)
	if err != nil {
		return []scenario.Problem{{Field: "manifest", Code: scenario.CodeBadManifest, Detail: err.Error()}}, nil
	}
	return scenario.Validate(m, files), nil
}

// NextVersion is the version the next publish of a scenario gets: one above
// the latest published version, 1 while none is published (D-037).
func (c *Store) NextVersion(ctx context.Context, id string) (int, error) {
	latest, err := c.db.LatestPublished(ctx, id)
	switch {
	case errors.Is(err, store.ErrScenarioNotFound):
		return 1, nil
	case err != nil:
		return 0, err
	}
	return latest.Version + 1, nil
}

// PublishDraft writes the package of a draft, validates it and publishes it
// through the chain of B07 (D-042): the manifest with its files table, the
// media beside it, zipped, handed to Put and then published. A draft with
// problems publishes nothing and returns them with ErrDraftProblems.
func (c *Store) PublishDraft(ctx context.Context, id string, limit int64, by string) (store.Scenario, []scenario.Problem, error) {
	d, err := c.db.GetDraft(ctx, id)
	if err != nil {
		return store.Scenario{}, nil, err
	}
	version, err := c.NextVersion(ctx, d.ScenarioID)
	if err != nil {
		return store.Scenario{}, nil, err
	}
	m, _, err := c.DraftPackage(d, version)
	if err != nil {
		return store.Scenario{}, []scenario.Problem{{Field: "manifest", Code: scenario.CodeBadManifest, Detail: err.Error()}}, ErrDraftProblems
	}
	if problems, err := c.ValidateDraft(ctx, d); err != nil {
		return store.Scenario{}, nil, err
	} else if len(problems) > 0 {
		return store.Scenario{}, problems, ErrDraftProblems
	}

	file, err := c.writeDraftZip(d, m)
	if err != nil {
		return store.Scenario{}, nil, err
	}
	defer os.RemoveAll(filepath.Dir(file))
	zipped, err := os.Open(file)
	if err != nil {
		return store.Scenario{}, nil, err
	}
	result, err := c.Put(ctx, zipped, limit, by)
	zipped.Close()
	switch {
	case err != nil:
		return store.Scenario{}, nil, err
	case !result.Stored:
		return store.Scenario{}, result.Problems, ErrDraftProblems
	case len(result.Problems) > 0:
		// The package is stored as a draft version; it cannot be published.
		return result.Scenario, result.Problems, ErrDraftProblems
	}
	sc, err := c.Publish(ctx, result.Scenario.ID, result.Scenario.Version)
	if err != nil {
		return store.Scenario{}, nil, err
	}
	if _, err := c.db.PromoteDraft(ctx, id, sc.Version, by); err != nil {
		return store.Scenario{}, nil, err
	}
	return sc, nil, nil
}

// writeDraftZip writes the package of a draft into a temporary directory
// below the content directory and returns the file.
func (c *Store) writeDraftZip(d store.Draft, m scenario.Manifest) (string, error) {
	manifest, err := scenario.WriteManifest(m)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(c.dir, incoming), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Join(c.dir, incoming), "publish-")
	if err != nil {
		return "", err
	}
	file := filepath.Join(tmp, PackageName)
	out, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	w := zip.NewWriter(out)
	err = func() error {
		entry, err := w.Create(scenario.ManifestName)
		if err != nil {
			return err
		}
		if _, err := entry.Write(manifest); err != nil {
			return err
		}
		for _, name := range d.MediaNames() {
			entry, err := w.Create(PackagePath(name))
			if err != nil {
				return err
			}
			source, err := os.Open(filepath.Join(c.DraftMediaDir(d.ID), name))
			if err != nil {
				return err
			}
			_, err = io.Copy(entry, source)
			source.Close()
			if err != nil {
				return err
			}
		}
		return w.Close()
	}()
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("write the package of the draft %s: %w", d.ID, err)
	}
	return file, nil
}

// writeMedia copies r into file and returns its size and SHA-256.
func writeMedia(file string, r io.Reader) (int64, string, error) {
	return writeLimitedMedia(file, r, 1<<62)
}

func writeLimitedMedia(file string, r io.Reader, limit int64) (int64, string, error) {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, "", err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return 0, "", fmt.Errorf("receive the file: %w", err)
	case n > limit:
		return 0, "", fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}
	return n, hex.EncodeToString(sum.Sum(nil)), nil
}

// draftID is the id of a new draft: short, readable, a valid scenario id so
// that it can name a directory.
func draftID() string {
	var b [6]byte
	rand.Read(b[:])
	return "d-" + hex.EncodeToString(b[:])
}
