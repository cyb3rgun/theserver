package scenario

import (
	"math"
	"slices"
	"sort"
)

// The rule engine of docs/scenario.md section 7 (D-044). It is the one place
// that says what a shot is worth, when a target times out and which clip
// plays; theserver uses it to replay a run, the editor shows it in the
// preview, and theclient grows from it.
//
// The engine runs a scenario against a list of shots and writes a trace: the
// journal of section 7 with the score after every decision. The same engine
// in JavaScript, internal/admin/static/rules.js, must write the same trace
// for the same input, which a test and the browser proof check. That is why
// every number here is computed the way the script computes it: integers
// where the model has integers, and one float64 division in the polygon
// test, in the same order.

// The kinds of an Event.
const (
	// EventAppear is a target showing up.
	EventAppear = "appear"
	// EventHit is a shot inside a live zone.
	EventHit = "hit"
	// EventMiss is a shot that met no live zone.
	EventMiss = "miss"
	// EventTimeout is an appearance that was not cleared in time.
	EventTimeout = "timeout"
	// EventClear is an appearance that took its last needed hit.
	EventClear = "clear"
	// EventState is the media state that plays from now on.
	EventState = "state"
	// EventEnd ends the run: done, timeout, lives.
	EventEnd = "end"
)

// The reasons an EventEnd carries.
const (
	// EndDone is the end of the scenario clock.
	EndDone = "done"
	// EndTimeout is an appearance whose on_timeout is end.
	EndTimeout = "timeout"
	// EndLives is the last life lost.
	EndLives = "lives"
)

// A Shot is one trigger pull: the scenario time and the point on the canvas.
type Shot struct {
	TMs int `json:"t_ms"`
	X   int `json:"x"`
	Y   int `json:"y"`
}

