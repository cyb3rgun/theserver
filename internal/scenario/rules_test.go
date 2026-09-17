package scenario_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/scenario/scenariotest"
)

// runsFile is the fixture of the rule engine: the shots of every run and the
// trace they must produce. The same file is what the browser proof compares
// the JavaScript engine against (D-044).
const runsFile = "rules/interactive.json"

type recordedRun struct {
	Name  string          `json:"name"`
	Shots []scenario.Shot `json:"shots"`
	Trace scenario.Trace  `json:"trace"`
}

type recordedRuns struct {
	Scenario string        `json:"scenario"`
	Runs     []recordedRun `json:"runs"`
}

// shotScripts are the runs the fixture is played with: one that lets the
// first zombie time out and clears the second, and one that takes the torso
// first, so that a follow up comes back to another state.
var shotScripts = []recordedRun{
	{
		Name: "a timeout and a cleared target",
		Shots: []scenario.Shot{
			{TMs: 1000, X: 540, Y: 960},  // nothing is up yet
			{TMs: 9500, X: 310, Y: 355},  // the head of the second zombie
			{TMs: 10000, X: 700, Y: 700}, // beside it
			{TMs: 11000, X: 340, Y: 370}, // the head again, which clears it
			{TMs: 19000, X: 100, Y: 100}, // after everything
		},
	},
	{
		Name: "a torso hit that comes back to walking",
		Shots: []scenario.Shot{
			{TMs: 3500, X: 520, Y: 500},  // the torso of the first zombie
			{TMs: 9200, X: 305, Y: 352},  // the head of the second
			{TMs: 12900, X: 365, Y: 382}, // and again, after the last keyframe
		},
	},
}

