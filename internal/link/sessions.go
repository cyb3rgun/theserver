package link

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// The session side of the link (D-056, D-057, D-059). A device never has to
// guess what it plays: the welcome carries the scenario version of its
// running session, and `session_start` carries it again with the session.
// A device that does not hold that version is announced to first and told
// to start when it reports the version installed.

// The states the link keeps for one device of a running session.
const (
	// StateStarted: the device took session_start.
	StateStarted = "started"
	// StateWaiting: the start is on its way, or it waits for the device to
	// install the scenario version.
	StateWaiting = "waiting"
	// StateTimeout: the install did not arrive within the install timeout.
	// The link keeps waiting; the page says the device is not ready.
	StateTimeout = "timeout"
	// StateOffline: the device was not connected; it is told when it
	// connects.
	StateOffline = "offline"
	// StateFailed: the device refused the start, or the command failed.
	StateFailed = "failed"
)

// A SessionState is what the link did for one device of a session.
type SessionState struct {
	DeviceID  string
	SessionID string
	Scenario  string
	Version   int
	State     string
	// Since is when the state was reached, in unix milliseconds.
	Since int64
	// Error is why a start failed, empty otherwise.
	Error string
	// Waited is how long a waiting device has been waiting, in seconds.
	Waited int
}

// deviceStart is the start of one device the link is working on.
type deviceStart struct {
	session  string
	scenario string
	version  int
	state    string
	since    time.Time
	// deadline is when a wait for the content counts as too long; it is zero
	// while the start itself is in flight.
	deadline time.Time
	err      string
}

// sessions holds what the link is doing for the devices of running sessions.
type sessions struct {
	mu     sync.Mutex
	starts map[string]*deviceStart
}

func newSessions() *sessions {
	return &sessions{starts: map[string]*deviceStart{}}
}

// StartSession tells every device of the session what it plays (D-057). A
// device that holds the version is told at once; one that does not is
// announced to and told when it reports the version installed (D-059); one
// that is offline is told when it connects. It returns what came of it, per
// device.
func (s *Server) StartSession(ctx context.Context, session store.Session) ([]SessionState, error) {
	if session.ScenarioVersion < 1 {
		return nil, fmt.Errorf("session %s plays nothing", session.ID)
	}
	var wg sync.WaitGroup
	for _, deviceID := range session.Devices {
		holds, err := s.store.HoldsScenario(ctx, deviceID, session.Scenario, session.ScenarioVersion)
		if err != nil {
			return nil, err
		}
		s.planStart(deviceID, session, holds)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if holds {
				s.sendStart(ctx, deviceID)
				return
			}
			// The device needs the package first; the start follows its
			// installed report (D-059).
			if _, err := s.AnnouncePending(ctx, deviceID, session.ID); err != nil {
				s.log.Warn("could not announce the content of a starting session",
					"session", session.ID, "device", deviceID, "error", err)
			}
		}()
	}
	wg.Wait()
	states := s.SessionStates(session.ID)
	for _, state := range states {
		s.log.Info("session start", "session", session.ID, "device", state.DeviceID,
			"scenario", state.Scenario, "version", state.Version, "state", state.State)
	}
	return states, nil
}

// StopSession tells every device of the session that it is over and forgets
// what it knew about the session.
func (s *Server) StopSession(ctx context.Context, session store.Session) error {
	args := protocol.SessionStop{Ses: session.ID}
	var wg sync.WaitGroup
	for _, deviceID := range session.Devices {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.SendCommand(ctx, deviceID, protocol.CommandSessionStop, args.Args())
			switch {
			case errors.Is(err, ErrDeviceOffline):
				s.log.Info("session stop not delivered", "session", session.ID, "device", deviceID, "reason", "offline")
			case err != nil:
				s.log.Warn("session stop failed", "session", session.ID, "device", deviceID, "error", err)
			case !result.OK:
				s.log.Warn("session stop refused", "session", session.ID, "device", deviceID, "error", result.E)
			default:
				s.log.Info("session stop", "session", session.ID, "device", deviceID)
			}
		}()
	}
	wg.Wait()
	s.forgetSession(session.ID)
	return nil
}

// SessionStates is what the link knows about the devices of a session, by
// device id. A wait that ran past the install timeout reads as a timeout.
func (s *Server) SessionStates(sessionID string) []SessionState {
	now := s.now()
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()

	var out []SessionState
	for deviceID, start := range s.sessions.starts {
		if start.session != sessionID {
			continue
		}
		state := start.state
		if state == StateWaiting && !start.deadline.IsZero() && now.After(start.deadline) {
			state = StateTimeout
		}
		waited := 0
		if state == StateWaiting || state == StateTimeout {
			waited = int(now.Sub(start.since).Seconds())
		}
		out = append(out, SessionState{
			DeviceID: deviceID, SessionID: start.session, Scenario: start.scenario, Version: start.version,
			State: state, Since: start.since.UnixMilli(), Error: start.err, Waited: waited,
		})
	}
	slices.SortFunc(out, func(a, b SessionState) int {
		if a.DeviceID < b.DeviceID {
			return -1
		}
		if a.DeviceID > b.DeviceID {
			return 1
		}
		return 0
	})
	return out
}

