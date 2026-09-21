package simtarget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/cyb3rgun/theserver/pkg/journal"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// ErrUnauthorized is returned when the server refuses the token. A target
// does not retry then; the operator has to act.
var ErrUnauthorized = errors.New("server refused the device token")

// Options configure a simulated target.
type Options struct {
	Server      string // wss://host:port, with or without the link path
	DeviceID    string
	Token       string
	Class       string
	Firmware    string
	Insecure    bool          // skip TLS verification
	Rate        float64       // events per second
	Controllers int           // controller ids to rotate
	DropEvery   time.Duration // deliberate drop after this long on a connection; 0 never
	Duration    time.Duration // stop generating after this long; 0 runs until ctx ends
	Health      time.Duration // health report interval
	Reconnect   time.Duration // pause before reconnecting
	DrainWait   time.Duration // how long to wait for the last acks
	Seed        uint64
	Logger      *slog.Logger
	// Holdings are scenario versions the target reports as held besides
	// the packages in ContentDir.
	Holdings []protocol.Holding
	// ContentDir keeps the installed packages, <id>/<version>/package.zip.
	// Without it the target refuses content_available.
	ContentDir string
}

func (o *Options) defaults() {
	if o.Class == "" {
		o.Class = protocol.ClassESP
	}
	if o.Firmware == "" {
		o.Firmware = "simtarget"
	}
	if o.Rate <= 0 {
		o.Rate = 5
	}
	if o.Health <= 0 {
		o.Health = 30 * time.Second
	}
	if o.Reconnect <= 0 {
		o.Reconnect = 2 * time.Second
	}
	if o.DrainWait <= 0 {
		o.DrainWait = 10 * time.Second
	}
	if o.Seed == 0 {
		o.Seed = uint64(time.Now().UnixNano())
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Stats is what a run did.
type Stats struct {
	Generated   uint64 // events this run created
	FirstSeq    uint64 // first seq this run created, 0 if none
	LastSeq     uint64 // highest seq in the journal at the end
	FramesSent  int    // event frames written, resends included
	Replayed    int    // event frames sent in a replay after hello
	Replays     int    // connections that replayed at least one event
	Drops       int    // deliberate drops
	Connections int    // successful handshakes
	Epoch       uint64 // sequence epoch of the journal at the end
	EpochResets int    // times the server announced a new epoch
	Dropped     int    // unacknowledged events given up at those resets
	LastAck     uint64 // highest ack received
	Unacked     int    // events still pending at the end
	Commands    int    // commands answered
	Installs    int    // scenario versions installed after an announcement
	// InstallsFailed counts announcements whose package did not install.
	InstallsFailed int
	Held           []protocol.Holding // scenario versions held at the end
	// Session is the session the target plays and Playing the scenario
	// version it was told to play, both empty until a session_start and
	// cleared again by a session_stop (D-057).
	Session string
	Playing protocol.Holding
	// Refused counts session_start commands for a version the target does
	// not hold.
	Refused int
}

// Run drives the simulated target until the duration is over and every event
// is acknowledged, or until ctx ends. It returns ErrUnauthorized when the
// server refuses the token.
func Run(ctx context.Context, opts Options, j *journal.Journal) (Stats, error) {
	opts.defaults()
	base, err := httpBase(opts.Server)
	if err != nil {
		return Stats{}, err
	}
	d := &device{
		opts:       opts,
		journal:    j,
		gen:        NewGenerator(opts.Controllers, opts.Seed, time.Now()),
		log:        opts.Logger.With("device", opts.DeviceID),
		fresh:      make(chan struct{}, 1),
		stopped:    make(chan struct{}),
		baseURL:    base,
		client:     httpClient(opts.Insecure),
		held:       map[protocol.Holding]bool{},
		installing: map[protocol.Holding]bool{},
	}
	for _, h := range append(append([]protocol.Holding{}, opts.Holdings...), installed(opts.ContentDir)...) {
		d.held[h] = true
	}
	return d.run(ctx)
}

type device struct {
	opts    Options
	journal *journal.Journal
	log     *slog.Logger
	baseURL string // the API, https://host:port
	client  *http.Client

	genMu sync.Mutex // the generator serves the generator loop and the connections
	gen   *Generator

	fresh   chan struct{} // a new event is in the journal
	stopped chan struct{} // generation is over

	heldMu     sync.Mutex
	held       map[protocol.Holding]bool
	installing map[protocol.Holding]bool
	installs   sync.WaitGroup

	mu    sync.Mutex
	stats Stats
}

// sessionEnd says why a connection ended.
type sessionEnd int

const (
	endError sessionEnd = iota
	endDrop
	endReboot
	endDone
	endCancel
	endEpoch
)

func (d *device) run(ctx context.Context) (Stats, error) {
	genCtx, stopGen := context.WithCancel(ctx)
	defer stopGen()
	generating := make(chan error, 1)
	go func() { generating <- d.generate(genCtx) }()

	var runErr error
	first, immediately := true, false
loop:
	for {
		if !first && !immediately {
			select {
			case <-ctx.Done():
				break loop
			case <-time.After(d.opts.Reconnect):
			}
		}
		first, immediately = false, false

		end, err := d.session(ctx)
		switch {
		case errors.Is(err, ErrUnauthorized):
			runErr = err
			break loop
		case end == endDone, end == endCancel:
			break loop
		case end == endDrop:
			d.log.Info("deliberate drop, reconnecting", "after", d.opts.Reconnect)
		case end == endReboot:
			d.log.Info("reboot requested, reconnecting", "after", d.opts.Reconnect)
		case end == endEpoch:
			immediately = true
		default:
			d.log.Warn("connection ended, reconnecting", "error", err, "after", d.opts.Reconnect)
		}
	}

	stopGen()
	if err := <-generating; err != nil && runErr == nil {
		runErr = err
	}
	d.installs.Wait()

	held := d.holdings()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stats.LastSeq = d.journal.LastSeq()
	d.stats.Unacked = d.journal.Pending()
	d.stats.Epoch = d.journal.Epoch()
	d.stats.Held = held
	return d.stats, runErr
}

// generate journals events at the configured rate until the duration is over.
func (d *device) generate(ctx context.Context) error {
	defer close(d.stopped)
	interval := time.Duration(float64(time.Second) / d.opts.Rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	health := time.NewTicker(d.opts.Health)
	defer health.Stop()

	var deadline <-chan time.Time
	if d.opts.Duration > 0 {
		timer := time.NewTimer(d.opts.Duration)
		defer timer.Stop()
		deadline = timer.C
	}

	for {
		var draft journal.Draft
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			d.log.Info("duration reached, no more events")
			return nil
		case now := <-health.C:
			draft = d.healthDraft(now)
		case <-ticker.C:
			d.genMu.Lock()
			draft = d.gen.Next()
			d.genMu.Unlock()
		}
		if err := d.appendDraft(draft); err != nil {
			return err
		}
	}
}

// healthDraft is a health report with what the target holds.
func (d *device) healthDraft(now time.Time) journal.Draft {
	held := d.holdings()
	d.genMu.Lock()
	defer d.genMu.Unlock()
	return d.gen.Health(now, held)
}

// appendDraft journals an event and wakes the connection that sends it.
func (d *device) appendDraft(draft journal.Draft) error {
	event, err := d.journal.Append(draft, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.stats.Generated++
	if d.stats.FirstSeq == 0 {
		d.stats.FirstSeq = event.Seq
	}
	d.mu.Unlock()
	select {
	case d.fresh <- struct{}{}:
	default:
	}
	return nil
}

// session runs one connection.
func (d *device) session(ctx context.Context) (sessionEnd, error) {
	ws, err := d.dial(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return endCancel, nil
		}
		return endError, err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(protocol.MaxFrameSize)

	helloLast := d.journal.LastSeq()
	if err := write(ctx, ws, protocol.Hello{
		Dev: d.opts.DeviceID, FW: d.opts.Firmware, Cls: d.opts.Class,
		Last: helloLast, Proto: protocol.Version,
	}); err != nil {
		return endError, err
	}
	welcome, err := readWelcome(ctx, ws)
	if err != nil {
		return endError, err
	}
	if welcome.Ep != 0 && welcome.Ep != d.journal.Epoch() {
		// The device was reset. Start over at seq 1 in the new epoch and
		// connect again, so the next hello speaks for that epoch.
		old := d.journal.Epoch()
		dropped, err := d.journal.Reset(welcome.Ep)
		if err != nil {
			return endError, err
		}
		d.mu.Lock()
		d.stats.EpochResets++
		d.stats.Dropped += dropped
		d.stats.LastAck = 0
		d.mu.Unlock()
		d.log.Info("server announced a new epoch, starting over at seq 1",
			"old_epoch", old, "epoch", welcome.Ep, "dropped", dropped)
		ws.Close(websocket.StatusNormalClosure, "epoch changed")
		return endEpoch, nil
	}
	if err := d.journal.Acked(welcome.Ack); err != nil {
		return endError, err
	}
	d.mu.Lock()
	d.stats.Connections++
	d.stats.LastAck = max(d.stats.LastAck, welcome.Ack)
	d.mu.Unlock()
	session, scenario, version := "", "", uint64(0)
	if welcome.Ses != nil {
		session = *welcome.Ses
	}
	if welcome.Scn != nil {
		scenario, version = welcome.Scn.ID, welcome.Scn.Ver
	}
	d.log.Info("connected", "epoch", welcome.Ep, "journal_last", helloLast, "server_ack", welcome.Ack,
		"to_replay", len(d.journal.After(welcome.Ack)), "session", session,
		"scenario", scenario, "version", version)
	// A target reports its health, and with it what it holds, as soon as
	// it is connected.
	if err := d.appendDraft(d.healthDraft(time.Now())); err != nil {
		return endError, err
	}

	readerDone := make(chan error, 1)
	reboot := make(chan struct{}, 1)
	var sendMu sync.Mutex
	go func() { readerDone <- d.readLoop(ctx, ws, &sendMu, reboot) }()

	// cursor is the highest seq sent on this connection. Everything up to
	// helloLast that goes out is a replay.
	cursor := welcome.Ack
	var replayed atomic.Int64
	sendPending := func() error {
		for _, event := range d.journal.After(cursor) {
			sendMu.Lock()
			err := write(ctx, ws, event)
			sendMu.Unlock()
			if err != nil {
				return err
			}
			cursor = event.Seq
			d.mu.Lock()
			d.stats.FramesSent++
			if event.Seq <= helloLast {
				d.stats.Replayed++
				replayed.Add(1)
			}
			d.mu.Unlock()
		}
		return nil
	}
	if err := sendPending(); err != nil {
		return endError, err
	}
	if n := replayed.Load(); n > 0 {
		d.mu.Lock()
		d.stats.Replays++
		d.mu.Unlock()
		d.log.Info("replayed", "events", n)
	}

	var drop <-chan time.Time
	if d.opts.DropEvery > 0 {
		timer := time.NewTimer(d.opts.DropEvery)
		defer timer.Stop()
		drop = timer.C
	}
	stopped := d.stopped
	var drained <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			ws.Close(websocket.StatusGoingAway, "simtarget stopping")
			return endCancel, nil
		case <-d.fresh:
			if err := sendPending(); err != nil {
				return endError, err
			}
		case <-drop:
			d.mu.Lock()
			d.stats.Drops++
			d.mu.Unlock()
			ws.Close(websocket.StatusNormalClosure, "deliberate drop")
			return endDrop, nil
		case <-reboot:
			ws.Close(websocket.StatusNormalClosure, "reboot")
			return endReboot, nil
		case err := <-readerDone:
			return endError, err
		case <-stopped:
			// Generation is over: send what is left, then wait for the acks.
			stopped = nil
			drop = nil
			if err := sendPending(); err != nil {
				return endError, err
			}
			drained = time.After(d.opts.DrainWait)
		case <-drained:
			d.log.Warn("not every event was acknowledged in time", "unacked", d.journal.Pending())
			ws.Close(websocket.StatusNormalClosure, "done")
			return endDone, nil
		case <-time.After(50 * time.Millisecond):
			if stopped == nil && d.journal.Pending() == 0 {
				ws.Close(websocket.StatusNormalClosure, "done")
				return endDone, nil
			}
		}
	}
}

// readLoop takes acks and commands until the connection ends.
func (d *device) readLoop(ctx context.Context, ws *websocket.Conn, sendMu *sync.Mutex, reboot chan<- struct{}) error {
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageBinary {
			return errors.New("server sent a text frame")
		}
		msg, err := protocol.Decode(data)
		if err != nil {
			return err
		}
		switch m := msg.(type) {
		case protocol.Ack:
			if err := d.journal.Acked(m.Seq); err != nil {
				return err
			}
			d.mu.Lock()
			d.stats.LastAck = max(d.stats.LastAck, m.Seq)
			d.mu.Unlock()
		case protocol.Command:
			var result protocol.Result
			restart := false
			switch m.N {
			case protocol.CommandContentAvailable:
				result = d.announced(ctx, m)
			case protocol.CommandSessionStart:
				result = d.plays(m)
			case protocol.CommandSessionStop:
				result = d.stopsPlaying(m)
			default:
				result, restart = answer(m)
			}
			sendMu.Lock()
			err := write(ctx, ws, result)
			sendMu.Unlock()
			if err != nil {
				return err
			}
			d.mu.Lock()
			d.stats.Commands++
			d.mu.Unlock()
			d.log.Info("command", "name", m.N, "id", m.ID, "ok", result.OK)
			if restart {
				select {
				case reboot <- struct{}{}:
				default:
				}
			}
		case protocol.Error:
			d.log.Warn("server sent an error", "code", m.C, "message", m.M)
			if m.C == protocol.CodeUnauthorized {
				return fmt.Errorf("%w: %s", ErrUnauthorized, m.M)
			}
			return m
		default:
			d.log.Warn("unexpected message", "type", msg.Type())
		}
	}
}

