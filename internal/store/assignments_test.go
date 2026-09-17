package store

import (
	"errors"
	"strings"
	"testing"
)

// assignmentStore has two published scenarios, rated 12 and 18, a draft,
// and three devices: tgt-01 and tgt-02 set for 18, tgt-03 for 16.
func assignmentStore(t *testing.T) *Store {
	t.Helper()
	s := openTest(t)
	ctx := t.Context()
	for _, sc := range []Scenario{draft("night-range", 1), draft("zombie-alley", 1), draft("zombie-alley", 2)} {
		if sc.ID == "zombie-alley" {
			sc.AgeRating = "18"
		}
		if err := s.PutScenario(ctx, sc); err != nil {
			t.Fatal(err)
		}
	}
	for _, sc := range []string{"night-range", "zombie-alley"} {
		if _, err := s.PublishScenario(ctx, sc, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"tgt-01", "tgt-02", "tgt-03"} {
		if err := s.UpsertDevice(ctx, testDevice(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetMinAge(ctx, "tgt-03", 16); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMinAge(t *testing.T) {
	s := assignmentStore(t)
	ctx := t.Context()
	if d, _ := s.GetDevice(ctx, "tgt-01"); d.MinAge != 18 {
		t.Errorf("a new device is set for %d, want 18", d.MinAge)
	}
	if d, _ := s.GetDevice(ctx, "tgt-03"); d.MinAge != 16 {
		t.Errorf("tgt-03 is set for %d, want 16", d.MinAge)
	}
	if err := s.SetMinAge(ctx, "tgt-01", 14); err == nil {
		t.Error("an age between the steps was accepted")
	}
	if err := s.SetMinAge(ctx, "tgt-09", 12); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("an unknown device gave %v", err)
	}
	// An upsert of what a device reports keeps the age.
	if err := s.UpsertDevice(ctx, testDevice("tgt-03")); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetDevice(ctx, "tgt-03"); d.MinAge != 16 {
		t.Errorf("the upsert changed the age to %d", d.MinAge)
	}
}

func TestAssignScenario(t *testing.T) {
	s := assignmentStore(t)
	ctx := t.Context()
	if err := s.CreateSession(ctx, Session{ID: "evening"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"tgt-01", "tgt-03"} {
		if err := s.AddSessionDevice(ctx, "evening", id); err != nil {
			t.Fatal(err)
		}
	}

	var age *AgeError
	err := s.AssignScenario(ctx, "evening", "zombie-alley", 1)
	if !errors.Is(err, ErrAgeRating) || !errors.As(err, &age) || age.Rating != 18 || len(age.Devices) != 1 ||
		age.Devices[0] != (DeviceAge{"tgt-03", 16}) || !strings.Contains(err.Error(), "device tgt-03 is set for 16") {
		t.Fatalf("an 18 rated scenario for a 16 device gave %v", err)
	}
	for _, c := range []struct {
		name     string
		session  string
		scenario string
		version  int
		want     error
	}{
		{"a draft", "evening", "zombie-alley", 2, ErrNotPublished},
		{"a missing version", "evening", "night-range", 5, ErrScenarioNotFound},
		{"a missing session", "night", "night-range", 1, ErrSessionNotFound},
	} {
		if err := s.AssignScenario(ctx, c.session, c.scenario, c.version); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}

	if err := s.AssignScenario(ctx, "evening", "night-range", 1); err != nil {
		t.Fatal(err)
	}
	session, _ := s.GetSession(ctx, "evening")
	if session.Scenario != "night-range" || session.ScenarioVersion != 1 {
		t.Errorf("the session plays %s %d", session.Scenario, session.ScenarioVersion)
	}

	// Rated 12: a device set for 6 cannot join or be set lower while it
	// plays, and a started session keeps its scenario.
	if err := s.UpsertDevice(ctx, testDevice("tgt-04")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMinAge(ctx, "tgt-04", 6); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSessionDevice(ctx, "evening", "tgt-04"); !errors.Is(err, ErrAgeRating) {
		t.Errorf("a device set for 6 joined a session rated 12: %v", err)
	}
	if err := s.SetMinAge(ctx, "tgt-01", 6); !errors.Is(err, ErrAgeRating) {
		t.Errorf("a device of a session rated 12 was set for 6: %v", err)
	}
	if err := s.SetMinAge(ctx, "tgt-01", 12); err != nil {
		t.Errorf("setting 12 for a session rated 12: %v", err)
	}
	if err := s.StartSession(ctx, "evening"); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignScenario(ctx, "evening", "night-range", 1); !errors.Is(err, ErrBadTransition) {
		t.Errorf("assigning to a running session gave %v", err)
	}
	if err := s.StopSession(ctx, "evening"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMinAge(ctx, "tgt-01", 0); err != nil {
		t.Errorf("a stopped session still holds the age: %v", err)
	}
}

func TestPendingContent(t *testing.T) {
	s := assignmentStore(t)
	ctx := t.Context()
	for _, id := range []string{"evening", "late", "done"} {
		if err := s.CreateSession(ctx, Session{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(session string, devices ...string) {
		for _, d := range devices {
			if err := s.AddSessionDevice(ctx, session, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("evening", "tgt-01", "tgt-02", "tgt-03")
	add("late", "tgt-01", "tgt-02")
	add("done", "tgt-01")
	for _, a := range []struct {
		session  string
		scenario string
	}{{"evening", "night-range"}, {"late", "zombie-alley"}, {"done", "zombie-alley"}} {
		if err := s.AssignScenario(ctx, a.session, a.scenario, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.StartSession(ctx, "late"); err != nil {
		t.Fatal(err)
	}
	if err := s.StartSession(ctx, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.StopSession(ctx, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDeviceScenarios(ctx, "tgt-02", []Holding{{ScenarioID: "night-range", Version: 1}}); err != nil {
		t.Fatal(err)
	}

	describe := func(list []Pending) string {
		var out []string
		for _, p := range list {
			out = append(out, p.DeviceID+" "+p.SessionID+" "+p.Scenario.ID)
			if p.Scenario.ManifestHash == "" || p.Scenario.Size == 0 {
				t.Errorf("the pending scenario lacks hash or size: %+v", p.Scenario)
			}
		}
		return strings.Join(out, "; ")
	}
	for _, c := range []struct {
		device, session, want string
	}{
		{"", "", "tgt-01 evening night-range; tgt-01 late zombie-alley; tgt-02 late zombie-alley; tgt-03 evening night-range"},
		{"", "evening", "tgt-01 evening night-range; tgt-03 evening night-range"},
		{"tgt-02", "", "tgt-02 late zombie-alley"},
		{"tgt-01", "late", "tgt-01 late zombie-alley"},
		{"tgt-09", "", ""},
	} {
		got, err := s.PendingContent(ctx, c.device, c.session)
		if err != nil {
			t.Fatal(err)
		}
		if describe(got) != c.want {
			t.Errorf("pending for %q %q: %s, want %s", c.device, c.session, describe(got), c.want)
		}
	}
}
