package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
)

// ErrDraftNotFound is returned for an editor draft the store does not hold.
var ErrDraftNotFound = errors.New("scenario draft not found")

// A Draft is the working copy of one scenario in the editor (D-041).
// Manifest is the manifest as JSON, in the shape of scenario.Manifest;
// Media says what the server knows about every file in the media directory
// of the draft. Times are unix milliseconds.
type Draft struct {
	ID               string
	ScenarioID       string
	Manifest         json.RawMessage
	Media            map[string]DraftMedia
	CreatedAt        int64
	CreatedBy        string
	UpdatedAt        int64
	UpdatedBy        string
	PublishedVersion int
	Lock             DraftLock
}

// A DraftLock says who is editing a draft right now (D-050). By is the id of
// the admin token that holds it, Name the name that token carried when the
// lock was taken, At the last refresh in unix milliseconds. A zero By is a
// draft nobody holds.
type DraftLock struct {
	By   string
	Name string
	At   int64
}

// Held says the lock was taken and its last refresh is not older than ttl.
// A lock that stopped being refreshed, because a browser was closed or a
// machine went to sleep, falls away by itself and the next person walks in
// without a takeover.
func (l DraftLock) Held(now int64, ttl time.Duration) bool {
	return l.By != "" && now-l.At < ttl.Milliseconds()
}

// DraftMedia is one file in the media directory of a draft. Container and
// Codec are what the header said at upload (D-045); DurationMs, Width and
// Height are what the browser measured, zero until it says.
type DraftMedia struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Container  string `json:"container"`
	Codec      string `json:"codec"`
	DurationMs int    `json:"duration_ms,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	UploadedAt int64  `json:"uploaded_at"`
}

// MediaNames lists the media files of a draft by name.
func (d Draft) MediaNames() []string {
	return slices.Sorted(maps.Keys(d.Media))
}

// CreateDraft writes a new draft. The caller gives the id and the directory
// is its own to make.
func (s *Store) CreateDraft(ctx context.Context, d Draft) (Draft, error) {
	manifest, media, err := draftJSON(d)
	if err != nil {
		return Draft{}, err
	}
	defer s.writing()()
	now := s.nowMilli()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO scenario_drafts (id, scenario_id, manifest, media, created_at, created_by, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.ScenarioID, manifest, media, now, d.CreatedBy, now, d.CreatedBy)
	if err != nil {
		return Draft{}, fmt.Errorf("create draft %s: %w", d.ID, err)
	}
	return getDraft(ctx, s.db, d.ID)
}

// UpdateDraft writes the manifest and the media of a draft again and stamps
// it with by.
func (s *Store) UpdateDraft(ctx context.Context, d Draft, by string) (Draft, error) {
	manifest, media, err := draftJSON(d)
	if err != nil {
		return Draft{}, err
	}
	defer s.writing()()
	result, err := s.db.ExecContext(ctx,
		`UPDATE scenario_drafts SET manifest = ?, media = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		manifest, media, s.nowMilli(), by, d.ID)
	if err != nil {
		return Draft{}, fmt.Errorf("update draft %s: %w", d.ID, err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return Draft{}, err
	} else if n == 0 {
		return Draft{}, fmt.Errorf("%s: %w", d.ID, ErrDraftNotFound)
	}
	return getDraft(ctx, s.db, d.ID)
}

