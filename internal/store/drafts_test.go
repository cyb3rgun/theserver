package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDraftsAreKeptAndChanged(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	d, err := s.CreateDraft(ctx, Draft{
		ID: "d-01", ScenarioID: "night-range", CreatedBy: "founder",
		Manifest: json.RawMessage(`{"scenario":{"id":"night-range"}}`),
		Media:    map[string]DraftMedia{"main.mp4": {Name: "main.mp4", Size: 12, SHA256: "ab"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.CreatedAt == 0 || d.UpdatedAt == 0 || d.UpdatedBy != "founder" || d.PublishedVersion != 0 {
		t.Errorf("the new draft reads %+v", d)
	}
	if names := d.MediaNames(); len(names) != 1 || names[0] != "main.mp4" {
		t.Errorf("the media are %v", names)
	}

	// A second draft of the same scenario is its own row.
	if _, err := s.CreateDraft(ctx, Draft{ID: "d-02", ScenarioID: "night-range", CreatedBy: "mausi"}); err != nil {
		t.Fatalf("a second draft: %v", err)
	}
	if _, err := s.CreateDraft(ctx, Draft{ID: "d-01", ScenarioID: "other"}); err == nil {
		t.Error("the same id twice was taken")
	}

	d.Manifest = json.RawMessage(`{"scenario":{"id":"night-range","duration_s":20}}`)
	d.Media["cover.png"] = DraftMedia{Name: "cover.png", Size: 4}
	changed, err := s.UpdateDraft(ctx, d, "mausi")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if changed.UpdatedBy != "mausi" || len(changed.Media) != 2 || changed.CreatedBy != "founder" {
		t.Errorf("the changed draft reads %+v", changed)
	}
	if !json.Valid(changed.Manifest) || string(changed.Manifest) != string(d.Manifest) {
		t.Errorf("the manifest reads %s", changed.Manifest)
	}

	// The list holds both, the one changed last first.
	list, err := s.ListDrafts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "d-01" && list[1].ID != "d-01" {
		t.Errorf("the list holds %+v", list)
	}

	promoted, err := s.PromoteDraft(ctx, "d-01", 3, "founder")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted.PublishedVersion != 3 {
		t.Errorf("the promoted draft reads %+v", promoted)
	}

	if err := s.DeleteDraft(ctx, "d-01"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, err := range []error{
		func() error { _, err := s.GetDraft(ctx, "d-01"); return err }(),
		s.DeleteDraft(ctx, "d-01"),
		func() error { _, err := s.PromoteDraft(ctx, "d-01", 1, "x"); return err }(),
		func() error { _, err := s.UpdateDraft(ctx, Draft{ID: "d-01"}, "x"); return err }(),
	} {
		if !errors.Is(err, ErrDraftNotFound) {
			t.Errorf("a draft that is gone gave %v", err)
		}
	}

	// A manifest that is not JSON is refused before it reaches the table.
	if _, err := s.CreateDraft(ctx, Draft{ID: "d-03", ScenarioID: "x", Manifest: json.RawMessage("{")}); err == nil {
		t.Error("a manifest that is not JSON was stored")
	}
}

// The lock of a draft (D-050): taking it, refreshing it, seeing it refused,
// taking it over, releasing it, and a lock that falls away because nobody
// refreshed it.
func TestDraftLock(t *testing.T) {
	start := time.UnixMilli(1_700_000_000_000).UTC()
	now := start
	s := openTest(t, WithClock(func() time.Time { return now }))
	ctx := t.Context()
	const ttl = 5 * time.Minute

	if _, err := s.CreateDraft(ctx, Draft{ID: "d-01", ScenarioID: "night-range", CreatedBy: "founder"}); err != nil {
		t.Fatal(err)
	}
	if d, err := s.GetDraft(ctx, "d-01"); err != nil {
		t.Fatal(err)
	} else if d.Lock.Held(s.Now(), ttl) {
		t.Errorf("a new draft is held: %+v", d.Lock)
	}

	// Taking it.
	_, lock, err := s.LockDraft(ctx, "d-01", "at-1", "founder", ttl, false)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if lock.By != "at-1" || lock.Name != "founder" || !lock.Held(s.Now(), ttl) {
		t.Fatalf("the lock reads %+v", lock)
	}

	// Refreshing it a minute later moves the stamp; the same token never
	// meets its own lock.
	now = start.Add(time.Minute)
	_, again, err := s.LockDraft(ctx, "d-01", "at-1", "founder", ttl, false)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if again.At != now.UnixMilli() || again.By != "at-1" {
		t.Errorf("the refreshed lock reads %+v", again)
	}

	// Somebody else is refused and is told who holds it.
	_, held, err := s.LockDraft(ctx, "d-01", "at-2", "mausi", ttl, false)
	if !errors.Is(err, ErrDraftLocked) {
		t.Fatalf("the second admin got %v", err)
	}
	if held.Name != "founder" || held.By != "at-1" {
		t.Errorf("the refusal names %+v", held)
	}

	// A release by the wrong token changes nothing.
	d, err := s.UnlockDraft(ctx, "d-01", "at-2")
	if err != nil {
		t.Fatal(err)
	}
	if d.Lock.By != "at-1" {
		t.Errorf("the wrong token released the lock: %+v", d.Lock)
	}

	// Taking it over walks in.
	_, over, err := s.LockDraft(ctx, "d-01", "at-2", "mausi", ttl, true)
	if err != nil {
		t.Fatalf("take over: %v", err)
	}
	if over.By != "at-2" || over.Name != "mausi" {
		t.Errorf("the taken over lock reads %+v", over)
	}

	// A lock nobody refreshes falls away, and the next person walks in
	// without asking to take it over.
	now = now.Add(ttl + time.Millisecond)
	d, err = s.GetDraft(ctx, "d-01")
	if err != nil {
		t.Fatal(err)
	}
	if d.Lock.Held(s.Now(), ttl) {
		t.Errorf("a lock nobody refreshed is still held: %+v", d.Lock)
	}
	if _, free, err := s.LockDraft(ctx, "d-01", "at-1", "founder", ttl, false); err != nil {
		t.Fatalf("a stale lock refused the next person: %v", err)
	} else if free.By != "at-1" {
		t.Errorf("the lock reads %+v", free)
	}

	// Releasing it by its holder empties it.
	d, err = s.UnlockDraft(ctx, "d-01", "at-1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Lock.By != "" || d.Lock.At != 0 || d.Lock.Held(s.Now(), ttl) {
		t.Errorf("the released lock reads %+v", d.Lock)
	}

	// A draft that is not there is not found, and a lock without a token is
	// refused.
	if _, _, err := s.LockDraft(ctx, "d-99", "at-1", "founder", ttl, false); !errors.Is(err, ErrDraftNotFound) {
		t.Errorf("locking an unknown draft gave %v", err)
	}
	if _, _, err := s.LockDraft(ctx, "d-01", "", "", ttl, false); err == nil {
		t.Error("a lock without an admin token was taken")
	}
}