// planStart records what a device is about to be told. A device that does
// not hold the version gets a deadline: after it the page says the device is
// not ready, while the link keeps waiting.
func (s *Server) planStart(deviceID string, session store.Session, holds bool) {
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	start := &deviceStart{
		session:  session.ID,
		scenario: session.Scenario,
		version:  session.ScenarioVersion,
		state:    StateWaiting,
		since:    s.now(),
	}
	if !holds {
		start.deadline = start.since.Add(s.installTimeout())
	}
	s.sessions.starts[deviceID] = start
}

// sendStart sends session_start to a device that holds what its session
// plays and records what came of it.
func (s *Server) sendStart(ctx context.Context, deviceID string) {
	s.sessions.mu.Lock()
	start, ok := s.sessions.starts[deviceID]
	if !ok || start.state == StateStarted {
		s.sessions.mu.Unlock()
		return
	}
	args := protocol.SessionStart{
		Ses: start.session,
		Scn: protocol.Holding{ID: start.scenario, Ver: uint64(start.version)},
	}
	s.sessions.mu.Unlock()

	result, err := s.SendCommand(ctx, deviceID, protocol.CommandSessionStart, args.Args())

	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	current, ok := s.sessions.starts[deviceID]
	if !ok || current.session != args.Ses {
		// The session changed while the command was in flight.
		return
	}
	current.since, current.err, current.deadline = s.now(), "", time.Time{}
	attrs := []any{"session", args.Ses, "device", deviceID, "scenario", args.Scn.ID, "version", args.Scn.Ver}
	switch {
	case errors.Is(err, ErrDeviceOffline):
		current.state = StateOffline
		s.log.Info("session start waits for the device", attrs...)
	case err != nil:
		current.state, current.err = StateFailed, err.Error()
		s.log.Warn("session start failed", append(attrs, "error", err)...)
	case !result.OK:
		current.state, current.err = StateFailed, result.E
		s.log.Warn("session start refused", append(attrs, "error", result.E)...)
	default:
		current.state = StateStarted
		s.log.Info("session start delivered", attrs...)
	}
}

// maybeStart tells a device that was waiting for its content to start, once
// it holds the version its session plays (D-059).
func (s *Server) maybeStart(ctx context.Context, deviceID string) {
	s.sessions.mu.Lock()
	start, ok := s.sessions.starts[deviceID]
	if !ok || start.state == StateStarted {
		s.sessions.mu.Unlock()
		return
	}
	scenarioID, version := start.scenario, start.version
	s.sessions.mu.Unlock()

	holds, err := s.store.HoldsScenario(ctx, deviceID, scenarioID, version)
	if err != nil {
		s.log.Warn("could not read what the device holds", "device", deviceID, "error", err)
		return
	}
	if !holds {
		return
	}
	s.sendStart(ctx, deviceID)
}

// forgetSession drops what the link knew about the devices of a session.
func (s *Server) forgetSession(sessionID string) {
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	for deviceID, start := range s.sessions.starts {
		if start.session == sessionID {
			delete(s.sessions.starts, deviceID)
		}
	}
}

// installTimeout is how long a start waits for a device to install the
// scenario before the page calls it not ready (D-059).
func (s *Server) installTimeout() time.Duration {
	cfg := s.Config()
	if cfg.InstallTimeout <= 0 {
		return DefaultConfig().InstallTimeout
	}
	return cfg.InstallTimeout
}

// sessionOnConnect tells a device that just connected into a running session
// what it plays, or waits for its content first (D-057).
func (c *deviceConn) sessionOnConnect(session store.Session) {
	if session.ID == "" || session.ScenarioVersion < 1 {
		return
	}
	ctx := c.ctx
	holds, err := c.srv.store.HoldsScenario(ctx, c.deviceID, session.Scenario, session.ScenarioVersion)
	if err != nil {
		c.log.Warn("could not read what the device holds", "error", err)
		return
	}
	c.srv.planStart(c.deviceID, session, holds)
	if holds {
		c.srv.sendStart(ctx, c.deviceID)
		return
	}
	c.log.Info("session start waits for the content",
		"session", session.ID, "scenario", session.Scenario, "version", session.ScenarioVersion)
}
