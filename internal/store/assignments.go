package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

var (
	// ErrNotPublished refuses to assign a draft.
	ErrNotPublished = errors.New("scenario version is not published")
	// ErrAgeRating refuses a scenario rated above the age a device of the
	// session is set for (D-039); an *AgeError wraps it.
	ErrAgeRating = errors.New("scenario is rated above the age set for a device")
)

// AgeError names a scenario version and the devices it is rated too high
// for.
type AgeError struct {
	ScenarioID string
	Version    int
	Rating     int
	Devices    []DeviceAge
}

// DeviceAge is a device with the age it is set for.
type DeviceAge struct {
	DeviceID string
	MinAge   int
}

func (e *AgeError) Error() string {
	parts := make([]string, len(e.Devices))
	for i, d := range e.Devices {
		parts[i] = fmt.Sprintf("device %s is set for %d", d.DeviceID, d.MinAge)
	}
	return fmt.Sprintf("%s version %d is rated %d, but %s", e.ScenarioID, e.Version, e.Rating, strings.Join(parts, " and "))
}

func (e *AgeError) Unwrap() error {
	return ErrAgeRating
}

// MinAges are the ages a device can be set for, the steps of the age
// ratings.
func MinAges() []int {
	ratings := scenario.AgeRatings()
	ages := make([]int, len(ratings))
	for i, r := range ratings {
		ages[i], _ = strconv.Atoi(r)
	}
	return ages
}

// ratingOf reads the age rating of a scenario version.
func ratingOf(sc Scenario) (int, error) {
	rating, err := strconv.Atoi(sc.AgeRating)
	if err != nil {
		return 0, fmt.Errorf("%s version %d has the age rating %q: %w", sc.ID, sc.Version, sc.AgeRating, err)
	}
	return rating, nil
}

