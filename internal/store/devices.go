package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// Device kinds, as the CHECK constraint on devices.kind allows them.
const (
	KindTarget     = "target"
	KindController = "controller"
	KindBridge     = "bridge"
)

// Device classes, as the CHECK constraint on devices.class allows them.
const (
	ClassESP = "esp"
	ClassPi  = "pi"
	ClassPC  = "pc"
)

// Device statuses, as the CHECK constraint on devices.status allows them.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusBlocked  = "blocked"
)

// ErrDeviceNotFound is returned for a device id the registry does not hold.
var ErrDeviceNotFound = errors.New("device not found")

// A Device is one row of the registry. FirstSeen and LastSeen are unix
// milliseconds and zero while the column is NULL, which means the device has
// never been seen.
type Device struct {
	ID              string
	Kind            string
	Class           string
	Name            string
	Room            string
	Zone            string
	Status          string
	TokenHash       []byte
	FirmwareVersion string
	ConfigJSON      string
	FirstSeen       int64
	LastSeen        int64
	CreatedAt       int64
	UpdatedAt       int64
}

// UpsertDevice inserts a device or updates the one with the same id.
//
// It writes what the device reports about itself: kind, class, name, room,
// zone, firmware version and configuration. Two columns are deliberately left
// alone on an update. status changes only through SetStatus, so a device that
// reconnects cannot talk itself back to pending, and created_at keeps the time
// of the first insert. A nil TokenHash keeps the stored token; pass a hash to
// replace it. Empty Class or ConfigJSON take the defaults of the column.
func (s *Store) UpsertDevice(ctx context.Context, d Device) error {
	if d.ID == "" {
		return errors.New("device id must not be empty")
	}
	if d.Class == "" {
		d.Class = ClassESP
	}
	if d.ConfigJSON == "" {
		d.ConfigJSON = "{}"
	}
	if d.Status == "" {
		d.Status = StatusPending
	}
	defer s.writing()()
	now := s.nowMilli()

	_, err := s.db.ExecContext(ctx, `
INSERT INTO devices (
  id, kind, class, name, room, zone, status, token_hash,
  firmware_version, config_json, first_seen, last_seen, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  kind             = excluded.kind,
  class            = excluded.class,
  name             = excluded.name,
  room             = excluded.room,
  zone             = excluded.zone,
  token_hash       = COALESCE(excluded.token_hash, devices.token_hash),
  firmware_version = excluded.firmware_version,
  config_json      = excluded.config_json,
  first_seen       = COALESCE(devices.first_seen, excluded.first_seen),
  last_seen        = COALESCE(excluded.last_seen, devices.last_seen),
  updated_at       = excluded.updated_at`,
		d.ID, d.Kind, d.Class, d.Name, d.Room, d.Zone, d.Status, nullBytes(d.TokenHash),
		d.FirmwareVersion, d.ConfigJSON, nullMilli(d.FirstSeen), nullMilli(d.LastSeen), now, now,
	)
	if err != nil {
		return fmt.Errorf("upsert device %s: %w", d.ID, err)
	}
	return nil
}

const deviceColumns = `id, kind, class, name, room, zone, status, token_hash,
  firmware_version, config_json, first_seen, last_seen, created_at, updated_at`

// GetDevice reads one device. It returns ErrDeviceNotFound for an unknown id.
func (s *Store) GetDevice(ctx context.Context, id string) (Device, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, fmt.Errorf("%s: %w", id, ErrDeviceNotFound)
	}
	if err != nil {
		return Device{}, fmt.Errorf("get device %s: %w", id, err)
	}
	return d, nil
}

// ListDevices returns every device, ordered by id.
func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceColumns+` FROM devices ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("list devices: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	return devices, nil
}

// TouchLastSeen records that the device answered at ts. The first touch also
// fills first_seen.
func (s *Store) TouchLastSeen(ctx context.Context, id string, ts time.Time) error {
	milli := ts.UTC().UnixMilli()
	defer s.writing()()
	result, err := s.db.ExecContext(ctx, `
