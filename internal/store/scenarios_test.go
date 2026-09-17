package store

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/pkg/scenario"
)

func draft(id string, version int) Scenario {
	return Scenario{
		ID: id, Version: version, Tier: scenario.TierVideo,
		Title:     scenario.Text{"en": "Night Range", "de": "Nachtschießstand"},
		AgeRating: "12", ManifestHash: strings.Repeat("a", 64), Size: 1234, UploadedBy: "founder",
	}
}

// The manifest carries the version (D-037): after version 1 is published,
// the next upload has to carry version 2, and a draft may be replaced.
func TestPutPublishPutYieldsVersionTwo(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if err := s.PutScenario(ctx, draft("night-range", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestPublished(ctx, "night-range"); !errors.Is(err, ErrScenarioNotFound) {
		t.Errorf("a draft counts as published: %v", err)
	}
	published, err := s.PublishScenario(ctx, "night-range", 1)
	if err != nil {
		t.Fatal(err)
	}
	if published.Status != ScenarioPublished || published.PublishedAt != 1_700_000_000_000 || published.Title.In("de") != "Nachtschießstand" {
		t.Errorf("published %+v", published)
	}

	if err := s.PutScenario(ctx, draft("night-range", 1)); !errors.Is(err, ErrVersionTaken) {
		t.Errorf("the published version was stored again: %v", err)
	}
	second := draft("night-range", 2)
	second.ManifestHash = strings.Repeat("b", 64)
	if err := s.PutScenario(ctx, second); err != nil {
		t.Fatal(err)
	}
	second.ManifestHash, second.Size = strings.Repeat("c", 64), 99
	second.Problems = []scenario.Problem{{Field: "scenario.tier", Code: scenario.CodeBadTier, Detail: "no"}}
	if err := s.PutScenario(ctx, second); err != nil {
		t.Fatalf("replacing the draft: %v", err)
	}
	list, err := s.ListScenarios(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Version != 2 || list[0].Status != ScenarioDraft || list[0].ManifestHash != strings.Repeat("c", 64) ||
		list[0].Size != 99 || len(list[0].Problems) != 1 || list[1].Version != 1 || list[1].Problems != nil {
		t.Errorf("the index is %+v", list)
	}
	latest, err := s.LatestPublished(ctx, "night-range")
	if err != nil || latest.Version != 1 || latest.ManifestHash != strings.Repeat("a", 64) {
		t.Errorf("latest published is %+v, %v", latest, err)
	}

	if _, err := s.PublishScenario(ctx, "night-range", 2); !errors.Is(err, ErrHasProblems) {
		t.Errorf("a draft with problems was published: %v", err)
	}
	second.Problems = nil
	if err := s.PutScenario(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishScenario(ctx, "night-range", 2); err != nil {
		t.Fatal(err)
	}
	if latest, _ := s.LatestPublished(ctx, "night-range"); latest.Version != 2 {
		t.Errorf("latest published is version %d", latest.Version)
	}
}

// Nothing published changes or goes away in S01 (D-037).
func TestPublishedVersionsStay(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	for _, v := range []int{1, 2, 3} {
		if err := s.PutScenario(ctx, draft("night-range", v)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PublishScenario(ctx, "night-range", 2); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"publish twice", second(s.PublishScenario(ctx, "night-range", 2)), ErrPublished},
		{"publish a stale draft", second(s.PublishScenario(ctx, "night-range", 1)), ErrVersionTaken},
		{"publish a missing version", second(s.PublishScenario(ctx, "night-range", 9)), ErrScenarioNotFound},
		{"delete the published version", s.DeleteScenario(ctx, "night-range", 2), ErrPublished},
		{"delete a missing version", s.DeleteScenario(ctx, "night-range", 9), ErrScenarioNotFound},
		{"replace the published version", s.PutScenario(ctx, draft("night-range", 2)), ErrVersionTaken},
		{"store an unusable id", s.PutScenario(ctx, draft("Night Range", 1)), nil},
	}
	for _, c := range cases {
		if c.want == nil {
			if c.err == nil {
				t.Errorf("%s: accepted", c.name)
			}
			continue
		}
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, c.err, c.want)
		}
	}
	if err := s.DeleteScenario(ctx, "night-range", 1); err != nil {
		t.Errorf("deleting a draft: %v", err)
	}
	if _, err := s.GetScenario(ctx, "night-range", 1); !errors.Is(err, ErrScenarioNotFound) {
		t.Errorf("the deleted draft is still there: %v", err)
	}
	kept, err := s.GetScenario(ctx, "night-range", 2)
	if err != nil || kept.Status != ScenarioPublished {
		t.Errorf("the published version is %+v, %v", kept, err)
	}
	if _, err := s.db.Exec(`UPDATE scenarios SET status = 'retired' WHERE version = 3`); err == nil {
		t.Error("the status column takes any word")
	}
}

func second[T any](_ T, err error) error {
	return err
}

func TestDeviceHoldings(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	s := openTest(t, WithClock(func() time.Time { return now }))
	ctx := t.Context()
	for _, id := range []string{"tgt-01", "tgt-02"} {
		if err := s.UpsertDevice(ctx, testDevice(id)); err != nil {
			t.Fatal(err)
		}
	}
	hold := func(pairs ...any) []Holding {
		var list []Holding
		for i := 0; i < len(pairs); i += 2 {
			list = append(list, Holding{ScenarioID: pairs[i].(string), Version: pairs[i+1].(int)})
		}
		return list
	}
	describe := func(list []Holding) string {
		var out []string
		for _, h := range list {
			state := "gone"
			if h.Current {
				state = "held"
			}
			out = append(out, strings.Join([]string{h.DeviceID, h.ScenarioID, strconv.Itoa(h.Version), state, strconv.FormatInt(h.InstalledAt-1_700_000_000_000, 10)}, " "))
		}
		return strings.Join(out, "; ")
	}

	if err := s.RecordDeviceScenarios(ctx, "tgt-01", hold("zombie-alley", 1, "night-range", 2)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := s.RecordDeviceScenarios(ctx, "tgt-01", hold("night-range", 2)); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeviceScenarios(ctx, "tgt-01")
	if err != nil {
		t.Fatal(err)
	}
	if want := "tgt-01 night-range 2 held 0; tgt-01 zombie-alley 1 gone 0"; describe(got) != want {
		t.Errorf("after the second report: %s, want %s", describe(got), want)
	}

	now = now.Add(time.Second)
	if err := s.SetDeviceScenario(ctx, "tgt-01", "night-range", 3, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceScenario(ctx, "tgt-01", "zombie-alley", 1, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeviceScenario(ctx, "tgt-01", "night-range", 2, false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.DeviceScenarios(ctx, "tgt-01")
	if want := "tgt-01 night-range 3 held 2000; tgt-01 night-range 2 gone 0; tgt-01 zombie-alley 1 held 2000"; describe(got) != want {
		t.Errorf("after the content events: %s, want %s", describe(got), want)
	}

	if err := s.RecordDeviceScenarios(ctx, "tgt-02", hold("night-range", 3)); err != nil {
		t.Fatal(err)
	}
	holders, err := s.ScenarioHoldings(ctx, "night-range")
	if err != nil {
		t.Fatal(err)
	}
	if want := "tgt-01 night-range 3 held 2000; tgt-01 night-range 2 gone 0; tgt-02 night-range 3 held 2000"; describe(holders) != want {
		t.Errorf("holders: %s, want %s", describe(holders), want)
	}
	for _, c := range []struct {
		device  string
		version int
		want    bool
	}{{"tgt-01", 3, true}, {"tgt-01", 2, false}, {"tgt-02", 2, false}, {"tgt-09", 3, false}} {
		if held, err := s.HoldsScenario(ctx, c.device, "night-range", c.version); err != nil || held != c.want {
			t.Errorf("%s holds night-range %d: %v, %v", c.device, c.version, held, err)
		}
	}

	if err := s.RecordDeviceScenarios(ctx, "tgt-02", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.DeviceScenarios(ctx, "tgt-02"); slices.ContainsFunc(got, func(h Holding) bool { return h.Current }) {
		t.Error("an empty report keeps a version current")
	}
	if err := s.RecordDeviceScenarios(ctx, "tgt-09", hold("night-range", 3)); err == nil {
		t.Error("holdings of an unknown device were recorded")
	}
}
