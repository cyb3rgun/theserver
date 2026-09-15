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