// answer is how the simulated target responds to a command: a time mark is
// accepted, a reboot is accepted and followed by a reconnect, anything else
// is refused. The session commands have their own answers.
func answer(cmd protocol.Command) (protocol.Result, bool) {
	switch cmd.N {
	case protocol.CommandTimeMark:
		return protocol.Result{ID: cmd.ID, OK: true}, false
	case protocol.CommandReboot:
		return protocol.Result{ID: cmd.ID, OK: true}, true
	default:
		return protocol.Result{ID: cmd.ID, OK: false, E: "simtarget does not support " + cmd.N}, false
	}
}

// plays answers session_start: the target says in the log which scenario
// version it would play, and refuses a start for content it does not hold,
// which is what a real target does (D-057).
func (d *device) plays(cmd protocol.Command) protocol.Result {
	var start protocol.SessionStart
	if err := protocol.DecodeArgs(cmd.A, &start); err != nil {
		d.log.Warn("session_start without a scenario", "error", err)
		return protocol.Result{ID: cmd.ID, OK: false, E: "session_start: " + err.Error()}
	}
	d.heldMu.Lock()
	held := d.held[start.Scn]
	d.heldMu.Unlock()
	if !held {
		d.log.Warn("session_start for content the target does not hold",
			"session", start.Ses, "scenario", start.Scn.ID, "version", start.Scn.Ver)
		d.mu.Lock()
		d.stats.Refused++
		d.mu.Unlock()
		return protocol.Result{ID: cmd.ID, OK: false,
			E: fmt.Sprintf("the target does not hold %s version %d", start.Scn.ID, start.Scn.Ver)}
	}
	d.log.Info("playing", "session", start.Ses, "scenario", start.Scn.ID, "version", start.Scn.Ver)
	d.mu.Lock()
	d.stats.Session, d.stats.Playing = start.Ses, start.Scn
	d.mu.Unlock()
	return protocol.Result{ID: cmd.ID, OK: true, R: map[string]any{"playing": start.Scn.ID}}
}

