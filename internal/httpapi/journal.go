package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cyb3rgun/theserver/internal/scoring"
	"github.com/cyb3rgun/theserver/internal/store"
)

// MaxEventsLimit caps one page of GET /events.
const MaxEventsLimit = 1000

// Ranking is the answer of GET /rankings.
type Ranking struct {
	Session    string          `json:"session"`
	ComputedAt int64           `json:"computed_at"`
	Entries    []scoring.Entry `json:"entries"`
}

// Event is an event as API v1 shows it. The payload is the CBOR map as the
// device sent it, base64 encoded; the API does not decode it (D-024).
type Event struct {
	ID           string `json:"id"`
	DeviceID     string `json:"device_id"`
	SeqEpoch     uint64 `json:"seq_epoch"`
	Seq          uint64 `json:"seq"`
	Kind         string `json:"kind"`
	ControllerID string `json:"controller_id"`
	SessionID    string `json:"session_id"`
	TsDevice     int64  `json:"ts_device"`
	TsServer     int64  `json:"ts_server"`
	Payload      []byte `json:"payload"`
}

// Online is one connected device.
type Online struct {
	DeviceID    string `json:"device_id"`
	RemoteAddr  string `json:"remote_addr"`
	ConnectedAt int64  `json:"connected_at"`
	LastEventAt int64  `json:"last_event_at"`
	LastAck     uint64 `json:"last_ack"`
}

func (s *Server) rankings(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session != "" {
		if _, err := s.opts.Store.GetSession(r.Context(), session); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	entries, err := scoring.Ranking(r.Context(), s.opts.Store, scoring.Scope{SessionID: session})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, Ranking{Session: session, ComputedAt: time.Now().UnixMilli(), Entries: entries})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.Filter{
		DeviceID:     q.Get("device"),
		SessionID:    q.Get("session"),
		Kind:         q.Get("kind"),
		ControllerID: q.Get("controller"),
	}
	var err error
	number := func(name string, into *int64) {
		if err != nil || q.Get(name) == "" {
			return
		}
		*into, err = strconv.ParseInt(q.Get(name), 10, 64)
		if err != nil || *into < 0 {
			err = fmt.Errorf("%w: %s must be a whole number of 0 or more", errBadRequest, name)
		}
	}
	var from, to, limit, offset int64
	number("from", &from)
	number("to", &to)
	number("limit", &limit)
	number("offset", &offset)
	if err == nil && limit > MaxEventsLimit {
		err = fmt.Errorf("%w: limit is at most %d", errBadRequest, MaxEventsLimit)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if limit == 0 {
		limit = store.DefaultListLimit
	}
	filter.From, filter.To, filter.Limit, filter.Offset = from, to, int(limit), int(offset)

	events, err := s.opts.Store.ListEvents(r.Context(), filter)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]Event, 0, len(events))
	for _, e := range events {
		out = append(out, Event{
			ID: e.ID.String(), DeviceID: e.DeviceID, SeqEpoch: e.SeqEpoch, Seq: e.Seq, Kind: e.Kind,
			ControllerID: e.ControllerID, SessionID: e.SessionID,
			TsDevice: e.TsDevice, TsServer: e.TsServer, Payload: e.Payload,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "limit": limit, "offset": offset})
}

func (s *Server) online(w http.ResponseWriter, r *http.Request) {
	out := []Online{}
	if s.opts.Link != nil {
		for _, status := range s.opts.Link.Online() {
			entry := Online{
				DeviceID: status.DeviceID, RemoteAddr: status.RemoteAddr,
				ConnectedAt: status.ConnectedAt.UnixMilli(), LastAck: status.LastAck,
			}
			if !status.LastEventAt.IsZero() {
				entry.LastEventAt = status.LastEventAt.UnixMilli()
			}
			out = append(out, entry)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}
