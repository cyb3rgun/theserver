package store

import (
	"encoding/json"
	"errors"
	"testing"
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
