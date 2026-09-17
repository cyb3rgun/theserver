package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// The states of a scenario version (D-037).
const (
	ScenarioDraft     = "draft"
	ScenarioPublished = "published"
)

var (
	// ErrScenarioNotFound is returned for a scenario version the index does
	// not hold.
	ErrScenarioNotFound = errors.New("scenario not found")
	// ErrVersionTaken refuses a version at or below the latest published
	// version of its scenario, which can be neither stored nor published.
	ErrVersionTaken = errors.New("scenario version is taken by a published one")
	// ErrPublished refuses to change or delete a published version.
	ErrPublished = errors.New("scenario version is published")
	// ErrHasProblems refuses to publish a draft that did not validate.
	ErrHasProblems = errors.New("scenario draft has problems")
)

// A Scenario is one version in the index of scenario packages (D-036).
// UploadedAt and PublishedAt are unix milliseconds; PublishedAt is zero for
// a draft.
type Scenario struct {
	ID           string
	Version      int
	Tier         string
	Title        scenario.Text
	AgeRating    string
	ManifestHash string
	Size         int64
	UploadedAt   int64
	UploadedBy   string
	Status       string
	PublishedAt  int64
	// Problems are the validation problems of a draft; a version with
	// problems cannot be published.
	Problems []scenario.Problem
}

// PutScenario stores the index row of an uploaded version as a draft. A
// draft of the same version is replaced. A version at or below the latest
// published one is refused with ErrVersionTaken (D-037).
func (s *Store) PutScenario(ctx context.Context, sc Scenario) error {
	if !scenario.ValidID(sc.ID) || sc.Version < 1 {
		return fmt.Errorf("put scenario %q version %d: not a usable id and version", sc.ID, sc.Version)
	}
	title, err := json.Marshal(nonNilText(sc.Title))
	if err != nil {
		return err
	}
	problems, err := json.Marshal(nonNilProblems(sc.Problems))
	if err != nil {
		return err
	}
	defer s.writing()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	latest, err := latestPublished(ctx, tx, sc.ID)
	if err != nil {
		return err
	}
	if sc.Version <= latest {
		return fmt.Errorf("%s version %d, latest published %d: %w", sc.ID, sc.Version, latest, ErrVersionTaken)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO scenarios (
  id, version, tier, title, age_rating, manifest_hash, size, uploaded_at, uploaded_by, status, problems
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'draft', ?)
ON CONFLICT (id, version) DO UPDATE SET
  tier          = excluded.tier,
  title         = excluded.title,
  age_rating    = excluded.age_rating,
  manifest_hash = excluded.manifest_hash,
  size          = excluded.size,
  uploaded_at   = excluded.uploaded_at,
  uploaded_by   = excluded.uploaded_by,
  problems      = excluded.problems
WHERE scenarios.status = 'draft'`,
		sc.ID, sc.Version, sc.Tier, string(title), sc.AgeRating, sc.ManifestHash, sc.Size,
		s.nowMilli(), sc.UploadedBy, string(problems))
	if err != nil {
		return fmt.Errorf("put scenario %s version %d: %w", sc.ID, sc.Version, err)
	}
	return tx.Commit()
}

// PublishScenario turns a draft into a published version and returns it. A
// published version gives ErrPublished, a draft with problems
// ErrHasProblems, a draft at or below the latest published version
// ErrVersionTaken.
func (s *Store) PublishScenario(ctx context.Context, id string, version int) (Scenario, error) {
	defer s.writing()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Scenario{}, err
	}
	defer tx.Rollback()
	sc, err := getScenario(ctx, tx, id, version)
	if err != nil {
		return Scenario{}, err
	}
	switch {
	case sc.Status == ScenarioPublished:
		return Scenario{}, fmt.Errorf("%s version %d: %w", id, version, ErrPublished)
	case len(sc.Problems) > 0:
		return Scenario{}, fmt.Errorf("%s version %d has %d problem(s): %w", id, version, len(sc.Problems), ErrHasProblems)
	}
	latest, err := latestPublished(ctx, tx, id)
	if err != nil {
		return Scenario{}, err
	}
	if version <= latest {
		return Scenario{}, fmt.Errorf("%s version %d, latest published %d: %w", id, version, latest, ErrVersionTaken)
	}
	now := s.nowMilli()
	if _, err := tx.ExecContext(ctx,
		`UPDATE scenarios SET status = 'published', published_at = ? WHERE id = ? AND version = ? AND status = 'draft'`,
		now, id, version); err != nil {
		return Scenario{}, fmt.Errorf("publish %s version %d: %w", id, version, err)
	}
	if err := tx.Commit(); err != nil {
		return Scenario{}, err
	}
	sc.Status, sc.PublishedAt = ScenarioPublished, now
	return sc, nil
}

// DeleteScenario removes a draft from the index. A published version is
// refused with ErrPublished.
func (s *Store) DeleteScenario(ctx context.Context, id string, version int) error {
	defer s.writing()()
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM scenarios WHERE id = ? AND version = ? AND status = 'draft'`, id, version)
	if err != nil {
		return fmt.Errorf("delete %s version %d: %w", id, version, err)
	}
	if n, err := result.RowsAffected(); err != nil || n == 1 {
		return err
	}
	if _, err := getScenario(ctx, s.db, id, version); err != nil {
		return err
	}
	return fmt.Errorf("%s version %d: %w", id, version, ErrPublished)
}