// stopsPlaying answers session_stop: the target stops and says so.
func (d *device) stopsPlaying(cmd protocol.Command) protocol.Result {
	var stop protocol.SessionStop
	if err := protocol.DecodeArgs(cmd.A, &stop); err != nil {
		return protocol.Result{ID: cmd.ID, OK: false, E: "session_stop: " + err.Error()}
	}
	d.log.Info("stopped playing", "session", stop.Ses)
	d.mu.Lock()
	d.stats.Session, d.stats.Playing = "", protocol.Holding{}
	d.mu.Unlock()
	return protocol.Result{ID: cmd.ID, OK: true}
}

func (d *device) dial(ctx context.Context) (*websocket.Conn, error) {
	url := strings.TrimRight(d.opts.Server, "/")
	if !strings.HasSuffix(url, "/link/v1") {
		url += "/link/v1"
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+d.opts.Token)

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		HTTPClient: d.client,
		HTTPHeader: header,
	})
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", url, err)
	}
	return ws, nil
}

func write(ctx context.Context, ws *websocket.Conn, msg protocol.Message) error {
	frame, err := protocol.Encode(msg)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return ws.Write(writeCtx, websocket.MessageBinary, frame)
}

func readWelcome(ctx context.Context, ws *websocket.Conn) (protocol.Welcome, error) {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	typ, data, err := ws.Read(readCtx)
	if err != nil {
		return protocol.Welcome{}, err
	}
	if typ != websocket.MessageBinary {
		return protocol.Welcome{}, errors.New("server sent a text frame")
	}
	msg, err := protocol.Decode(data)
	if err != nil {
		return protocol.Welcome{}, err
	}
	switch m := msg.(type) {
	case protocol.Welcome:
		return m, nil
	case protocol.Error:
		if m.C == protocol.CodeUnauthorized {
			return protocol.Welcome{}, fmt.Errorf("%w: %s", ErrUnauthorized, m.M)
		}
		return protocol.Welcome{}, m
	default:
		return protocol.Welcome{}, fmt.Errorf("expected welcome, got %s", msg.Type())
	}
}
