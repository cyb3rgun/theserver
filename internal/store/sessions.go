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
)

// A Session is one row of sessions with the ids of its devices. StartedAt and
// EndedAt are unix milliseconds and zero while the session has not started or
// not ended.
type Session struct {
	ID        string
	Scenario  string
	Room      string
	State     string
	StartedAt int64
	EndedAt   int64
	CreatedAt int64
	UpdatedAt int64
	Devices   []string
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

// StartSession moves a created session to running and stamps started_at.
func (s *Store) StartSession(ctx context.Context, id string) error {
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
// error. Unknown sessions and devices are.
func (s *Store) AddSessionDevice(ctx context.Context, sessionID, deviceID string) error {
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		return err
	}
	if _, err := s.GetDevice(ctx, deviceID); err != nil {
		return err
	}
	defer s.writing()()
	_, err := s.db.ExecContext(ctx, `
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

const sessionColumns = `id, scenario, room, state, started_at, ended_at, created_at, updated_at`

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
func (s *Store) RunningSessionFor(ctx context.Context, deviceID string) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
SELECT s.id
  FROM sessions s
  JOIN session_devices sd ON sd.session_id = s.id
 WHERE sd.device_id = ? AND s.state = ?
 ORDER BY s.started_at DESC, s.id
 LIMIT 1`, deviceID, SessionRunning).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("running session of %s: %w", deviceID, err)
	}
	return id, true, nil
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
		&started, &stopped, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		return Session{}, err
	}
	session.StartedAt = started.Int64
	session.EndedAt = stopped.Int64
	return session, nil
}
