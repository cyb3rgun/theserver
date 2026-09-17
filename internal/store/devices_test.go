package store

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func testDevice(id string) Device {
	return Device{
		ID:              id,
		Kind:            KindTarget,
		Class:           ClassESP,
		Name:            "range left",
		Room:            "hall",
		Zone:            "a",
		FirmwareVersion: "0.1.0",
	}
}

func TestUpsertDeviceInsertsAndUpdates(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	if err := s.UpsertDevice(ctx, testDevice("t-1")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	inserted, err := s.GetDevice(ctx, "t-1")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if inserted.Status != StatusPending {
		t.Errorf("status is %q, want %q for a new device", inserted.Status, StatusPending)
	}
	if inserted.ConfigJSON != "{}" {
		t.Errorf("config_json is %q, want the column default", inserted.ConfigJSON)
	}
	if inserted.CreatedAt == 0 || inserted.UpdatedAt != inserted.CreatedAt {
		t.Errorf("created_at %d and updated_at %d are not both stamped", inserted.CreatedAt, inserted.UpdatedAt)
	}
	if inserted.FirstSeen != 0 || inserted.LastSeen != 0 {
		t.Errorf("a device that was never seen has first_seen %d, last_seen %d", inserted.FirstSeen, inserted.LastSeen)
	}

	changed := testDevice("t-1")
	changed.Name = "range right"
	changed.FirmwareVersion = "0.2.0"
	if err := s.UpsertDevice(ctx, changed); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, err := s.GetDevice(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "range right" || updated.FirmwareVersion != "0.2.0" {
		t.Errorf("update did not take: %+v", updated)
	}
	if updated.CreatedAt != inserted.CreatedAt {
		t.Errorf("created_at changed from %d to %d", inserted.CreatedAt, updated.CreatedAt)
	}
}

func TestUpsertDeviceKeepsStatusAndToken(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	device := testDevice("t-1")
	device.TokenHash = HashToken("first token")
	if err := s.UpsertDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(ctx, "t-1", StatusApproved); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	// The device reconnects and reports itself again, without a token.
	if err := s.UpsertDevice(ctx, testDevice("t-1")); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetDevice(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusApproved {
		t.Errorf("status fell back to %q, want it to stay %q", got.Status, StatusApproved)
	}
	if !bytes.Equal(got.TokenHash, HashToken("first token")) {
		t.Error("the stored token hash was lost by an upsert without a token")
	}
}

func TestGetDeviceUnknown(t *testing.T) {
	s := openTest(t)
	if _, err := s.GetDevice(t.Context(), "nobody"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("GetDevice error is %v, want ErrDeviceNotFound", err)
	}
}

func TestListDevicesOrderedByID(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	for _, id := range []string{"t-3", "t-1", "t-2"} {
		if err := s.UpsertDevice(ctx, testDevice(id)); err != nil {
			t.Fatal(err)
		}
	}
	devices, err := s.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 3 {
		t.Fatalf("ListDevices returned %d devices, want 3", len(devices))
	}
	for i, want := range []string{"t-1", "t-2", "t-3"} {
		if devices[i].ID != want {
			t.Errorf("device %d is %s, want %s", i, devices[i].ID, want)
		}
	}
}

func TestTouchLastSeen(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.UpsertDevice(ctx, testDevice("t-1")); err != nil {
		t.Fatal(err)
	}

	first := time.UnixMilli(1_700_000_100_000).UTC()
	if err := s.TouchLastSeen(ctx, "t-1", first); err != nil {
		t.Fatalf("TouchLastSeen: %v", err)
	}
	seen, err := s.GetDevice(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if seen.FirstSeen != first.UnixMilli() || seen.LastSeen != first.UnixMilli() {
		t.Errorf("first touch wrote first_seen %d, last_seen %d, want both %d", seen.FirstSeen, seen.LastSeen, first.UnixMilli())
	}

	later := time.UnixMilli(1_700_000_200_000).UTC()
	if err := s.TouchLastSeen(ctx, "t-1", later); err != nil {
		t.Fatal(err)
	}
	seen, err = s.GetDevice(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if seen.FirstSeen != first.UnixMilli() {
		t.Errorf("first_seen moved to %d, want it to stay %d", seen.FirstSeen, first.UnixMilli())
	}
	if seen.LastSeen != later.UnixMilli() {
		t.Errorf("last_seen is %d, want %d", seen.LastSeen, later.UnixMilli())
	}

	if err := s.TouchLastSeen(ctx, "nobody", later); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("TouchLastSeen on an unknown device returned %v, want ErrDeviceNotFound", err)
	}
}

func TestSetStatus(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.UpsertDevice(ctx, testDevice("t-1")); err != nil {
		t.Fatal(err)
	}

	for _, status := range []string{StatusApproved, StatusBlocked, StatusPending} {
		if err := s.SetStatus(ctx, "t-1", status); err != nil {
			t.Fatalf("SetStatus %q: %v", status, err)
		}
		got, err := s.GetDevice(ctx, "t-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != status {
			t.Errorf("status is %q, want %q", got.Status, status)
		}
	}

	if err := s.SetStatus(ctx, "nobody", StatusApproved); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("SetStatus on an unknown device returned %v, want ErrDeviceNotFound", err)
	}
}

// TestCheckConstraints proves that the database, not only the Go code, refuses
// values outside the allowed sets.
func TestCheckConstraints(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.UpsertDevice(ctx, testDevice("t-1")); err != nil {
		t.Fatal(err)
	}

	if err := s.SetStatus(ctx, "t-1", "retired"); err == nil {
		t.Error("SetStatus accepted the unknown status \"retired\"")
	} else {
		got, err := s.GetDevice(ctx, "t-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusPending {
			t.Errorf("the refused status left %q behind", got.Status)
		}
	}

	wrongKind := testDevice("t-2")
	wrongKind.Kind = "drone"
	if err := s.UpsertDevice(ctx, wrongKind); err == nil {
		t.Error("UpsertDevice accepted the unknown kind \"drone\"")
	}

	wrongClass := testDevice("t-3")
	wrongClass.Class = "quantum"
	if err := s.UpsertDevice(ctx, wrongClass); err == nil {
		t.Error("UpsertDevice accepted the unknown class \"quantum\"")
	}
}

func TestTokenHashingAndVerify(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	hash := HashToken("s3cret")
	if len(hash) != 32 {
		t.Fatalf("HashToken returned %d bytes, want 32", len(hash))
	}
	if bytes.Equal(hash, []byte("s3cret")) {
		t.Fatal("HashToken returned the token itself")
	}
	if !bytes.Equal(hash, HashToken("s3cret")) {
		t.Error("HashToken is not stable")
	}

	device := testDevice("t-1")
	device.TokenHash = hash
	if err := s.UpsertDevice(ctx, device); err != nil {
		t.Fatal(err)
	}

	ok, err := s.VerifyToken(ctx, "t-1", "s3cret")
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if !ok {
		t.Error("the right token did not verify")
	}

	ok, err = s.VerifyToken(ctx, "t-1", "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a wrong token verified")
	}

	if err := s.UpsertDevice(ctx, testDevice("t-2")); err != nil {
		t.Fatal(err)
	}
	ok, err = s.VerifyToken(ctx, "t-2", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a device without a token verified an empty token")
	}

	if _, err := s.VerifyToken(ctx, "nobody", "s3cret"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("VerifyToken on an unknown device returned %v, want ErrDeviceNotFound", err)
	}
}

func TestDeviceByToken(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	token, err := NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	device := testDevice("t-1")
	device.TokenHash = HashToken(token)
	device.Status = StatusApproved
	if err := s.UpsertDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDevice(ctx, testDevice("t-2")); err != nil {
		t.Fatal(err)
	}

	got, err := s.DeviceByToken(ctx, token)
	if err != nil {
		t.Fatalf("DeviceByToken: %v", err)
	}
	if got.ID != "t-1" || got.Status != StatusApproved {
		t.Errorf("DeviceByToken found %s in status %s, want t-1 approved", got.ID, got.Status)
	}

	for _, wrong := range []string{"", "not a token", token + "x"} {
		if _, err := s.DeviceByToken(ctx, wrong); !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("DeviceByToken(%q) returned %v, want ErrDeviceNotFound", wrong, err)
		}
	}
}

func TestTokensAreUniqueAndRandom(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	seen := map[string]bool{}
	for range 100 {
		token, err := NewDeviceToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(token) != 43 {
			t.Fatalf("token %q is %d characters, want 43", token, len(token))
		}
		if seen[token] {
			t.Fatalf("token %q drawn twice", token)
		}
		seen[token] = true
	}

	// The unique index refuses a second device with the same token.
	first := testDevice("t-1")
	first.TokenHash = HashToken("shared")
	if err := s.UpsertDevice(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := testDevice("t-2")
	second.TokenHash = HashToken("shared")
	if err := s.UpsertDevice(ctx, second); err == nil {
		t.Error("two devices were stored with the same token")
	}
}

func TestSetFirmwareVersion(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.UpsertDevice(ctx, testDevice("t-1")); err != nil {
		t.Fatal(err)
	}

	for _, version := range []string{"0.3.0", "0.3.0"} {
		if err := s.SetFirmwareVersion(ctx, "t-1", version); err != nil {
			t.Fatalf("SetFirmwareVersion %s: %v", version, err)
		}
	}
	got, err := s.GetDevice(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.FirmwareVersion != "0.3.0" {
		t.Errorf("firmware is %q, want 0.3.0", got.FirmwareVersion)
	}
	if err := s.SetFirmwareVersion(ctx, "nobody", "1.0"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("an unknown device returned %v, want ErrDeviceNotFound", err)
	}
}

func TestDeviceStates(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	for _, id := range []string{"t-1", "t-2"} {
		if err := s.UpsertDevice(ctx, testDevice(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetStatus(ctx, "t-1", StatusApproved); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceToken(ctx, "t-1", HashToken("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetDevice(ctx, "t-2"); err != nil {
		t.Fatal(err)
	}

	states, err := s.DeviceStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("DeviceStates returned %d devices, want 2", len(states))
	}
	one, two := states["t-1"], states["t-2"]
	if one.Status != StatusApproved || one.SeqEpoch != 1 || !bytes.Equal(one.TokenHash, HashToken("one")) {
		t.Errorf("t-1 is %+v", one)
	}
	if two.Status != StatusPending || two.SeqEpoch != 2 || two.TokenHash != nil {
		t.Errorf("t-2 is %+v", two)
	}
}

func TestResetDevice(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.UpsertDevice(ctx, testDevice("t-1")); err != nil {
		t.Fatal(err)
	}
	for want := uint64(2); want <= 3; want++ {
		epoch, err := s.ResetDevice(ctx, "t-1")
		if err != nil {
			t.Fatalf("ResetDevice: %v", err)
		}
		if epoch != want {
			t.Errorf("epoch after reset is %d, want %d", epoch, want)
		}
	}
	device, err := s.GetDevice(ctx, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if device.SeqEpoch != 3 {
		t.Errorf("GetDevice reports epoch %d, want 3", device.SeqEpoch)
	}
	if _, err := s.ResetDevice(ctx, "nobody"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("resetting an unknown device returned %v, want ErrDeviceNotFound", err)
	}
}

func TestSetDeviceToken(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	device := testDevice("t-1")
	device.TokenHash = HashToken("old")
	if err := s.UpsertDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceToken(ctx, "t-1", HashToken("new")); err != nil {
		t.Fatalf("SetDeviceToken: %v", err)
	}
	if _, err := s.DeviceByToken(ctx, "old"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("the old token still finds a device: %v", err)
	}
	if got, err := s.DeviceByToken(ctx, "new"); err != nil || got.ID != "t-1" {
		t.Errorf("the new token finds %q, %v", got.ID, err)
	}
	if err := s.SetDeviceToken(ctx, "nobody", HashToken("x")); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("an unknown device returned %v, want ErrDeviceNotFound", err)
	}
	if err := s.SetDeviceToken(ctx, "t-1", nil); err == nil {
		t.Error("an empty token hash was accepted")
	}
}
