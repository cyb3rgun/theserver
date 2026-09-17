package simtarget

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/cyb3rgun/theserver/internal/protocol"
)

// A Draft is an event before the journal gives it a sequence number and an
// id.
type Draft struct {
	Kind string
	Data cbor.RawMessage
}

// HitProbability is the share of shots that hit.
const HitProbability = 0.7

// zones and the points a hit in them is worth.
var zones = []struct {
	name   string
	points int64
}{
	{"head", 100},
	{"torso", 50},
	{"arm", 25},
	{"leg", 25},
}

// A Generator invents what a target would report: for every shot a shot
// event, then a hit with probability HitProbability or else a miss, from
// controllers taken in turn.
type Generator struct {
	rng         *rand.Rand
	controllers []string
	turn        int
	cseq        map[string]uint64
	queue       []Draft
	started     time.Time
}

// NewGenerator returns a generator with the given number of controllers,
// named ctl-01 and so on. The same seed gives the same events.
func NewGenerator(controllers int, seed uint64, started time.Time) *Generator {
	controllers = max(controllers, 1)
	names := make([]string, controllers)
	for i := range names {
		names[i] = fmt.Sprintf("ctl-%02d", i+1)
	}
	return &Generator{
		rng:         rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		controllers: names,
		cseq:        map[string]uint64{},
		started:     started,
	}
}

// Controllers lists the controller ids the generator uses.
func (g *Generator) Controllers() []string {
	return g.controllers
}

// Next returns the next shot or outcome.
func (g *Generator) Next() Draft {
	if len(g.queue) == 0 {
		g.queue = g.shot()
	}
	d := g.queue[0]
	g.queue = g.queue[1:]
	return d
}

// shot returns a shot and its outcome.
func (g *Generator) shot() []Draft {
	ctl := g.controllers[g.turn%len(g.controllers)]
	g.turn++
	g.cseq[ctl]++
	cseq := g.cseq[ctl]

	shot := ShotDraft(protocol.KindShot, ctl, cseq)
	if g.rng.Float64() >= HitProbability {
		return []Draft{shot, ShotDraft(protocol.KindMiss, ctl, cseq)}
	}
	zone := zones[g.rng.IntN(len(zones))]
	hit := mustDraft(protocol.KindHit, protocol.HitData{
		Ctl:  ctl,
		Cseq: cseq,
		X:    round3(g.rng.Float64()),
		Y:    round3(g.rng.Float64()),
		Zone: zone.name,
		Pts:  zone.points,
	})
	return []Draft{shot, hit}
}

// Health returns the periodic health report with the scenario versions the
// target holds; nil leaves them out.
func (g *Generator) Health(now time.Time, held []protocol.Holding) Draft {
	return mustDraft(protocol.KindHealth, protocol.HealthData{
		Up:   uint64(now.Sub(g.started).Seconds()),
		RSSI: -45 - int64(g.rng.IntN(30)),
		Temp: round3(38 + 8*g.rng.Float64()),
		Free: 120_000 + uint64(g.rng.IntN(40_000)),
		Scn:  held,
	})
}

// ShotDraft is the payload of a shot or a miss.
func ShotDraft(kind, ctl string, cseq uint64) Draft {
	return mustDraft(kind, protocol.ShotData{Ctl: ctl, Cseq: cseq})
}

func mustDraft(kind string, data any) Draft {
	raw, err := protocol.EncodeData(data)
	if err != nil {
		panic(fmt.Sprintf("simtarget: encode %s: %v", kind, err))
	}
	return Draft{Kind: kind, Data: raw}
}

func round3(v float64) float64 {
	return float64(int(v*1000)) / 1000
}
