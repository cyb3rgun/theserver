package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Session states, as the CHECK constraint on sessions.state allows them. A
// session only ever moves forward: created, running, stopped.
const (
	SessionCreated = "created"
	SessionRunning = "running"
	SessionStopped = "stopped"
)

var (
	// ErrSessionNotFound is returned for a session id the store does not hold.
	ErrSessionNotFound = errors.New("session not found")
	// ErrBadTransition is a state change the session cannot make from where
	// it is, such as starting a stopped session.
	ErrBadTransition = errors.New("session state change not allowed")
	// ErrNoScenario refuses to start a session that plays nothing: a device
	// has to be told what to load, so a published version must be assigned
	// first (D-058).
	ErrNoScenario = errors.New("session has no published scenario assigned")
)

// A Session is one row of sessions with the ids of its devices. StartedAt and
// EndedAt are unix milliseconds and zero while the session has not started or
// not ended. Scenario and ScenarioVersion name the scenario version the
// session plays; the version is 0 while none is assigned.
type Session struct {
	ID              string
	Scenario        string
	ScenarioVersion int
	Room            string
	State           string
	StartedAt       int64
	EndedAt         int64
	CreatedAt       int64
	UpdatedAt       int64
	Devices         []string
}

// CreateSession stores a new session in state created. The id must be new.
func (s *Store) CreateSession(ctx context.Context, session Session) error {
	if session.ID == "" {
		return errors.New("session id must not be empty")
	}
	defer s.writing()()
	now := s.nowMilli()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (id, scenario, room, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		session.ID, session.Scenario, session.Room, SessionCreated, now, now)
	if err != nil {
		return fmt.Errorf("create session %s: %w", session.ID, err)
	}
	return nil
}

// StartSession moves a created session to running and stamps started_at. A session that plays no
// published scenario version is refused with ErrNoScenario (D-058).
func (s *Store) StartSession(ctx context.Context, id string) error {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if session.State == SessionCreated {
		if session.ScenarioVersion < 1 {
			return fmt.Errorf("session %s plays nothing: %w", id, ErrNoScenario)
		}
		sc, err := s.GetScenario(ctx, session.Scenario, session.ScenarioVersion)
		if err != nil {
			return fmt.Errorf("session %s: %w", id, err)
		}
		if sc.Status != ScenarioPublished {
			return fmt.Errorf("session %s plays %s version %d, which is a draft: %w",
				id, sc.ID, sc.Version, ErrNoScenario)
		}
	}
	return s.transition(ctx, id, SessionCreated, SessionRunning, "started_at")
}

// StopSession moves a running session to stopped and stamps ended_at.
func (s *Store) StopSession(ctx context.Context, id string) error {
	return s.transition(ctx, id, SessionRunning, SessionStopped, "ended_at")
}

// transition makes one state change in a single conditional update, so two
// callers cannot both succeed, and stamps the named time column.
func (s *Store) transition(ctx context.Context, id, from, to, stamp string) error {
	defer s.writing()()
	now := s.nowMilli()
	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET state = ?, `+stamp+` = ?, updated_at = ? WHERE id = ? AND state = ?`,
		to, now, now, id, from)
	if err != nil {
		return fmt.Errorf("session %s to %s: %w", id, to, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}

	var state string
	err = s.db.QueryRowContext(ctx, `SELECT state FROM sessions WHERE id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", id, ErrSessionNotFound)
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("session %s is %s, cannot move to %s: %w", id, state, to, ErrBadTransition)
}

// AddSessionDevice puts a device into a session. Adding it twice is not an
// error. Unknown sessions and devices are, and so is a device set for an age
// below the rating of the scenario the session plays (D-039).
func (s *Store) AddSessionDevice(ctx context.Context, sessionID, deviceID string) error {
	defer s.writing()()
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	device, err := s.GetDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	if err := checkSessionAge(ctx, s.db, session, device); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO session_devices (session_id, device_id) VALUES (?, ?)
ON CONFLICT(session_id, device_id) DO NOTHING`, sessionID, deviceID)
	if err != nil {
		return fmt.Errorf("add device %s to session %s: %w", deviceID, sessionID, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET updated_at = ? WHERE id = ?`, s.nowMilli(), sessionID); err != nil {
		return fmt.Errorf("add device %s to session %s: %w", deviceID, sessionID, err)
	}
	return nil
}

const sessionColumns = `id, scenario, room, state, started_at, ended_at, created_at, updated_at, scenario_version`

// GetSession reads one session with its devices, ordered by id.
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id)
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("%s: %w", id, ErrSessionNotFound)
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session %s: %w", id, err)
	}
	devices, err := s.sessionDevices(ctx, []string{id})
	if err != nil {
		return Session{}, err
	}
	session.Devices = devices[id]
	return session, nil
}

// ListSessions returns every session with its devices, oldest first.
func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM sessions ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	var ids []string
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		sessions = append(sessions, session)
		ids = append(ids, session.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	if len(ids) == 0 {
		return sessions, nil
	}

	devices, err := s.sessionDevices(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		sessions[i].Devices = devices[sessions[i].ID]
	}
	return sessions, nil
}

// RunningSessionFor returns the running session that holds the device, the
// latest started if there are several, for the welcome of the device link.
func (s *Store) RunningSessionFor(ctx context.Context, deviceID string) (Session, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+sessionColumns+`
  FROM sessions s
  JOIN session_devices sd ON sd.session_id = s.id
 WHERE sd.device_id = ? AND s.state = ?
 ORDER BY s.started_at DESC, s.id
 LIMIT 1`, deviceID, SessionRunning)
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("running session of %s: %w", deviceID, err)
	}
	return session, true, nil
}

func (s *Store) sessionDevices(ctx context.Context, sessionIDs []string) (map[string][]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(sessionIDs)), ", ")
	args := make([]any, len(sessionIDs))
	for i, id := range sessionIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id, device_id FROM session_devices WHERE session_id IN (`+placeholders+`)
		 ORDER BY session_id, device_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("session devices: %w", err)
	}
	defer rows.Close()

	devices := map[string][]string{}
	for rows.Next() {
		var sessionID, deviceID string
		if err := rows.Scan(&sessionID, &deviceID); err != nil {
			return nil, err
		}
		devices[sessionID] = append(devices[sessionID], deviceID)
	}
	return devices, rows.Err()
}

func scanSession(row rowScanner) (Session, error) {
	var (
		session          Session
		started, stopped sql.NullInt64
	)
	err := row.Scan(&session.ID, &session.Scenario, &session.Room, &session.State,
		&started, &stopped, &session.CreatedAt, &session.UpdatedAt, &session.ScenarioVersion)
	if err != nil {
		return Session{}, err
	}
	session.StartedAt = started.Int64
	session.EndedAt = stopped.Int64
	return session, nil
}