// GetScenario reads one version.
func (s *Store) GetScenario(ctx context.Context, id string, version int) (Scenario, error) {
	return getScenario(ctx, s.db, id, version)
}

// LatestPublished returns the highest published version of id, or
// ErrScenarioNotFound when none is published.
func (s *Store) LatestPublished(ctx context.Context, id string) (Scenario, error) {
	latest, err := latestPublished(ctx, s.db, id)
	if err != nil {
		return Scenario{}, err
	}
	if latest == 0 {
		return Scenario{}, fmt.Errorf("%s has no published version: %w", id, ErrScenarioNotFound)
	}
	return getScenario(ctx, s.db, id, latest)
}

// ListScenarios returns every version of every scenario, by id and then by
// version, newest first.
func (s *Store) ListScenarios(ctx context.Context) ([]Scenario, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scenarioColumns+` FROM scenarios ORDER BY id, version DESC`)
	if err != nil {
		return nil, fmt.Errorf("list scenarios: %w", err)
	}
	defer rows.Close()
	var list []Scenario
	for rows.Next() {
		sc, err := scanScenario(rows)
		if err != nil {
			return nil, fmt.Errorf("list scenarios: %w", err)
		}
		list = append(list, sc)
	}
	return list, rows.Err()
}

const scenarioColumns = `id, version, tier, title, age_rating, manifest_hash, size,
  uploaded_at, uploaded_by, status, published_at, problems`

func getScenario(ctx context.Context, q querier, id string, version int) (Scenario, error) {
	row := q.QueryRowContext(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE id = ? AND version = ?`, id, version)
	sc, err := scanScenario(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Scenario{}, fmt.Errorf("%s version %d: %w", id, version, ErrScenarioNotFound)
	}
	if err != nil {
		return Scenario{}, fmt.Errorf("get scenario %s version %d: %w", id, version, err)
	}
	return sc, nil
}

func latestPublished(ctx context.Context, q querier, id string) (int, error) {
	var latest sql.NullInt64
	err := q.QueryRowContext(ctx,
		`SELECT MAX(version) FROM scenarios WHERE id = ? AND status = 'published'`, id).Scan(&latest)
	if err != nil {
		return 0, fmt.Errorf("latest published version of %s: %w", id, err)
	}
	return int(latest.Int64), nil
}

func scanScenario(row rowScanner) (Scenario, error) {
	var (
		sc              Scenario
		title, problems string
		publishedAt     sql.NullInt64
	)
	err := row.Scan(&sc.ID, &sc.Version, &sc.Tier, &title, &sc.AgeRating, &sc.ManifestHash, &sc.Size,
		&sc.UploadedAt, &sc.UploadedBy, &sc.Status, &publishedAt, &problems)
	if err != nil {
		return Scenario{}, err
	}
	sc.PublishedAt = publishedAt.Int64
	if err := json.Unmarshal([]byte(title), &sc.Title); err != nil {
		return Scenario{}, fmt.Errorf("title of %s version %d: %w", sc.ID, sc.Version, err)
	}
	if err := json.Unmarshal([]byte(problems), &sc.Problems); err != nil {
		return Scenario{}, fmt.Errorf("problems of %s version %d: %w", sc.ID, sc.Version, err)
	}
	if len(sc.Problems) == 0 {
		sc.Problems = nil
	}
	return sc, nil
}

func nonNilText(t scenario.Text) scenario.Text {
	if t == nil {
		return scenario.Text{}
	}
	return t
}

func nonNilProblems(p []scenario.Problem) []scenario.Problem {
	if p == nil {
		return []scenario.Problem{}
	}
	return p
}

// A Holding is one scenario version a device holds or held (D-038).
// InstalledAt is when it last became current, in unix milliseconds.
type Holding struct {
	DeviceID    string
	ScenarioID  string
	Version     int
	InstalledAt int64
	Current     bool
}

// RecordDeviceScenarios records the report of a device of what it holds
// now: each version in holdings is current, every other version it held
// before is not. Only ScenarioID and Version of holdings are read. A version
// that becomes current is stamped with the time of the report.
func (s *Store) RecordDeviceScenarios(ctx context.Context, deviceID string, holdings []Holding) error {
	defer s.writing()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.nowMilli()
	type key struct {
		id      string
		version int
	}
	held := map[key]bool{}
	for _, h := range holdings {
		held[key{h.ScenarioID, h.Version}] = true
		if err := holdLocked(ctx, tx, deviceID, h.ScenarioID, h.Version, now); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT scenario_id, version FROM device_scenarios WHERE device_id = ? AND current = 1`, deviceID)
	if err != nil {
		return fmt.Errorf("holdings of %s: %w", deviceID, err)
	}
	var gone []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.id, &k.version); err != nil {
			rows.Close()
			return err
		}
		if !held[k] {
			gone = append(gone, k)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range gone {
		if _, err := tx.ExecContext(ctx,
			`UPDATE device_scenarios SET current = 0 WHERE device_id = ? AND scenario_id = ? AND version = ?`,
			deviceID, k.id, k.version); err != nil {
			return fmt.Errorf("holdings of %s: %w", deviceID, err)
		}
	}
	return tx.Commit()
}