// An Event is one decision of the engine, the journal entry of section 7.
// Score and Lives are the state after the decision.
type Event struct {
	TMs        int    `json:"t_ms"`
	Kind       string `json:"kind"`
	Appearance string `json:"appearance,omitempty"`
	Zone       string `json:"zone,omitempty"`
	ZoneClass  string `json:"zone_class,omitempty"`
	State      string `json:"state,omitempty"`
	Points     int    `json:"points,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Score      int    `json:"score"`
	Lives      int    `json:"lives"`
}

// A Trace is a whole run: every decision and what it came to.
type Trace struct {
	Events   []Event `json:"events"`
	Score    int     `json:"score"`
	Hits     int     `json:"hits"`
	Misses   int     `json:"misses"`
	Timeouts int     `json:"timeouts"`
	Lives    int     `json:"lives"`
	EndedMs  int     `json:"ended_ms"`
}

// Run plays a scenario against shots and returns the trace. The shots are
// taken in the order they are given; a shot before the previous one is moved
// up to it, so a trace never runs backwards.
func Run(m Manifest, shots []Shot) Trace {
	r := &run{m: m, rules: Rules{}, playing: -1}
	if m.Rules != nil {
		r.rules = *m.Rules
	}
	r.lives = r.rules.Lives
	r.durationMs = m.Scenario.DurationS * 1000
	r.startAt = make([]int, len(m.Appearances))
	r.endAt = make([]int, len(m.Appearances))
	r.left = make([]int, len(m.Appearances))
	r.done = make([]bool, len(m.Appearances))
	r.started = make([]bool, len(m.Appearances))
	for i, a := range m.Appearances {
		r.startAt[i], r.endAt[i], r.left[i] = a.TStartMs, a.TEndMs, a.RequiredHits
	}
	r.events = []Event{}

	now := 0
	for _, shot := range shots {
		if shot.TMs > now {
			now = shot.TMs
		}
		r.advance(now)
		if r.ended {
			break
		}
		r.shoot(now, shot)
	}
	if !r.ended {
		r.advance(r.durationMs)
	}
	if !r.ended {
		r.end(r.durationMs, EndDone)
	}
	return Trace{
		Events: r.events, Score: r.score, Hits: r.hits, Misses: r.misses,
		Timeouts: r.timeouts, Lives: r.lives, EndedMs: r.endedMs,
	}
}

// run is one play of a scenario.
type run struct {
	m          Manifest
	rules      Rules
	durationMs int

	startAt []int
	endAt   []int
	left    []int
	done    []bool
	started []bool

	// playing is the follow up on the screen, -1 when none.
	playing  int
	playUntl int
	state    string

	score, hits, misses, timeouts, lives int
	events                               []Event
	ended                                bool
	endedMs                              int
}

func (r *run) add(event Event) {
	event.Score, event.Lives = r.score, r.lives
	r.events = append(r.events, event)
}

// charge changes the score, never below zero and never above the cap.
func (r *run) charge(points int) {
	r.score += points
	if r.score < 0 {
		r.score = 0
	}
	if r.rules.ScoreCap > 0 && r.score > r.rules.ScoreCap {
		r.score = r.rules.ScoreCap
	}
}

func (r *run) setState(t int, state string) {
	if state == "" || state == r.state {
		return
	}
	r.state = state
	r.add(Event{TMs: t, Kind: EventState, State: state})
}

func (r *run) end(t int, reason string) {
	if r.ended {
		return
	}
	r.ended, r.endedMs = true, t
	r.add(Event{TMs: t, Kind: EventEnd, Reason: reason})
}

// advance works through everything that happens up to and including t: a
// follow up that ends, an appearance that times out, one that shows up, and
// the end of the scenario, in that order at the same moment.
func (r *run) advance(t int) {
	for !r.ended {
		when, what, which := r.next()
		if what == "" || when > t {
			return
		}
		switch what {
		case "followup":
			r.finishFollowup(when, which)
		case "timeout":
			r.timeout(when, which)
		case "appear":
			r.appear(when, which)
		case "end":
			r.end(when, EndDone)
		}
	}
}

// next is the first thing that happens, with the order of a moment.
func (r *run) next() (int, string, int) {
	when, what, which := 0, "", -1
	take := func(t int, kind string, index int) {
		if what == "" || t < when {
			when, what, which = t, kind, index
		}
	}
	if r.playing >= 0 {
		take(r.playUntl, "followup", r.playing)
	}
	for i := range r.m.Appearances {
		if r.started[i] && !r.done[i] {
			take(r.endAt[i], "timeout", i)
		}
	}
	for i := range r.m.Appearances {
		if !r.started[i] {
			take(r.startAt[i], "appear", i)
		}
	}
	if r.durationMs > 0 {
		take(r.durationMs, "end", -1)
	}
	return when, what, which
}

func (r *run) appear(t, i int) {
	a := r.m.Appearances[i]
	r.started[i] = true
	r.add(Event{TMs: t, Kind: EventAppear, Appearance: a.ID})
	r.setState(t, a.MediaState)
}

// timeout is section 7.3 with the rules settled in S01-B08: the switch in
// the rules turns timeout costs on for the whole scenario, on_timeout says
// per appearance whether this one costs and whether it ends the run.
func (r *run) timeout(t, i int) {
	a := r.m.Appearances[i]
	r.done[i] = true
	r.timeouts++
	costs := r.rules.TimeoutCountsAsHit && a.OnTimeout != "nothing"
	if costs {
		r.charge(-r.rules.TimeoutPenalty)
		if r.rules.Lives > 0 && r.lives > 0 {
			r.lives--
		}
	}
	r.add(Event{TMs: t, Kind: EventTimeout, Appearance: a.ID})
	switch {
	case costs && r.rules.Lives > 0 && r.lives == 0:
		r.end(t, EndLives)
	case a.OnTimeout == "end":
		r.end(t, EndTimeout)
	}
}

// shoot is section 7.2: the first live zone in manifest order that holds the
// point is the hit; anything else is a miss.
func (r *run) shoot(t int, shot Shot) {
	zone, appearance := r.zoneAt(t, shot)
	if zone < 0 {
		r.misses++
		r.charge(-r.rules.MissPenalty)
		r.add(Event{TMs: t, Kind: EventMiss})
		return
	}
	z := r.m.Zones[zone]
	points := r.rules.PointsPerHitDefault
	if z.PointsValue != nil {
		points = *z.PointsValue
	}
	r.hits++
	r.charge(points)
	a := r.m.Appearances[appearance]
	r.left[appearance]--
	r.add(Event{TMs: t, Kind: EventHit, Appearance: a.ID, Zone: z.ID, ZoneClass: z.ZoneClass, Points: points})
	if r.left[appearance] > 0 {
		return
	}
	r.done[appearance] = true
	r.add(Event{TMs: t, Kind: EventClear, Appearance: a.ID, Zone: z.ID, ZoneClass: z.ZoneClass})
	r.startFollowup(t, appearance, z.ZoneClass)
}

// startFollowup plays the reaction of the character, if it has one for this
// zone class.
func (r *run) startFollowup(t, appearance int, class string) {
	a := r.m.Appearances[appearance]
	for i, f := range r.m.Reaction.Followups {
		if f.Appearance != a.ID || f.ZoneClass != class {
			continue
		}
		r.playing, r.playUntl = i, t+f.DurationMs
		r.setState(t, f.MediaState)
		return
	}
}

// finishFollowup is the then of a follow up: the figure is done, a state
// comes back, or the clock moves on to the next target.
func (r *run) finishFollowup(t, index int) {
	f := r.m.Reaction.Followups[index]
	r.playing, r.playUntl = -1, 0
	switch {
	case f.Then == "next":
		r.jump(t)
	case len(f.Then) > 5 && f.Then[:5] == "back:":
		r.setState(t, f.Then[5:])
	}
}

// jump moves the window of the next appearance that has not started to now,
// keeping its length.
func (r *run) jump(t int) {
	next := -1
	for i := range r.m.Appearances {
		if r.started[i] || r.startAt[i] <= t {
			continue
		}
		if next < 0 || r.startAt[i] < r.startAt[next] {
			next = i
		}
	}
	if next < 0 {
		return
	}
	shift := r.startAt[next] - t
	r.startAt[next] -= shift
	r.endAt[next] -= shift
}

// zoneAt finds the zone a shot meets: the first in manifest order that
// belongs to an appearance that is up and not cleared.
func (r *run) zoneAt(t int, shot Shot) (int, int) {
	for i, z := range r.m.Zones {
		owner := r.ownerOf(z.ID)
		if owner < 0 || !r.started[owner] || r.done[owner] || t < r.startAt[owner] || t >= r.endAt[owner] {
			continue
		}
		points, radius := ShapeAt(z, t)
		if InShape(z.Shape, points, radius, shot.X, shot.Y) {
			return i, owner
		}
	}
	return -1, -1
}

func (r *run) ownerOf(zone string) int {
	for i, a := range r.m.Appearances {
		if slices.Contains(a.Zones, zone) {
			return i
		}
	}
	return -1
}

// ShapeAt is the shape of a zone at the scenario time t: its keyframes
// interpolated and rounded, or its own points when it has none.
func ShapeAt(z Zone, t int) ([][]int, int) {
	frames := z.Keyframes
	if len(frames) == 0 {
		return z.Points, z.Radius
	}
	if t <= frames[0].TMs {
		return frames[0].Points, frames[0].Radius
	}
	last := frames[len(frames)-1]
	if t >= last.TMs {
		return last.Points, last.Radius
	}
	index := sort.Search(len(frames), func(i int) bool { return frames[i].TMs >= t })
	b := frames[index]
	a := frames[index-1]
	span := b.TMs - a.TMs
	at := 0.0
	if span > 0 {
		at = float64(t-a.TMs) / float64(span)
	}
	points := make([][]int, 0, len(a.Points))
	for j, p := range a.Points {
		q := p
		if j < len(b.Points) {
			q = b.Points[j]
		}
		points = append(points, []int{
			int(math.Round(float64(p[0]) + float64(q[0]-p[0])*at)),
			int(math.Round(float64(p[1]) + float64(q[1]-p[1])*at)),
		})
	}
	radius := int(math.Round(float64(a.Radius) + float64(b.Radius-a.Radius)*at))
	return points, radius
}

// InShape reports whether the point x, y lies in the shape. A point on the
// edge counts as inside; overlapping zones are settled by manifest order.
func InShape(shape string, points [][]int, radius, x, y int) bool {
	switch shape {
	case "circle":
		if len(points) < 1 {
			return false
		}
		dx, dy := x-points[0][0], y-points[0][1]
		return dx*dx+dy*dy <= radius*radius
	case "rect":
		if len(points) < 2 {
			return false
		}
		x1, x2 := min(points[0][0], points[1][0]), max(points[0][0], points[1][0])
		y1, y2 := min(points[0][1], points[1][1]), max(points[0][1], points[1][1])
		return x >= x1 && x <= x2 && y >= y1 && y <= y2
	default:
		hit := false
		for i, j := 0, len(points)-1; i < len(points); j, i = i, i+1 {
			xi, yi := points[i][0], points[i][1]
			xj, yj := points[j][0], points[j][1]
			if (yi > y) != (yj > y) && float64(x) < float64((xj-xi)*(y-yi))/float64(yj-yi)+float64(xi) {
				hit = !hit
			}
		}
		return hit
	}
}