// PromoteDraft records the version a draft published (D-042). The draft
// stays, so the next change of it becomes the next version.
func (s *Store) PromoteDraft(ctx context.Context, id string, version int, by string) (Draft, error) {
	defer s.writing()()
	result, err := s.db.ExecContext(ctx,
		`UPDATE scenario_drafts SET published_version = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		version, s.nowMilli(), by, id)
	if err != nil {
		return Draft{}, fmt.Errorf("promote draft %s: %w", id, err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return Draft{}, err
	} else if n == 0 {
		return Draft{}, fmt.Errorf("%s: %w", id, ErrDraftNotFound)
	}
	return getDraft(ctx, s.db, id)
}

// GetDraft reads one draft.
func (s *Store) GetDraft(ctx context.Context, id string) (Draft, error) {
	return getDraft(ctx, s.db, id)
}

// ListDrafts returns every draft, the one changed last first.
func (s *Store) ListDrafts(ctx context.Context) ([]Draft, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+draftColumns+` FROM scenario_drafts ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list drafts: %w", err)
	}
	defer rows.Close()
	var list []Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, fmt.Errorf("list drafts: %w", err)
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// ErrDraftLocked is returned when a draft is held by somebody else and the
// caller did not ask to take it over.
var ErrDraftLocked = errors.New("scenario draft is locked")

// LockDraft takes or refreshes the lock of a draft for the admin token by
// with the name name (D-050). A draft nobody holds, a draft this token
// already holds, and a draft whose lock has not been refreshed within ttl
// are taken without a word; a draft somebody else holds gives ErrDraftLocked
// with the holder in the returned lock, unless takeOver says to walk in.
func (s *Store) LockDraft(ctx context.Context, id, by, name string, ttl time.Duration, takeOver bool) (Draft, DraftLock, error) {
	if by == "" {
		return Draft{}, DraftLock{}, errors.New("a draft lock needs an admin token")
	}
	defer s.writing()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, DraftLock{}, err
	}
	defer tx.Rollback()
	d, err := getDraft(ctx, tx, id)
	if err != nil {
		return Draft{}, DraftLock{}, err
	}
	now := s.nowMilli()
	// The holder is only in the way while somebody else holds a lock that is
	// still being refreshed.
	if held := d.Lock; held.By != by && held.Held(now, ttl) && !takeOver {
		return d, held, fmt.Errorf("%s is held by %s: %w", id, held.Name, ErrDraftLocked)
	}
	taken := DraftLock{By: by, Name: name, At: now}
	if _, err := tx.ExecContext(ctx,
		`UPDATE scenario_drafts SET locked_by = ?, locked_name = ?, locked_at = ? WHERE id = ?`,
		taken.By, taken.Name, taken.At, id); err != nil {
		return Draft{}, DraftLock{}, fmt.Errorf("lock draft %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return Draft{}, DraftLock{}, err
	}
	d.Lock = taken
	return d, taken, nil
}

// UnlockDraft releases the lock of a draft when by holds it. Releasing a
// draft somebody else holds changes nothing, so a browser that leaves late
// cannot open the draft of the person who took it over.
func (s *Store) UnlockDraft(ctx context.Context, id, by string) (Draft, error) {
	defer s.writing()()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE scenario_drafts SET locked_by = '', locked_name = '', locked_at = 0 WHERE id = ? AND locked_by = ?`,
		id, by); err != nil {
		return Draft{}, fmt.Errorf("unlock draft %s: %w", id, err)
	}
	return getDraft(ctx, s.db, id)
}

// DeleteDraft removes a draft row; the media directory is the caller's to
// remove.
func (s *Store) DeleteDraft(ctx context.Context, id string) error {
	defer s.writing()()
	result, err := s.db.ExecContext(ctx, `DELETE FROM scenario_drafts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete draft %s: %w", id, err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("%s: %w", id, ErrDraftNotFound)
	}
	return nil
}

const draftColumns = `id, scenario_id, manifest, media, created_at, created_by, updated_at, updated_by, published_version, locked_by, locked_name, locked_at`

func getDraft(ctx context.Context, q querier, id string) (Draft, error) {
	row := q.QueryRowContext(ctx, `SELECT `+draftColumns+` FROM scenario_drafts WHERE id = ?`, id)
	d, err := scanDraft(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, fmt.Errorf("%s: %w", id, ErrDraftNotFound)
	}
	if err != nil {
		return Draft{}, fmt.Errorf("get draft %s: %w", id, err)
	}
	return d, nil
}

func scanDraft(row rowScanner) (Draft, error) {
	var (
		d               Draft
		manifest, media string
	)
	err := row.Scan(&d.ID, &d.ScenarioID, &manifest, &media, &d.CreatedAt, &d.CreatedBy,
		&d.UpdatedAt, &d.UpdatedBy, &d.PublishedVersion, &d.Lock.By, &d.Lock.Name, &d.Lock.At)
	if err != nil {
		return Draft{}, err
	}
	d.Manifest = json.RawMessage(manifest)
	if err := json.Unmarshal([]byte(media), &d.Media); err != nil {
		return Draft{}, fmt.Errorf("media of draft %s: %w", d.ID, err)
	}
	if d.Media == nil {
		d.Media = map[string]DraftMedia{}
	}
	return d, nil
}

func draftJSON(d Draft) (manifest, media string, err error) {
	manifest = string(d.Manifest)
	if manifest == "" {
		manifest = "{}"
	}
	if !json.Valid([]byte(manifest)) {
		return "", "", fmt.Errorf("draft %s: the manifest is not JSON", d.ID)
	}
	files := d.Media
	if files == nil {
		files = map[string]DraftMedia{}
	}
	raw, err := json.Marshal(files)
	if err != nil {
		return "", "", err
	}
	return manifest, string(raw), nil
}