// SetDeviceScenario records that a device installed a version, current, or
// removed it, not current, and leaves its other versions alone.
func (s *Store) SetDeviceScenario(ctx context.Context, deviceID, scenarioID string, version int, current bool) error {
	defer s.writing()()
	if current {
		return holdLocked(ctx, s.db, deviceID, scenarioID, version, s.nowMilli())
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE device_scenarios SET current = 0 WHERE device_id = ? AND scenario_id = ? AND version = ?`,
		deviceID, scenarioID, version)
	if err != nil {
		return fmt.Errorf("device %s removed %s version %d: %w", deviceID, scenarioID, version, err)
	}
	return nil
}

// execer is the part of *sql.DB and *sql.Tx the writes need.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func holdLocked(ctx context.Context, e execer, deviceID, scenarioID string, version int, now int64) error {
	_, err := e.ExecContext(ctx, `
INSERT INTO device_scenarios (device_id, scenario_id, version, installed_at, current)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT (device_id, scenario_id, version) DO UPDATE SET
  installed_at = CASE WHEN device_scenarios.current = 1 THEN device_scenarios.installed_at ELSE excluded.installed_at END,
  current      = 1`, deviceID, scenarioID, version, now)
	if err != nil {
		return fmt.Errorf("device %s holds %s version %d: %w", deviceID, scenarioID, version, err)
	}
	return nil
}

// DeviceScenarios lists every version a device holds or held, by scenario
// and then by version, newest first.
func (s *Store) DeviceScenarios(ctx context.Context, deviceID string) ([]Holding, error) {
	return s.holdings(ctx, `WHERE device_id = ? ORDER BY scenario_id, version DESC`, deviceID)
}

// ScenarioHoldings lists every device that holds or held a version of the
// scenario id, by device and then by version, newest first.
func (s *Store) ScenarioHoldings(ctx context.Context, id string) ([]Holding, error) {
	return s.holdings(ctx, `WHERE scenario_id = ? ORDER BY device_id, version DESC`, id)
}

// HoldsScenario reports whether a device holds a version now.
func (s *Store) HoldsScenario(ctx context.Context, deviceID, scenarioID string, version int) (bool, error) {
	holdings, err := s.DeviceScenarios(ctx, deviceID)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(holdings, func(h Holding) bool {
		return h.Current && h.ScenarioID == scenarioID && h.Version == version
	}), nil
}

func (s *Store) holdings(ctx context.Context, where string, arg any) ([]Holding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT device_id, scenario_id, version, installed_at, current FROM device_scenarios `+where, arg)
	if err != nil {
		return nil, fmt.Errorf("holdings: %w", err)
	}
	defer rows.Close()
	var list []Holding
	for rows.Next() {
		var h Holding
		if err := rows.Scan(&h.DeviceID, &h.ScenarioID, &h.Version, &h.InstalledAt, &h.Current); err != nil {
			return nil, fmt.Errorf("holdings: %w", err)
		}
		list = append(list, h)
	}
	return list, rows.Err()
}
