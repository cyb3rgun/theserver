package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cyb3rgun/theserver/internal/store"
)

// maxBody bounds a JSON request body.
const maxBody = 64 << 10

// Session is a session as API v1 shows it. Scenario and ScenarioVersion
// name the scenario version it plays; the version is 0 while none is
// assigned, and Scenario is then a label given at creation.
type Session struct {
	ID              string   `json:"id"`
	Scenario        string   `json:"scenario"`
	ScenarioVersion int      `json:"scenario_version"`
	Room            string   `json:"room"`
	State           string   `json:"state"`
	StartedAt       int64    `json:"started_at"`
	EndedAt         int64    `json:"ended_at"`
	CreatedAt       int64    `json:"created_at"`
	UpdatedAt       int64    `json:"updated_at"`
	Devices         []string `json:"devices"`
}

// NewSession is the body of POST /sessions. An empty ID is chosen by the
// server.
type NewSession struct {
	ID       string `json:"id"`
	Scenario string `json:"scenario"`
	Room     string `json:"room"`
}

// SessionDevice is the body of POST /sessions/{id}/devices.
type SessionDevice struct {
	DeviceID string `json:"device_id"`
}

func sessionJSON(s store.Session) Session {
	devices := s.Devices
	if devices == nil {
		devices = []string{}
	}
	return Session{
		ID: s.ID, Scenario: s.Scenario, ScenarioVersion: s.ScenarioVersion, Room: s.Room, State: s.State,
		StartedAt: s.StartedAt, EndedAt: s.EndedAt, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		Devices: devices,
	}
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.opts.Store.ListSessions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, sessionJSON(session))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, id string, status int) {
	session, err := s.opts.Store.GetSession(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, sessionJSON(session))
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var body NewSession
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	body.ID = strings.TrimSpace(body.ID)
	if body.ID == "" {
		id, err := newSessionID()
		if err != nil {
			s.fail(w, r, err)
			return
		}
		body.ID = id
	} else if !validID(body.ID) {
		s.fail(w, r, fmt.Errorf("%w: a session id may hold letters, digits, dot, dash and underscore, up to 64", errBadRequest))
		return
	}

	ctx := r.Context()
	if _, err := s.opts.Store.GetSession(ctx, body.ID); err == nil {
		s.fail(w, r, fmt.Errorf("%w: session %s exists", errConflict, body.ID))
		return
	} else if !errors.Is(err, store.ErrSessionNotFound) {
		s.fail(w, r, err)
		return
	}
	err := s.opts.Store.CreateSession(ctx, store.Session{ID: body.ID, Scenario: body.Scenario, Room: body.Room})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "session created", "session", body.ID)
	w.Header().Set("Location", Prefix+"/sessions/"+body.ID)
	s.writeSession(w, r, body.ID, http.StatusCreated)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Store.StartSession(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "session started", "session", id)
	s.writeSession(w, r, id, http.StatusOK)
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Store.StopSession(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "session stopped", "session", id)
	s.writeSession(w, r, id, http.StatusOK)
}

func (s *Server) addSessionDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body SessionDevice
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	if body.DeviceID == "" {
		s.fail(w, r, fmt.Errorf("%w: device_id is required", errBadRequest))
		return
	}
	if err := s.opts.Store.AddSessionDevice(r.Context(), id, body.DeviceID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "device added to session", "session", id, "device", body.DeviceID)
	s.announce(r, body.DeviceID, id)
	s.writeSession(w, r, id, http.StatusOK)
}

// decodeBody reads one JSON object and refuses unknown fields.
func decodeBody(r *http.Request, into any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w: body: %v", errBadRequest, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: body holds more than one JSON value", errBadRequest)
	}
	return nil
}

func newSessionID() (string, error) {
	var raw [3]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "s-" + hex.EncodeToString(raw[:]), nil
}

func validID(id string) bool {
	if len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