func TestRulesOfSectionSevenOnTheFixture(t *testing.T) {
	pkg := load(t, scenariotest.Interactive)
	file := filepath.Join(scenariotest.Dir("."), filepath.FromSlash(runsFile))

	want := recordedRuns{Scenario: pkg.Manifest.Scenario.ID}
	for _, script := range shotScripts {
		want.Runs = append(want.Runs, recordedRun{
			Name: script.Name, Shots: script.Shots, Trace: scenario.Run(pkg.Manifest, script.Shots),
		})
	}
	if *update {
		data, err := json.MarshalIndent(want, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v; run go test ./internal/scenario -update", err)
	}
	var recorded recordedRuns
	if err := json.Unmarshal(data, &recorded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorded, want) {
		t.Fatalf("the engine no longer writes the recorded trace; run go test ./internal/scenario -update to see the difference")
	}

	// The first run is read here in full, so that a change of the rules is
	// visible in this test and not only in the fixture.
	first := recorded.Runs[0].Trace
	if first.Score != 100 || first.Hits != 2 || first.Misses != 3 || first.Timeouts != 1 || first.Lives != 2 {
		t.Errorf("the first run came to %+v", first)
	}
	kinds := map[string]int{}
	for _, e := range first.Events {
		kinds[e.Kind]++
	}
	for kind, count := range map[string]int{
		scenario.EventAppear: 2, scenario.EventHit: 2, scenario.EventMiss: 3,
		scenario.EventTimeout: 1, scenario.EventClear: 1, scenario.EventEnd: 1,
	} {
		if kinds[kind] != count {
			t.Errorf("the first run holds %d %s events, want %d", kinds[kind], kind, count)
		}
	}
	second := recorded.Runs[1].Trace
	if second.Score != 150 || second.Hits != 3 || second.Timeouts != 0 {
		t.Errorf("the second run came to %+v", second)
	}
	states := []string{}
	for _, e := range second.Events {
		if e.Kind == scenario.EventState {
			states = append(states, e.State)
		}
	}
	if len(states) != 4 || states[0] != "walk" || states[1] != "die-body" || states[2] != "walk" || states[3] != "die-head" {
		t.Errorf("the second run played the states %v", states)
	}
}

// The rules that the fixture does not reach: the cap, the floor, a miss that
// costs, lives that run out, and an appearance that ends the scenario.
func TestRulesOfTheEdges(t *testing.T) {
	base := func() scenario.Manifest {
		m := load(t, scenariotest.Interactive).Manifest
		rules := *m.Rules
		m.Rules = &rules
		return m
	}
	hitHead := scenario.Shot{TMs: 4000, X: 560, Y: 360}

	t.Run("a cap holds the score", func(t *testing.T) {
		m := base()
		m.Rules.ScoreCap = 60
		trace := scenario.Run(m, []scenario.Shot{hitHead})
		if trace.Score != 60 {
			t.Errorf("the score is %d", trace.Score)
		}
	})
	t.Run("the score never falls below zero", func(t *testing.T) {
		m := base()
		m.Rules.MissPenalty = 30
		trace := scenario.Run(m, []scenario.Shot{{TMs: 500, X: 10, Y: 10}, {TMs: 600, X: 10, Y: 10}})
		if trace.Score != 0 || trace.Misses != 2 {
			t.Errorf("the run came to %+v", trace)
		}
	})
	t.Run("a switched off timeout costs nothing", func(t *testing.T) {
		m := base()
		m.Rules.TimeoutCountsAsHit = false
		trace := scenario.Run(m, nil)
		if trace.Score != 0 || trace.Lives != 3 || trace.Timeouts != 2 {
			t.Errorf("the run came to %+v", trace)
		}
	})
	t.Run("the last life ends the run", func(t *testing.T) {
		m := base()
		m.Rules.Lives = 1
		trace := scenario.Run(m, nil)
		if trace.Lives != 0 || trace.EndedMs != 6000 {
			t.Errorf("the run came to %+v", trace)
		}
		last := trace.Events[len(trace.Events)-1]
		if last.Kind != scenario.EventEnd || last.Reason != scenario.EndLives {
			t.Errorf("the run ended with %+v", last)
		}
	})
	t.Run("an appearance can end the scenario", func(t *testing.T) {
		m := base()
		m.Appearances[0].OnTimeout = "end"
		m.Rules.TimeoutCountsAsHit = false
		trace := scenario.Run(m, nil)
		last := trace.Events[len(trace.Events)-1]
		if last.Reason != scenario.EndTimeout || trace.EndedMs != 6000 {
			t.Errorf("the run ended with %+v at %d", last, trace.EndedMs)
		}
	})
	t.Run("a shot after the end changes nothing", func(t *testing.T) {
		m := base()
		trace := scenario.Run(m, []scenario.Shot{{TMs: 30000, X: 560, Y: 360}})
		if trace.Hits != 0 || trace.EndedMs != 20000 {
			t.Errorf("the run came to %+v", trace)
		}
	})
	t.Run("a zone of a cleared appearance is no longer live", func(t *testing.T) {
		m := base()
		trace := scenario.Run(m, []scenario.Shot{hitHead, {TMs: 4500, X: 560, Y: 500}})
		if trace.Hits != 1 || trace.Misses != 1 {
			t.Errorf("the run came to %+v", trace)
		}
	})
}

// The shapes the engine hits are the shapes the editor draws, down to the
// rounding of an interpolated keyframe.
func TestShapeAtAndInShape(t *testing.T) {
	m := load(t, scenariotest.Interactive).Manifest
	head := m.Zones[0]
	points, _ := scenario.ShapeAt(head, 4000)
	want := [][]int{{540, 305}, {620, 305}, {620, 425}, {540, 425}}
	if !reflect.DeepEqual(points, want) {
		t.Errorf("the head at 4000 ms is %v", points)
	}
	if points, _ := scenario.ShapeAt(head, 1000); !reflect.DeepEqual(points, head.Keyframes[0].Points) {
		t.Errorf("before the first keyframe the head is %v", points)
	}
	if points, _ := scenario.ShapeAt(head, 9000); !reflect.DeepEqual(points, head.Keyframes[1].Points) {
		t.Errorf("after the last keyframe the head is %v", points)
	}

	circle := m.Zones[2]
	points, radius := scenario.ShapeAt(circle, 10500)
	if !reflect.DeepEqual(points, [][]int{{330, 365}}) || radius != 65 {
		t.Errorf("the circle at 10500 ms is %v with radius %d", points, radius)
	}
	for _, c := range []struct {
		name  string
		shape string
		in    bool
		x, y  int
	}{
		{"the centre", "circle", true, 330, 365},
		{"the edge", "circle", true, 330 + 65, 365},
		{"just outside", "circle", false, 330 + 66, 365},
		{"inside the rect", "rect", true, 500, 500},
		{"on the corner", "rect", true, 480, 420},
		{"outside the rect", "rect", false, 479, 420},
		{"inside the polygon", "polygon", true, 560, 360},
		{"outside the polygon", "polygon", false, 700, 360},
	} {
		var points [][]int
		var radius int
		switch c.shape {
		case "circle":
			points, radius = scenario.ShapeAt(circle, 10500)
		case "rect":
			points, radius = scenario.ShapeAt(m.Zones[1], 0)
		default:
			points, radius = scenario.ShapeAt(head, 4000)
		}
		if got := scenario.InShape(c.shape, points, radius, c.x, c.y); got != c.in {
			t.Errorf("%s: InShape gave %v", c.name, got)
		}
	}
}
