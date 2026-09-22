package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/store"
)

// maxBody bounds a JSON request body.
const maxBody = 64 << 10

// Session is a session as API v1 shows it. Scenario and ScenarioVersion
// name the scenario version it plays; the version is 0 while none is
// assigned, and Scenario is then a label given at creation. Readiness says
// per device whether it can play what the session plays (D-059).
type Session struct {
	ID              string      `json:"id"`
	Scenario        string      `json:"scenario"`
	ScenarioVersion int         `json:"scenario_version"`
	Room            string      `json:"room"`
	RoomID          string      `json:"room_id"`
	State           string      `json:"state"`
	StartedAt       int64       `json:"started_at"`
	EndedAt         int64       `json:"ended_at"`
	CreatedAt       int64       `json:"created_at"`
	UpdatedAt       int64       `json:"updated_at"`
	Devices         []string    `json:"devices"`
	Readiness       []Readiness `json:"readiness"`
}

// The reasons a device of a session is not ready to play.
const (
	// ReadyReasonNone is the empty reason of a device that is ready.
	ReadyReasonNone = ""
	// ReadyNoScenario: the session has no published version assigned yet.
	ReadyNoScenario = "no_scenario"
	// ReadyOffline: the device is not connected.
	ReadyOffline = "offline"
	// ReadyInstalling: the device is fetching the scenario.
	ReadyInstalling = "installing"
	// ReadyTimeout: the install is taking longer than
	// link.install_timeout_s.
	ReadyTimeout = "timeout"
	// ReadyFailed: the device refused the start or could not install.
	ReadyFailed = "failed"
	// ReadyMissing: the device does not hold the version and nothing is
	// under way yet.
	ReadyMissing = "missing"
	// ReadyNotStarted: the device holds the version but has not taken the
	// start yet.
	ReadyNotStarted = "not_started"
)

// Readiness is one device of a session: whether it is connected, whether it
// holds what the session plays, whether it was told to play, and, when it is
// not ready, why.
type Readiness struct {
	DeviceID string `json:"device_id"`
	Online   bool   `json:"online"`
	Holds    bool   `json:"holds"`
	Started  bool   `json:"started"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason,omitempty"`
	// Detail is what the link said, empty when it said nothing.
	Detail string `json:"detail,omitempty"`
	// WaitedS is how long the device has been installing, in seconds.
	WaitedS int `json:"waited_s,omitempty"`
}

// NewSession is the body of POST /sessions. RoomID names the room the
// session runs in; its approved targets become the devices of the session
// (D-067). An empty ID is chosen by the
// server.
type NewSession struct {
	ID       string `json:"id"`
	Scenario string `json:"scenario"`
	Room     string `json:"room"`
	RoomID   string `json:"room_id"`
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
		ID: s.ID, Scenario: s.Scenario, ScenarioVersion: s.ScenarioVersion,
		Room: s.Room, RoomID: s.RoomID, State: s.State,
		StartedAt: s.StartedAt, EndedAt: s.EndedAt, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		Devices: devices, Readiness: []Readiness{},
	}
}

// readiness says for every device of a session whether it can play what the
// session plays: the store says what it holds, the link says whether it is
// connected and what it was told (D-059).
func (s *Server) readiness(ctx context.Context, session store.Session) []Readiness {
	out := make([]Readiness, 0, len(session.Devices))
	online := map[string]bool{}
	states := map[string]link.SessionState{}
	if s.opts.Link != nil {
		for _, status := range s.opts.Link.Online() {
			online[status.DeviceID] = true
		}
		for _, state := range s.opts.Link.SessionStates(session.ID) {
			states[state.DeviceID] = state
		}
	}
	for _, deviceID := range session.Devices {
		item := Readiness{DeviceID: deviceID, Online: online[deviceID]}
		if session.ScenarioVersion >= 1 {
			holds, err := s.opts.Store.HoldsScenario(ctx, deviceID, session.Scenario, session.ScenarioVersion)
			if err != nil {
				s.log.Error("could not read what a device holds", "device", deviceID, "error", err)
			}
			item.Holds = holds
		}
		state, told := states[deviceID]
		item.Started = told && state.State == link.StateStarted
		item.Detail = state.Error
		item.WaitedS = state.Waited
		switch {
		case session.ScenarioVersion < 1:
			item.Reason = ReadyNoScenario
		case told && state.State == link.StateStarted:
			item.Ready = true
		case told && state.State == link.StateFailed:
			item.Reason = ReadyFailed
		case told && state.State == link.StateTimeout:
			item.Reason = ReadyTimeout
		case !item.Online:
			item.Reason = ReadyOffline
		case told && state.State == link.StateWaiting && !item.Holds:
			item.Reason = ReadyInstalling
		case !item.Holds:
			item.Reason = ReadyMissing
		case session.State == store.SessionRunning:
			item.Reason = ReadyNotStarted
		default:
			// A created session with a device that is here and holds the
			// version: it is ready to be started.
			item.Ready = true
		}
		out = append(out, item)
	}
	return out
}

