package httpapi

import (
	"net/http"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/store"
)

// Device is a device as API v1 shows it. The token hash never leaves the
// server; HasToken only says whether there is one.
type Device struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Class           string `json:"class"`
	Name            string `json:"name"`
	Room            string `json:"room"`
	Zone            string `json:"zone"`
	Status          string `json:"status"`
	FirmwareVersion string `json:"firmware_version"`
	SeqEpoch        uint64 `json:"seq_epoch"`
	HasToken        bool   `json:"has_token"`
	FirstSeen       int64  `json:"first_seen"`
	LastSeen        int64  `json:"last_seen"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	Online          bool   `json:"online"`
	ConnectedAt     int64  `json:"connected_at"`
	LastAck         uint64 `json:"last_ack"`
	// MinAge is the age the device is set for (D-039).
	MinAge int `json:"min_age"`
	// TargetType is the kind of target the device is (D-061), empty while
	// none is assigned.
	TargetType string `json:"target_type"`
}

// NewToken is the answer to a token change; the token is shown this once.
type NewToken struct {
	DeviceID string `json:"device_id"`
	Token    string `json:"token"`
	Warning  string `json:"warning"`
}

func (s *Server) onlineByID() map[string]link.DeviceStatus {
	online := map[string]link.DeviceStatus{}
	if s.opts.Link == nil {
		return online
	}
	for _, status := range s.opts.Link.Online() {
		online[status.DeviceID] = status
	}
	return online
}

func deviceJSON(d store.Device, online map[string]link.DeviceStatus) Device {
	out := Device{
		ID: d.ID, Kind: d.Kind, Class: d.Class, Name: d.Name, Room: d.Room, Zone: d.Zone,
		Status: d.Status, FirmwareVersion: d.FirmwareVersion, SeqEpoch: d.SeqEpoch,
		HasToken: len(d.TokenHash) > 0, FirstSeen: d.FirstSeen, LastSeen: d.LastSeen,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, MinAge: d.MinAge, TargetType: d.TargetType,
	}
	if status, ok := online[d.ID]; ok {
		out.Online = true
		out.ConnectedAt = status.ConnectedAt.UnixMilli()
		out.LastAck = status.LastAck
	}
	return out
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.opts.Store.ListDevices(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	online := s.onlineByID()
	out := make([]Device, 0, len(devices))
	for _, d := range devices {
		out = append(out, deviceJSON(d, online))
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	s.writeDevice(w, r, r.PathValue("id"), http.StatusOK)
}

func (s *Server) writeDevice(w http.ResponseWriter, r *http.Request, id string, status int) {
	d, err := s.opts.Store.GetDevice(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, deviceJSON(d, s.onlineByID()))
}

func (s *Server) approveDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Store.SetStatus(r.Context(), id, store.StatusApproved); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "device approved", "device", id)
	// A device that is allowed in learns what it is and where it stands
	// (D-063, D-068); one without a type and without a room is told
	// nothing.
	s.sendSetup(r, id)
	s.writeDevice(w, r, id, http.StatusOK)
}

func (s *Server) blockDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.opts.Store.SetStatus(r.Context(), id, store.StatusBlocked); err != nil {
		s.fail(w, r, err)
		return
	}
	s.disconnect(id, link.Revoked)
	s.audit(r, "device blocked", "device", id)
	s.writeDevice(w, r, id, http.StatusOK)
}

// resetDevice starts a new sequence epoch and drops the live connection; the
// device reconnects and learns the epoch from welcome (D-026).
func (s *Server) resetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	epoch, err := s.opts.Store.ResetDevice(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.disconnect(id, link.Reset)
	s.audit(r, "device reset", "device", id, "epoch", epoch)
	s.writeDevice(w, r, id, http.StatusOK)
}

// newDeviceToken replaces the token of a device, drops its live connection
// and shows the new token once.
func (s *Server) newDeviceToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token, err := store.NewDeviceToken()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.opts.Store.SetDeviceToken(r.Context(), id, store.HashToken(token)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.disconnect(id, link.TokenReplaced)
	s.audit(r, "device token replaced", "device", id)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, NewToken{
		DeviceID: id,
		Token:    token,
		Warning:  "This token is shown once and cannot be shown again. Put it into the device now.",
	})
}

func (s *Server) disconnect(id string, why link.DisconnectReason) {
	if s.opts.Link != nil {
		s.opts.Link.Disconnect(id, why)
	}
}

// audit logs a change with the admin token that made it.
func (s *Server) audit(r *http.Request, what string, args ...any) {
	token, _ := AdminFrom(r.Context())
	s.log.Info(what, append(args, "admin_token", token.ID, "admin_name", token.Name)...)
}
