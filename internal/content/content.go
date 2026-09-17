// Package content keeps the scenario packages of theserver on disk (D-036):
// every version lives in content/<id>/<version>/package.zip exactly as it was
// uploaded, beside its index row in the store. A published version is never
// written again or removed (D-037).
package content

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/store"
)

// PackageName is the file a version is kept in.
const PackageName = "package.zip"

// incoming is the directory below the content directory where uploads are
// written before they are moved into place. Scenario ids cannot start with a
// dot, so it never meets a scenario.
const incoming = ".incoming"

// ErrTooLarge refuses an upload above the limit.
var ErrTooLarge = errors.New("the package is larger than the upload limit")

// Store is the content directory with the index of the store.
type Store struct {
	dir string
	db  *store.Store
	// mu serializes the changes of the content directory, so an upload, a
	// publish and a delete of the same version cannot interleave.
	mu sync.Mutex
}

// New opens the content directory dir, creating it when missing, and removes
// what an upload that was cut off left behind.
func New(dir string, db *store.Store) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("content directory %s: %w", dir, err)
	}
	if err := os.RemoveAll(filepath.Join(dir, incoming)); err != nil {
		return nil, fmt.Errorf("content directory %s: %w", dir, err)
	}
	return &Store{dir: dir, db: db}, nil
}

// Dir is the content directory.
func (c *Store) Dir() string {
	return c.dir
}

// A Result is what an upload came to. A package that could not be read, or
// whose id or version cannot name it, or whose version is published already,
// is not stored; Problems says why. A stored draft may have problems too.
type Result struct {
	Stored   bool
	Replaced bool // a draft of the same version was replaced
	Scenario store.Scenario
	Problems []scenario.Problem
}

// Put reads an uploaded package from r, at most limit bytes, validates it
// and keeps it as a draft of the id and version its manifest names. The file
// is written below the content directory first and moved into place only
// when it is complete and valid enough to be stored, so a failed upload
// leaves nothing behind. by names who uploaded it.
func (c *Store) Put(ctx context.Context, r io.Reader, limit int64, by string) (Result, error) {
	if err := os.MkdirAll(filepath.Join(c.dir, incoming), 0o755); err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp(filepath.Join(c.dir, incoming), "upload-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)

	file := filepath.Join(tmp, PackageName)
	size, err := writeLimited(file, r, limit)
	if err != nil {
		return Result{}, err
	}

	pkg, err := scenario.LoadZip(file)
	var le *scenario.LoadError
	switch {
	case errors.As(err, &le):
		return Result{Problems: []scenario.Problem{le.Problem}}, nil
	case err != nil:
		return Result{}, err
	}
	m := pkg.Manifest
	sc := store.Scenario{
		ID:           m.Scenario.ID,
		Version:      m.Scenario.Version,
		Tier:         m.Scenario.Tier,
		Title:        m.Scenario.Title,
		AgeRating:    m.Scenario.AgeRating,
		ManifestHash: scenario.Hash(m),
		Size:         size,
		UploadedBy:   by,
		Status:       store.ScenarioDraft,
		Problems:     pkg.Validate(),
	}
	result := Result{Scenario: sc, Problems: sc.Problems}
	if !scenario.ValidID(sc.ID) || sc.Version < 1 {
		return result, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	latest, err := c.db.LatestPublished(ctx, sc.ID)
	switch {
	case errors.Is(err, store.ErrScenarioNotFound):
	case err != nil:
		return Result{}, err
	case sc.Version <= latest.Version:
		result.Problems = append(result.Problems, versionTaken(sc, latest.Version))
		return result, nil
	}

	previous, err := c.db.GetScenario(ctx, sc.ID, sc.Version)
	switch {
	case err == nil && previous.Status == store.ScenarioPublished:
		// LatestPublished said otherwise a moment ago; never touch it.
		result.Problems = append(result.Problems, versionTaken(sc, sc.Version))
		return result, nil
	case err == nil:
		result.Replaced = true
	case !errors.Is(err, store.ErrScenarioNotFound):
		return Result{}, err
	}

	commit, rollback, err := c.moveInto(tmp, sc.ID, sc.Version)
	if err != nil {
		return Result{}, err
	}
	if err := c.db.PutScenario(ctx, sc); err != nil {
		rollback()
		return Result{}, err
	}
	commit()
	stored, err := c.db.GetScenario(ctx, sc.ID, sc.Version)
	if err != nil {
		return Result{}, err
	}
	result.Stored, result.Scenario = true, stored
	return result, nil
}

func versionTaken(sc store.Scenario, latest int) scenario.Problem {
	return scenario.Problem{
		Field:  "scenario.version",
		Code:   scenario.CodeVersionTaken,
		Detail: fmt.Sprintf("version %d of %s is published; the next upload needs version %d or higher", latest, sc.ID, latest+1),
	}
}

// moveInto renames the upload directory tmp to the directory of the
// version. A directory already there, a draft or what a crash left, is moved
// aside first. commit removes what was moved aside; rollback removes the new
// directory and puts the old one back.
func (c *Store) moveInto(tmp, id string, version int) (commit, rollback func(), err error) {
	final := c.versionDir(id, version)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, nil, err
	}
	aside := ""
	if _, err := os.Stat(final); err == nil {
		aside = filepath.Join(c.dir, incoming, "old-"+randomName())
		if err := os.Rename(final, aside); err != nil {
			return nil, nil, fmt.Errorf("move the old draft of %s version %d aside: %w", id, version, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	if err := os.Rename(tmp, final); err != nil {
		if aside != "" {
			os.Rename(aside, final)
		}
		return nil, nil, fmt.Errorf("move %s version %d into place: %w", id, version, err)
	}
	commit = func() {
		if aside != "" {
			os.RemoveAll(aside)
		}
	}
	rollback = func() {
		os.RemoveAll(final)
		if aside != "" {
			os.Rename(aside, final)
		} else {
			os.Remove(filepath.Dir(final))
		}
	}
	return commit, rollback, nil
}

// Publish publishes a draft; the package stays as it is.
func (c *Store) Publish(ctx context.Context, id string, version int) (store.Scenario, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.db.PublishScenario(ctx, id, version)
}

// Delete removes a draft and its package. A published version is refused.
func (c *Store) Delete(ctx context.Context, id string, version int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.db.DeleteScenario(ctx, id, version); err != nil {
		return err
	}
	if !scenario.ValidID(id) || version < 1 {
		return nil
	}
	dir := c.versionDir(id, version)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove %s: %w", dir, err)
	}
	os.Remove(filepath.Dir(dir))
	return nil
}

// Path returns the package file of a version for a download. It checks only
// that id and version can name a file; whether the version exists, and who
// may have it, is the index's to say.
func (c *Store) Path(id string, version int) (string, error) {
	if !scenario.ValidID(id) || version < 1 {
		return "", fmt.Errorf("%q version %d: %w", id, version, store.ErrScenarioNotFound)
	}
	return filepath.Join(c.versionDir(id, version), PackageName), nil
}

func (c *Store) versionDir(id string, version int) string {
	return filepath.Join(c.dir, id, strconv.Itoa(version))
}

// writeLimited copies r into a new file and refuses more than limit bytes.
func writeLimited(file string, r io.Reader, limit int64) (int64, error) {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return 0, fmt.Errorf("receive the package: %w", err)
	case n > limit:
		return 0, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}
	return n, nil
}

func randomName() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