// sessionAnswer is sessionJSON with the readiness of its devices.
func (s *Server) sessionAnswer(ctx context.Context, session store.Session) Session {
	out := sessionJSON(session)
	out.Readiness = s.readiness(ctx, session)
	return out
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.opts.Store.ListSessions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, s.sessionAnswer(r.Context(), session))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// getSession is one session with what its devices are doing.
func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	s.writeSession(w, r, r.PathValue("id"), http.StatusOK)
}

func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, id string, status int) {
	session, err := s.opts.Store.GetSession(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, s.sessionAnswer(r.Context(), session))
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
	if body.RoomID != "" {
		if _, err := s.opts.Store.GetRoom(ctx, body.RoomID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if _, err := s.opts.Store.GetSession(ctx, body.ID); err == nil {
		s.fail(w, r, fmt.Errorf("%w: session %s exists", errConflict, body.ID))
		return
	} else if !errors.Is(err, store.ErrSessionNotFound) {
		s.fail(w, r, err)
		return
	}
	err := s.opts.Store.CreateSession(ctx, store.Session{
		ID: body.ID, Scenario: body.Scenario, Room: body.Room, RoomID: body.RoomID,
	})
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
	ctx := r.Context()
	if err := s.opts.Store.StartSession(ctx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	session, err := s.opts.Store.GetSession(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "session started", "session", id, "scenario", session.Scenario, "version", session.ScenarioVersion)
	if s.opts.Link != nil {
		// Every device is told what it plays; one that does not hold the
		// version is announced to and told when it reports it installed
		// (D-057, D-059).
		if _, err := s.opts.Link.StartSession(ctx, session); err != nil {
			s.log.Error("could not tell the devices what the session plays", "session", id, "error", err)
		}
	}
	s.writeSession(w, r, id, http.StatusOK)
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	session, err := s.opts.Store.GetSession(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.opts.Store.StopSession(ctx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "session stopped", "session", id)
	if s.opts.Link != nil {
		if err := s.opts.Link.StopSession(ctx, session); err != nil {
			s.log.Error("could not tell the devices that the session is over", "session", id, "error", err)
		}
	}
	s.writeSession(w, r, id, http.StatusOK)
}

// removeSessionDevice leaves a target out of a session that has not
// started, which is how a room is taken with one target excluded (D-067).
func (s *Server) removeSessionDevice(w http.ResponseWriter, r *http.Request) {
	id, deviceID := r.PathValue("id"), r.PathValue("device")
	if err := s.opts.Store.RemoveSessionDevice(r.Context(), id, deviceID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "session device removed", "session", id, "device", deviceID)
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
	ctx := r.Context()
	if err := s.opts.Store.AddSessionDevice(ctx, id, body.DeviceID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "device added to session", "session", id, "device", body.DeviceID)
	s.announce(r, body.DeviceID, id)
	// A device that joins a session that already runs is told what it plays
	// as well (D-057).
	if session, err := s.opts.Store.GetSession(ctx, id); err == nil &&
		session.State == store.SessionRunning && s.opts.Link != nil {
		session.Devices = []string{body.DeviceID}
		if _, err := s.opts.Link.StartSession(ctx, session); err != nil {
			s.log.Error("could not tell a joining device what the session plays",
				"session", id, "device", body.DeviceID, "error", err)
		}
	}
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
