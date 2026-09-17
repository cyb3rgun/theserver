package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAdminTokens(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	row, token, err := s.AddAdminToken(ctx, "  founder ")
	if err != nil {
		t.Fatalf("AddAdminToken: %v", err)
	}
	if !strings.HasPrefix(row.ID, "adm-") || row.Name != "founder" || row.CreatedAt == 0 || len(token) != 43 {
		t.Fatalf("AddAdminToken returned %+v and a token of %d characters", row, len(token))
	}

	// Only the hash is stored.
	var stored []byte
	if err := s.db.QueryRow(`SELECT token_hash FROM admin_tokens WHERE id = ?`, row.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), token) || string(stored) != string(HashToken(token)) {
		t.Error("admin_tokens does not hold exactly the hash of the token")
	}

	got, err := s.VerifyAdminToken(ctx, token)
	if err != nil {
		t.Fatalf("VerifyAdminToken: %v", err)
	}
	if got.ID != row.ID || got.LastUsed == 0 {
		t.Errorf("VerifyAdminToken returned %+v", got)
	}

	for _, wrong := range []string{"", "nope", token + "x"} {
		if _, err := s.VerifyAdminToken(ctx, wrong); !errors.Is(err, ErrAdminTokenUnknown) {
			t.Errorf("VerifyAdminToken(%q) returned %v, want ErrAdminTokenUnknown", wrong, err)
		}
	}

	// A later token lists after the first, whatever its random id.
	s.now = testClock(time.UnixMilli(1_800_000_000_000))
	second, _, err := s.AddAdminToken(ctx, "desk")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListAdminTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != row.ID || list[1].ID != second.ID {
		t.Fatalf("ListAdminTokens returned %+v", list)
	}

	if err := s.RevokeAdminToken(ctx, row.ID); err != nil {
		t.Fatalf("RevokeAdminToken: %v", err)
	}
	revoked, err := s.VerifyAdminToken(ctx, token)
	if !errors.Is(err, ErrAdminTokenRevoked) || revoked.ID != row.ID || !revoked.Revoked() {
		t.Errorf("a revoked token returned %+v, %v; want ErrAdminTokenRevoked with its row", revoked, err)
	}
	first, err := s.GetAdminToken(ctx, row.ID)
	if err != nil || !first.Revoked() {
		t.Fatalf("GetAdminToken returned %+v, %v", first, err)
	}

	// Revoking again keeps the first time.
	s.now = testClock(time.UnixMilli(9_999_999_999_999))
	if err := s.RevokeAdminToken(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetAdminToken(ctx, row.ID)
	if err != nil || again.RevokedAt != first.RevokedAt {
		t.Errorf("a second revoke moved revoked_at from %d to %d", first.RevokedAt, again.RevokedAt)
	}

	if err := s.RevokeAdminToken(ctx, "adm-nothing"); !errors.Is(err, ErrAdminTokenNotFound) {
		t.Errorf("revoking an unknown id returned %v, want ErrAdminTokenNotFound", err)
	}
	if _, err := s.GetAdminToken(ctx, "adm-nothing"); !errors.Is(err, ErrAdminTokenNotFound) {
		t.Errorf("GetAdminToken of an unknown id returned %v", err)
	}
	if _, _, err := s.AddAdminToken(ctx, "   "); err == nil {
		t.Error("an admin token without a name was accepted")
	}
}

func TestAdminTokenLastUsedIsThrottled(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	_, token, err := s.AddAdminToken(ctx, "founder")
	if err != nil {
		t.Fatal(err)
	}

	s.now = testClock(time.UnixMilli(1_000_000))
	first, err := s.VerifyAdminToken(ctx, token)
	if err != nil || first.LastUsed != 1_000_000 {
		t.Fatalf("first use recorded %d, %v", first.LastUsed, err)
	}
	s.now = testClock(time.UnixMilli(1_030_000))
	second, err := s.VerifyAdminToken(ctx, token)
	if err != nil || second.LastUsed != 1_000_000 {
		t.Errorf("a use 30 s later recorded %d, want it to stay 1000000", second.LastUsed)
	}
	s.now = testClock(time.UnixMilli(1_061_000))
	third, err := s.VerifyAdminToken(ctx, token)
	if err != nil || third.LastUsed != 1_061_000 {
		t.Errorf("a use 61 s later recorded %d, want 1061000", third.LastUsed)
	}
}