UPDATE devices
   SET last_seen  = ?,
       first_seen = COALESCE(first_seen, ?),
       updated_at = ?
 WHERE id = ?`, milli, milli, s.nowMilli(), id)
	if err != nil {
		return fmt.Errorf("touch device %s: %w", id, err)
	}
	return checkOneRow(result, id)
}

// SetStatus moves a device to pending, approved or blocked. An unknown status
// is refused by the CHECK constraint on the column.
func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	defer s.writing()()
	result, err := s.db.ExecContext(ctx,
		`UPDATE devices SET status = ?, updated_at = ? WHERE id = ?`, status, s.nowMilli(), id)
	if err != nil {
		return fmt.Errorf("set status of device %s to %q: %w", id, status, err)
	}
	return checkOneRow(result, id)
}

// SetFirmwareVersion records the firmware a device reported in its hello.
func (s *Store) SetFirmwareVersion(ctx context.Context, id, version string) error {
	defer s.writing()()
	result, err := s.db.ExecContext(ctx,
		`UPDATE devices SET firmware_version = ?, updated_at = ? WHERE id = ? AND firmware_version <> ?`,
		version, s.nowMilli(), id, version)
	if err != nil {
		return fmt.Errorf("set firmware of device %s: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		// Either the version did not change or the device is unknown.
		if _, err := s.GetDevice(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// DevicesNotApproved lists the ids of every device whose status is not
// approved. The device link closes their connections.
func (s *Store) DevicesNotApproved(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM devices WHERE status <> ? ORDER BY id`, StatusApproved)
	if err != nil {
		return nil, fmt.Errorf("list devices that are not approved: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// NewDeviceToken draws a device token: 32 bytes from crypto/rand, written as
// unpadded URL safe base64. Only its hash is ever stored.
func NewDeviceToken() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", fmt.Errorf("new device token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

// DeviceByToken finds the device a bearer token belongs to, through the hash
// of the token (D-019). An empty token or one that no device holds returns
// ErrDeviceNotFound.
func (s *Store) DeviceByToken(ctx context.Context, token string) (Device, error) {
	if token == "" {
		return Device{}, fmt.Errorf("empty token: %w", ErrDeviceNotFound)
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE token_hash = ?`, HashToken(token))
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, fmt.Errorf("token: %w", ErrDeviceNotFound)
	}
	if err != nil {
		return Device{}, fmt.Errorf("device by token: %w", err)
	}
	return d, nil
}

// HashToken is how a device token is stored: the SHA-256 of the token, never
// the token itself (D-015).
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// VerifyToken reports whether token belongs to the device. The stored hash
// and the hash of token are compared in constant time, so a wrong token gives
// nothing away through how long the comparison took. A device without a token
// verifies nothing.
func (s *Store) VerifyToken(ctx context.Context, id, token string) (bool, error) {
	var stored []byte
	err := s.db.QueryRowContext(ctx, `SELECT token_hash FROM devices WHERE id = ?`, id).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%s: %w", id, ErrDeviceNotFound)
	}
	if err != nil {
		return false, fmt.Errorf("verify token of device %s: %w", id, err)
	}
	if len(stored) == 0 {
		return false, nil
	}
	return subtle.ConstantTimeCompare(stored, HashToken(token)) == 1, nil
}

// rowScanner is what both *sql.Row and *sql.Rows offer.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDevice(row rowScanner) (Device, error) {
	var (
		d                   Device
		tokenHash           []byte
		firstSeen, lastSeen sql.NullInt64
	)
	err := row.Scan(
		&d.ID, &d.Kind, &d.Class, &d.Name, &d.Room, &d.Zone, &d.Status, &tokenHash,
		&d.FirmwareVersion, &d.ConfigJSON, &firstSeen, &lastSeen, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return Device{}, err
	}
	d.TokenHash = tokenHash
	d.FirstSeen = firstSeen.Int64
	d.LastSeen = lastSeen.Int64
	return d, nil
}

// nullMilli keeps a zero timestamp out of the database, where the column
// means "never" with NULL.
func nullMilli(milli int64) any {
	if milli == 0 {
		return nil
	}
	return milli
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func checkOneRow(result sql.Result, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%s: %w", id, ErrDeviceNotFound)
	}
	return nil
}