// SetMinAge sets the age a device is set for. An age that is not one of
// MinAges is refused, and so is an age below the rating of a scenario that a
// created or running session of the device plays.
func (s *Store) SetMinAge(ctx context.Context, id string, age int) error {
	if !slices.Contains(MinAges(), age) {
		return fmt.Errorf("the age %d is not one of %v", age, MinAges())
	}
	defer s.writing()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT `+prefixed("sc.", scenarioColumns)+`
  FROM sessions se
  JOIN session_devices sd ON sd.session_id = se.id
  JOIN scenarios sc ON sc.id = se.scenario AND sc.version = se.scenario_version
 WHERE sd.device_id = ? AND se.state IN ('created', 'running') AND se.scenario_version > 0`, id)
	if err != nil {
		return fmt.Errorf("min age of %s: %w", id, err)
	}
	var playing []Scenario
	for rows.Next() {
		sc, err := scanScenario(rows)
		if err != nil {
			rows.Close()
			return err
		}
		playing = append(playing, sc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sc := range playing {
		rating, err := ratingOf(sc)
		if err != nil {
			return err
		}
		if rating > age {
			return &AgeError{ScenarioID: sc.ID, Version: sc.Version, Rating: rating, Devices: []DeviceAge{{id, age}}}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE devices SET min_age = ?, updated_at = ? WHERE id = ?`, age, s.nowMilli(), id)
	if err != nil {
		return fmt.Errorf("min age of %s: %w", id, err)
	}
	if err := checkOneRow(result, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AssignScenario sets the scenario version a session plays. The session has
// to be created, the version published, and no device of the session may be
// set for an age below its rating (D-039).
func (s *Store) AssignScenario(ctx context.Context, sessionID, scenarioID string, version int) error {
	defer s.writing()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM sessions WHERE id = ?`, sessionID).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%s: %w", sessionID, ErrSessionNotFound)
	case err != nil:
		return fmt.Errorf("assign to session %s: %w", sessionID, err)
	case state != SessionCreated:
		return fmt.Errorf("session %s is %s; a scenario is assigned while it is created: %w", sessionID, state, ErrBadTransition)
	}
	sc, err := getScenario(ctx, tx, scenarioID, version)
	if err != nil {
		return err
	}
	if sc.Status != ScenarioPublished {
		return fmt.Errorf("%s version %d is a draft: %w", scenarioID, version, ErrNotPublished)
	}
	rating, err := ratingOf(sc)
	if err != nil {
		return err
	}
	if err := checkRoomAge(ctx, tx, sessionID, sc, rating); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT d.id, d.min_age
  FROM session_devices sd
  JOIN devices d ON d.id = sd.device_id
 WHERE sd.session_id = ? AND d.min_age < ?
 ORDER BY d.id`, sessionID, rating)
	if err != nil {
		return fmt.Errorf("assign to session %s: %w", sessionID, err)
	}
	var young []DeviceAge
	for rows.Next() {
		var d DeviceAge
		if err := rows.Scan(&d.DeviceID, &d.MinAge); err != nil {
			rows.Close()
			return err
		}
		young = append(young, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(young) > 0 {
		return &AgeError{ScenarioID: scenarioID, Version: version, Rating: rating, Devices: young}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET scenario = ?, scenario_version = ?, updated_at = ? WHERE id = ?`,
		scenarioID, version, s.nowMilli(), sessionID); err != nil {
		return fmt.Errorf("assign to session %s: %w", sessionID, err)
	}
	return tx.Commit()
}

// RoomAgeError is a scenario version rated above the age rating of the room
// the session runs in (D-067).
type RoomAgeError struct {
	ScenarioID string
	Version    int
	Rating     int
	RoomID     string
	RoomName   string
	RoomRating int
}

func (e *RoomAgeError) Error() string {
	return fmt.Sprintf("%s version %d is rated %d, but room %s is rated %d",
		e.ScenarioID, e.Version, e.Rating, e.RoomName, e.RoomRating)
}

func (e *RoomAgeError) Unwrap() error {
	return ErrAgeRating
}

// checkRoomAge refuses a scenario rated above the age rating of the room the
// session runs in. A session without a room is not checked (D-067).
func checkRoomAge(ctx context.Context, q querier, sessionID string, sc Scenario, rating int) error {
	var (
		roomID, name, roomRating string
	)
	err := q.QueryRowContext(ctx, `
SELECT r.id, r.name, r.age_rating
  FROM sessions s
  JOIN rooms r ON r.id = s.room_id
 WHERE s.id = ?`, sessionID).Scan(&roomID, &name, &roomRating)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("room of session %s: %w", sessionID, err)
	}
	allowed, err := strconv.Atoi(roomRating)
	if err != nil {
		return fmt.Errorf("age rating of room %s: %w", roomID, err)
	}
	if rating > allowed {
		return &RoomAgeError{
			ScenarioID: sc.ID, Version: sc.Version, Rating: rating,
			RoomID: roomID, RoomName: name, RoomRating: allowed,
		}
	}
	return nil
}

// checkSessionAge refuses a device for a session whose scenario is rated
// above the age the device is set for.
func checkSessionAge(ctx context.Context, q querier, session Session, device Device) error {
	if session.ScenarioVersion == 0 {
		return nil
	}
	sc, err := getScenario(ctx, q, session.Scenario, session.ScenarioVersion)
	if err != nil {
		return err
	}
	rating, err := ratingOf(sc)
	if err != nil {
		return err
	}
	if device.MinAge < rating {
		return &AgeError{ScenarioID: sc.ID, Version: sc.Version, Rating: rating, Devices: []DeviceAge{{device.ID, device.MinAge}}}
	}
	return nil
}

// A Pending is a scenario version a device should hold for a session it is
// in, and does not hold now.
type Pending struct {
	DeviceID  string
	SessionID string
	Scenario  Scenario
}

// PendingContent lists what devices should fetch (D-038): for every session
// that is created or running and plays a scenario version, each of its
// devices that does not hold that version now. deviceID and sessionID narrow
// the list when they are not empty. The list is ordered by device and
// session.
func (s *Store) PendingContent(ctx context.Context, deviceID, sessionID string) ([]Pending, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT sd.device_id, se.id, `+prefixed("sc.", scenarioColumns)+`
  FROM sessions se
  JOIN session_devices sd ON sd.session_id = se.id
  JOIN scenarios sc ON sc.id = se.scenario AND sc.version = se.scenario_version
 WHERE se.state IN ('created', 'running') AND se.scenario_version > 0
   AND (? = '' OR sd.device_id = ?)
   AND (? = '' OR se.id = ?)
   AND NOT EXISTS (
     SELECT 1 FROM device_scenarios ds
      WHERE ds.device_id = sd.device_id AND ds.scenario_id = se.scenario
        AND ds.version = se.scenario_version AND ds.current = 1)
 ORDER BY sd.device_id, se.id`, deviceID, deviceID, sessionID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("pending content: %w", err)
	}
	defer rows.Close()
	var list []Pending
	for rows.Next() {
		var p Pending
		sc, err := scanScenario(prefixedScanner{rows, []any{&p.DeviceID, &p.SessionID}})
		if err != nil {
			return nil, fmt.Errorf("pending content: %w", err)
		}
		p.Scenario = sc
		list = append(list, p)
	}
	return list, rows.Err()
}

// prefixedScanner scans leading columns into before and the rest through the
// scanner of the caller.
type prefixedScanner struct {
	rows   rowScanner
	before []any
}

func (p prefixedScanner) Scan(dest ...any) error {
	return p.rows.Scan(append(append([]any{}, p.before...), dest...)...)
}

// prefixed puts a table alias before every column of a column list.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = alias + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}
