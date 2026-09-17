// Package link is the server side of the device link (docs/protocol.md).
//
// Every device holds one WebSocket connection. The server authenticates the
// upgrade with the bearer token, reads the hello, answers with welcome, takes
// the replay and the live events into the journal in batches, acknowledges
// the contiguous sequence number after every batch, sends commands and
// matches their results, and keeps the connection alive with pings. Each
// connection runs in its own goroutines; a failing device never takes the
// server down.
package link

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/cyb3rgun/theserver/internal/protocol"
	"github.com/cyb3rgun/theserver/internal/store"
)

// Path is where the device link is mounted.
const Path = "/link/v1"

// Config holds the timing of the link. DefaultConfig gives the values of the
// protocol.
type Config struct {
	// AckInterval and AckBatch bound a batch: it is flushed to the journal and
	// acknowledged when it is AckInterval old or holds AckBatch events.
	AckInterval time.Duration
	AckBatch    int
	// PingInterval is how often the server pings; a pong has PongTimeout to
	// arrive before the connection is dropped.
	PingInterval time.Duration
	PongTimeout  time.Duration
	// HelloTimeout is how long a new connection may take to send hello.
	HelloTimeout time.Duration
	// CommandTimeout is how long a device has to answer a command.
	CommandTimeout time.Duration
	// StatusCheck is how often the server looks for devices that lost their
	// approval, so that a revoked device is dropped within that time.
	StatusCheck time.Duration
}

// DefaultConfig returns the numbers of docs/protocol.md.
func DefaultConfig() Config {
	return Config{
		AckInterval:    100 * time.Millisecond,
		AckBatch:       32,
		PingInterval:   15 * time.Second,
		PongTimeout:    10 * time.Second,
		HelloTimeout:   5 * time.Second,
		CommandTimeout: 5 * time.Second,
		StatusCheck:    500 * time.Millisecond,
	}
}

var (
	// ErrDeviceOffline is returned for a command to a device without a
	// connection, or one whose connection ended before it answered.
	ErrDeviceOffline = errors.New("device is not connected")
	// ErrCommandTimeout is returned when a device does not answer a command in
	// time.
	ErrCommandTimeout = errors.New("device did not answer the command in time")
	// errShutdown refuses connections while the server stops.
	errShutdown = errors.New("server is shutting down")
)

// writeTimeout bounds a single frame write; a peer that does not take a frame
// in that time is dropped.
const writeTimeout = 5 * time.Second

// DeviceStatus describes one connected device.
type DeviceStatus struct {
	DeviceID    string
	RemoteAddr  string
	ConnectedAt time.Time
	LastEventAt time.Time
	LastAck     uint64
}

// Server accepts device connections and keeps the registry of the ones that
// are online. The zero value is not usable; call New.
type Server struct {
	store *store.Store
	cfg   Config
	log   *slog.Logger
	now   func() time.Time

	mu     sync.Mutex
	conns  map[string]*deviceConn
	closed bool
	active sync.WaitGroup
}

// New returns a link server that journals into st.
func New(st *store.Store, cfg Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store: st,
		cfg:   cfg,
		log:   logger.With("component", "link"),
		now:   time.Now,
		conns: map[string]*deviceConn{},
	}
}

// ServeHTTP authenticates the request and runs the connection until it ends.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	remote := r.RemoteAddr
	if s.isClosed() {
		http.Error(w, errShutdown.Error(), http.StatusServiceUnavailable)
		return
	}

	device, err := s.authenticate(r)
	if err != nil {
		var refusal *refusal
		if errors.As(err, &refusal) {
			s.log.Warn("link refused", "remote", remote, "reason", refusal.reason)
			writeUnauthorized(w, refusal.reason)
			return
		}
		s.log.Error("link could not check the token", "remote", remote, "error", err)
		http.Error(w, "device registry unavailable", http.StatusServiceUnavailable)
		return
	}

	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("link upgrade failed", "remote", remote, "device", device.ID, "error", err)
		return
	}
	ws.SetReadLimit(protocol.MaxFrameSize)

	s.active.Add(1)
	defer s.active.Done()

	c := newDeviceConn(s, ws, device, remote)
	c.log.Info("device connected")
	c.serve()
}

type refusal struct {
	reason string
}

func (r *refusal) Error() string {
	return r.reason
}

// authenticate finds the device of the bearer token (D-019) and requires it to
// be approved.
func (s *Server) authenticate(r *http.Request) (store.Device, error) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return store.Device{}, &refusal{"no bearer token"}
	}
	device, err := s.store.DeviceByToken(r.Context(), strings.TrimSpace(token))
	if errors.Is(err, store.ErrDeviceNotFound) {
		return store.Device{}, &refusal{"unknown token"}
	}
	if err != nil {
		return store.Device{}, err
	}
	if device.Status != store.StatusApproved {
		return store.Device{}, &refusal{fmt.Sprintf("device %s is %s", device.ID, device.Status)}
	}
	return device, nil
}

// writeUnauthorized answers 401 before the upgrade, with the err message of
// the protocol as a CBOR body.
func writeUnauthorized(w http.ResponseWriter, reason string) {
	body, err := protocol.Encode(protocol.Error{C: protocol.CodeUnauthorized, M: reason})
	w.Header().Set("WWW-Authenticate", `Bearer realm="theserver device link"`)
	if err != nil {
		http.Error(w, reason, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/cbor")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write(body)
}

// Online lists the connected devices, ordered by device id.
func (s *Server) Online() []DeviceStatus {
	s.mu.Lock()
	conns := make([]*deviceConn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	statuses := make([]DeviceStatus, 0, len(conns))
	for _, c := range conns {
		statuses = append(statuses, c.status())
	}
	slices.SortFunc(statuses, func(a, b DeviceStatus) int { return strings.Compare(a.DeviceID, b.DeviceID) })
	return statuses
}

// SendCommand sends a command to a connected device and waits for its result.
// The command id is counted per connection from 1. A device that does not
// answer within the command timeout gives ErrCommandTimeout, which is logged.
func (s *Server) SendCommand(ctx context.Context, deviceID, name string, args map[string]any) (protocol.Result, error) {
	c := s.lookup(deviceID)
	if c == nil {
		return protocol.Result{}, fmt.Errorf("%s: %w", deviceID, ErrDeviceOffline)
	}
	return c.command(ctx, name, args)
}

// A DisconnectReason says why the server ends a live connection on behalf of
// an operator.
type DisconnectReason int

const (
	// Revoked: the device is no longer approved. It gets err unauthorized.
	Revoked DisconnectReason = iota
	// TokenReplaced: the device has a new token. It gets err unauthorized,
	// and its old token no longer connects.
	TokenReplaced
	// Reset: the device has a new sequence epoch. It is closed with status
	// 1012 and no err, reconnects, and learns the epoch from welcome.
	Reset
)

func (r DisconnectReason) closing() closing {
	switch r {
	case TokenReplaced:
		return closing{
			code:    websocket.StatusPolicyViolation,
			reason:  "device token replaced",
			err:     &protocol.Error{C: protocol.CodeUnauthorized, M: "the device token was replaced"},
			discard: true,
		}
	case Reset:
		return closing{code: websocket.StatusServiceRestart, reason: "device reset", discard: true}
	default:
		return closing{
			code:   websocket.StatusPolicyViolation,
			reason: "device revoked",
			err:    &protocol.Error{C: protocol.CodeUnauthorized, M: "device is no longer approved"},
		}
	}
}

// Disconnect ends the live connection of a device, if it has one, and reports
// whether it had. The connection is closed in the background.
func (s *Server) Disconnect(deviceID string, why DisconnectReason) bool {
	c := s.lookup(deviceID)
	if c == nil {
		return false
	}
	go c.close(why.closing())
	return true
}

// Watch compares every live connection with its device every StatusCheck
// until ctx ends, and drops connections of devices that are no longer
// approved, got a new token, or were reset. That covers changes made by
// another process, such as a device command beside the running server.
func (s *Server) Watch(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.StatusCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if len(s.Online()) == 0 {
			continue
		}
		states, err := s.store.DeviceStates(ctx)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Error("link could not read device states", "error", err)
			}
			continue
		}
		for _, status := range s.Online() {
			c := s.lookup(status.DeviceID)
			if c == nil {
				continue
			}
			state, known := states[status.DeviceID]
			switch {
			case !known || state.Status != store.StatusApproved:
				go c.close(Revoked.closing())
			case !bytes.Equal(state.TokenHash, c.device.TokenHash):
				go c.close(TokenReplaced.closing())
			case state.SeqEpoch != c.epoch:
				go c.close(Reset.closing())
			}
		}
	}
}

// Close ends every connection, lets each flush what it received, and waits
// for them, at most until ctx ends. New connections are refused from now on.
func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	conns := make([]*deviceConn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	for _, c := range conns {
		go c.close(closing{code: websocket.StatusGoingAway, reason: "server shutting down"})
	}

	done := make(chan struct{})
	go func() {
		s.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("device connections still open: %w", ctx.Err())
	}
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) lookup(deviceID string) *deviceConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[deviceID]
}

// register makes c the connection of its device and returns the connection it
// replaces, if any. The newest connection wins.
func (s *Server) register(c *deviceConn) (*deviceConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errShutdown
	}
	old := s.conns[c.deviceID]
	s.conns[c.deviceID] = c
	return old, nil
}

func (s *Server) unregister(c *deviceConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns[c.deviceID] == c {
		delete(s.conns, c.deviceID)
	}
}

// deviceConn is one device connection. serve runs the handshake and the read
// loop; a writer, a batcher and a pinger run beside it.
type deviceConn struct {
	srv      *Server
	ws       *websocket.Conn
	deviceID string
	device   store.Device
	epoch    uint64 // the sequence epoch this connection journals into
	remote   string
	log      *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	out        chan outFrame
	events     chan store.Event
	stopWriter chan struct{}
	writerDone chan struct{}
	batchDone  chan struct{}
	closing    chan struct{}
	done       chan struct{}

	closeOnce  sync.Once
	stopOnce   sync.Once
	discarding atomic.Bool

	nextCommand atomic.Uint64
	pendingMu   sync.Mutex
	pending     map[uint64]chan protocol.Result

	statMu      sync.Mutex
	reason      string
	connectedAt time.Time
	lastEventAt time.Time
	lastAck     uint64
	replayUpTo  uint64
	replayDone  bool
	replayed    int
	received    int
	stored      int
	duplicates  int
}

type outFrame struct {
	data    []byte
	last    bool
	written chan struct{}
}

// closing says how a connection ends.
type closing struct {
	code   websocket.StatusCode
	reason string
	// err is sent as the last frame before the close.
	err *protocol.Error
	// abrupt skips the close handshake, for a peer that no longer answers.
	abrupt bool
	// discard drops events that are not flushed yet, after the journal
	// refused a batch; the device replays them.
	discard bool
}

func newDeviceConn(s *Server, ws *websocket.Conn, device store.Device, remote string) *deviceConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &deviceConn{
		srv:         s,
		ws:          ws,
		deviceID:    device.ID,
		device:      device,
		remote:      remote,
		log:         s.log.With("device", device.ID, "remote", remote),
		ctx:         ctx,
		cancel:      cancel,
		out:         make(chan outFrame, 64),
		events:      make(chan store.Event, 2*max(s.cfg.AckBatch, 1)),
		stopWriter:  make(chan struct{}),
		writerDone:  make(chan struct{}),
		batchDone:   make(chan struct{}),
		closing:     make(chan struct{}),
		done:        make(chan struct{}),
		pending:     map[uint64]chan protocol.Result{},
		connectedAt: s.now(),
	}
}

// serve runs the whole life of the connection.
func (c *deviceConn) serve() {
	go c.writeLoop()
	go c.batchLoop()
	defer c.finish()

	if !c.handshake() {
		return
	}
	go c.pingLoop()
	c.readLoop()
}

// handshake reads the hello and answers it. It reports whether the connection
// goes on.
func (c *deviceConn) handshake() bool {
	helloCtx, cancel := context.WithTimeout(c.ctx, c.srv.cfg.HelloTimeout)
	typ, data, err := c.ws.Read(helloCtx)
	cancel()
	if err != nil {
		if helloCtx.Err() != nil && c.ctx.Err() == nil {
			c.close(closing{code: websocket.StatusPolicyViolation, reason: "no hello in time", abrupt: true})
		} else {
			c.setReason("closed before hello: " + describeReadError(err))
		}
		return false
	}
	c.touch()

	msg, ok := c.decode(typ, data)
	if !ok {
		return false
	}
	hello, isHello := msg.(protocol.Hello)
	if !isHello {
		c.fail(protocol.CodeBadMessage, fmt.Sprintf("expected hello, got %s", msg.Type()))
		return false
	}
	switch {
	case hello.Proto != protocol.Version:
		c.fail(protocol.CodeProtoUnsupported, fmt.Sprintf("protocol %d is not supported, this server speaks %d", hello.Proto, protocol.Version))
		return false
	case hello.Dev != c.deviceID:
		c.fail(protocol.CodeUnauthorized, fmt.Sprintf("the token belongs to %s, not to %s", c.deviceID, hello.Dev))
		return false
	case hello.Cls != protocol.ClassESP && hello.Cls != protocol.ClassPi && hello.Cls != protocol.ClassPC:
		c.fail(protocol.CodeBadMessage, fmt.Sprintf("unknown class %q", hello.Cls))
		return false
	}
	if hello.Cls != c.device.Class {
		c.log.Warn("device reports another class than registered", "hello_class", hello.Cls, "registered_class", c.device.Class)
	}

	// The newest connection wins. Wait for the one it replaces, so that its
	// last batch is in the journal before the ack of this one is read.
	old, err := c.srv.register(c)
	if err != nil {
		c.close(closing{code: websocket.StatusGoingAway, reason: "server shutting down"})
		return false
	}
	if old != nil {
		c.log.Info("replacing an older connection of the device", "old_remote", old.remote)
		// The device is here again, so the old peer is gone or stuck; a close
		// handshake with it would only hold up this connection.
		old.close(closing{code: websocket.StatusPolicyViolation, reason: "replaced by a newer connection", abrupt: true})
		select {
		case <-old.done:
		case <-time.After(2 * writeTimeout):
			c.log.Warn("the replaced connection did not finish in time")
		}
	}

	ctx := c.ctx
	epoch, serverAck, err := c.srv.store.SeqState(ctx, c.deviceID)
	if err != nil {
		c.storeFailure("read sequence state", err)
		return false
	}
	c.epoch = epoch
	if hello.Last < serverAck {
		c.fail(protocol.CodeSeqRegression,
			fmt.Sprintf("device journal ends at %d, the server has %d; reset the device in the admin UI", hello.Last, serverAck))
		return false
	}
	if hello.FW != "" && hello.FW != c.device.FirmwareVersion {
		if err := c.srv.store.SetFirmwareVersion(ctx, c.deviceID, hello.FW); err != nil {
			c.log.Warn("could not record the firmware version", "error", err)
		}
	}
	session, inSession, err := c.srv.store.RunningSessionFor(ctx, c.deviceID)
	if err != nil {
		c.storeFailure("read running session", err)
		return false
	}

	welcome := protocol.Welcome{Ack: serverAck, Now: c.srv.now().UnixMilli(), Ep: epoch}
	if inSession {
		welcome.Ses = &session
	}

	c.statMu.Lock()
	c.lastAck = serverAck
	c.replayUpTo = hello.Last
	c.replayDone = hello.Last <= serverAck
	c.statMu.Unlock()

	c.log.Info("device hello",
		"fw", hello.FW, "class", hello.Cls, "device_last", hello.Last,
		"epoch", epoch, "server_ack", serverAck, "replay_expected", hello.Last-serverAck, "session", session)

	if err := c.send(welcome); err != nil {
		return false
	}
	if hello.Last <= serverAck {
		// Nothing to replay, so the replay is complete right away.
		if err := c.send(protocol.Ack{Seq: serverAck}); err != nil {
			return false
		}
	}
	return true
}

// readLoop takes frames until the connection ends.
func (c *deviceConn) readLoop() {
	for {
		typ, data, err := c.ws.Read(c.ctx)
		if err != nil {
			c.setReason(describeReadError(err))
			return
		}
		now := c.touch()

		msg, ok := c.decode(typ, data)
		if !ok {
			return
		}
		switch m := msg.(type) {
		case protocol.Event:
			event := store.Event{
				ID:       store.EventID(m.ID),
				Seq:      m.Seq,
				Kind:     m.K,
				TsDevice: m.Ts,
				Payload:  []byte(m.D),
			}
			if ctl, ok := protocol.ControllerID(m.D); ok {
				event.ControllerID = ctl
			}
			c.statMu.Lock()
			c.lastEventAt = now
			c.received++
			c.statMu.Unlock()
			select {
			case c.events <- event:
			case <-c.closing:
				return
			}
		case protocol.Result:
			c.resolve(m)
		case protocol.Error:
			c.log.Warn("device reported a protocol error", "code", m.C, "message", m.M)
			c.close(closing{code: websocket.StatusNormalClosure, reason: "device sent err " + m.C})
			return
		default:
			c.fail(protocol.CodeBadMessage, fmt.Sprintf("a device does not send %s", msg.Type()))
			return
		}
	}
}

// decode turns a frame into a message, or ends the connection with the
// matching error code.
func (c *deviceConn) decode(typ websocket.MessageType, data []byte) (protocol.Message, bool) {
	if typ != websocket.MessageBinary {
		c.fail(protocol.CodeBadMessage, "frames must be binary")
		return nil, false
	}
	msg, err := protocol.Decode(data)
	switch {
	case err == nil:
		return msg, true
	case errors.Is(err, protocol.ErrUnknownType):
		c.fail(protocol.CodeUnknownType, err.Error())
	default:
		c.fail(protocol.CodeBadMessage, err.Error())
	}
	return nil, false
}

// touch records that the device sent a frame.
func (c *deviceConn) touch() time.Time {
	now := c.srv.now()
	if err := c.srv.store.TouchLastSeen(c.ctx, c.deviceID, now); err != nil && c.ctx.Err() == nil {
		c.log.Warn("could not record last seen", "error", err)
	}
	return now
}

// batchLoop collects events and flushes them to the journal every AckInterval
// or AckBatch events, whichever comes first, and acknowledges after every
// flush.
func (c *deviceConn) batchLoop() {
	defer close(c.batchDone)

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	var batch []store.Event

	flush := func() bool {
		timer.Stop()
		if len(batch) == 0 {
			return true
		}
		ok := c.flush(batch)
		batch = batch[:0]
		return ok
	}

	for {
		select {
		case event, open := <-c.events:
			if !open {
				if !c.discarding.Load() {
					flush()
				}
				return
			}
			if c.discarding.Load() {
				continue
			}
			if len(batch) == 0 {
				timer.Reset(c.srv.cfg.AckInterval)
			}
			batch = append(batch, event)
			if len(batch) >= c.srv.cfg.AckBatch {
				flush()
			}
		case <-timer.C:
			flush()
		}
	}
}

// flush writes one batch and sends the ack. It reports whether the batch was
// stored.
func (c *deviceConn) flush(batch []store.Event) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := c.srv.store.AppendEventsInEpoch(ctx, c.deviceID, c.epoch, batch)
	var conflict *store.ConflictError
	switch {
	case errors.Is(err, store.ErrEpochChanged):
		// The device was reset while these events were on their way. They
		// belong to the old epoch and are dropped with the connection.
		c.log.Info("device reset while events were pending", "epoch", c.epoch, "dropped", len(batch))
		c.close(Reset.closing())
		return false
	case errors.As(err, &conflict):
		c.log.Warn("device journal conflicts with the server", "seq", conflict.Seq, "error", err)
		c.close(closing{
			code:    websocket.StatusPolicyViolation,
			reason:  "journal conflict",
			err:     &protocol.Error{C: protocol.CodeBadMessage, M: fmt.Sprintf("seq %d: %v: %s", conflict.Seq, conflict.Err, conflict.Detail)},
			discard: true,
		})
		return false
	case err != nil:
		c.storeFailure("append events", err)
		return false
	}

	var replayed int
	c.statMu.Lock()
	for _, event := range batch {
		if event.Seq <= c.replayUpTo {
			replayed++
		}
	}
	c.replayed += replayed
	c.stored += result.Stored
	c.duplicates += result.Duplicates
	c.lastAck = result.AckSeq
	completed := !c.replayDone && result.AckSeq >= c.replayUpTo
	if completed {
		c.replayDone = true
	}
	total, duplicates := c.replayed, c.duplicates
	c.statMu.Unlock()

	if completed {
		c.log.Info("replay complete", "replayed", total, "duplicates", duplicates, "ack", result.AckSeq)
	}
	c.send(protocol.Ack{Seq: result.AckSeq})
	return true
}

// writeLoop sends the queued frames in order.
func (c *deviceConn) writeLoop() {
	defer close(c.writerDone)
	for {
		select {
		case <-c.stopWriter:
			return
		case frame := <-c.out:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := c.ws.Write(ctx, websocket.MessageBinary, frame.data)
			cancel()
			if frame.written != nil {
				close(frame.written)
			}
			if err != nil {
				go c.close(closing{code: websocket.StatusInternalError, reason: "write failed: " + err.Error(), abrupt: true})
				return
			}
			if frame.last {
				return
			}
		}
	}
}

// send queues a message. It fails once the connection is closing.
func (c *deviceConn) send(msg protocol.Message) error {
	data, err := protocol.Encode(msg)
	if err != nil {
		c.log.Error("could not encode a message", "type", msg.Type(), "error", err)
		return err
	}
	select {
	case <-c.closing:
		return fmt.Errorf("%s: %w", c.deviceID, ErrDeviceOffline)
	default:
	}
	select {
	case c.out <- outFrame{data: data}:
		return nil
	case <-c.closing:
		return fmt.Errorf("%s: %w", c.deviceID, ErrDeviceOffline)
	case <-c.writerDone:
		return fmt.Errorf("%s: %w", c.deviceID, ErrDeviceOffline)
	}
}

// pingLoop pings the device and drops it when a pong does not come back.
func (c *deviceConn) pingLoop() {
	ticker := time.NewTicker(c.srv.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.closing:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(c.ctx, c.srv.cfg.PongTimeout)
		err := c.ws.Ping(ctx)
		cancel()
		if err != nil {
			if c.ctx.Err() == nil {
				c.close(closing{code: websocket.StatusPolicyViolation, reason: "ping timeout", abrupt: true})
			}
			return
		}
	}
}

// fail ends the connection with a protocol error.
func (c *deviceConn) fail(code, message string) {
	c.log.Warn("protocol error", "code", code, "message", message)
	c.close(closing{
		code:   websocket.StatusPolicyViolation,
		reason: code,
		err:    &protocol.Error{C: code, M: message},
	})
}

// storeFailure ends the connection after the journal failed. The device keeps
// what was not acknowledged and replays it.
func (c *deviceConn) storeFailure(what string, err error) {
	c.log.Error("link store failure", "operation", what, "error", err)
	c.close(closing{code: websocket.StatusInternalError, reason: "store failure", discard: true})
}

// close ends the connection once: it stops further frames, sends the err
// message if there is one as the last frame, and closes the WebSocket.
func (c *deviceConn) close(how closing) {
	c.closeOnce.Do(func() {
		c.setReason(how.reason)
		if how.discard {
			c.discarding.Store(true)
		}

		if how.err != nil {
			if data, err := protocol.Encode(*how.err); err == nil {
				written := make(chan struct{})
				select {
				case c.out <- outFrame{data: data, last: true, written: written}:
					select {
					case <-written:
					case <-time.After(time.Second):
					}
				case <-c.writerDone:
				case <-time.After(time.Second):
				}
			}
		}
		close(c.closing)

		if how.abrupt {
			c.ws.CloseNow()
		} else {
			c.ws.Close(how.code, truncate(how.reason, 120))
		}
	})
}

// finish runs after the read loop: it flushes what arrived, stops the helper
// goroutines, leaves the registry and logs the end.
func (c *deviceConn) finish() {
	close(c.events)
	<-c.batchDone
	c.stopOnce.Do(func() { close(c.stopWriter) })
	<-c.writerDone
	c.close(closing{code: websocket.StatusNormalClosure, reason: "connection ended"})
	c.cancel()
	c.failPending()
	c.srv.unregister(c)

	st := c.status()
	c.statMu.Lock()
	reason, received, stored, duplicates, replayed := c.reason, c.received, c.stored, c.duplicates, c.replayed
	c.statMu.Unlock()
	c.log.Info("device disconnected",
		"reason", reason,
		"connected_for", c.srv.now().Sub(st.ConnectedAt).Round(time.Millisecond),
		"received", received, "stored", stored, "duplicates", duplicates,
		"replayed", replayed, "last_ack", st.LastAck)
	close(c.done)
}

func (c *deviceConn) setReason(reason string) {
	c.statMu.Lock()
	defer c.statMu.Unlock()
	if c.reason == "" {
		c.reason = reason
	}
}

func (c *deviceConn) status() DeviceStatus {
	c.statMu.Lock()
	defer c.statMu.Unlock()
	return DeviceStatus{
		DeviceID:    c.deviceID,
		RemoteAddr:  c.remote,
		ConnectedAt: c.connectedAt,
		LastEventAt: c.lastEventAt,
		LastAck:     c.lastAck,
	}
}

// command sends one command and waits for its result.
func (c *deviceConn) command(ctx context.Context, name string, args map[string]any) (protocol.Result, error) {
	id := c.nextCommand.Add(1)
	answer := make(chan protocol.Result, 1)

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pendingMu.Unlock()
		return protocol.Result{}, fmt.Errorf("%s: %w", c.deviceID, ErrDeviceOffline)
	}
	c.pending[id] = answer
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		if c.pending != nil {
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()
	}()

	if err := c.send(protocol.Command{ID: id, N: name, A: args}); err != nil {
		return protocol.Result{}, err
	}

	timer := time.NewTimer(c.srv.cfg.CommandTimeout)
	defer timer.Stop()
	select {
	case result, ok := <-answer:
		if !ok {
			return protocol.Result{}, fmt.Errorf("%s: %w", c.deviceID, ErrDeviceOffline)
		}
		return result, nil
	case <-timer.C:
		c.log.Warn("command timed out", "command", name, "id", id, "timeout", c.srv.cfg.CommandTimeout)
		return protocol.Result{}, fmt.Errorf("%s %s: %w", c.deviceID, name, ErrCommandTimeout)
	case <-ctx.Done():
		return protocol.Result{}, ctx.Err()
	}
}

func (c *deviceConn) resolve(result protocol.Result) {
	c.pendingMu.Lock()
	answer, ok := c.pending[result.ID]
	if ok {
		delete(c.pending, result.ID)
	}
	c.pendingMu.Unlock()
	if !ok {
		c.log.Warn("result for an unknown command", "id", result.ID)
		return
	}
	answer <- result
}

// failPending wakes every command still waiting; they report the device as
// offline.
func (c *deviceConn) failPending() {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, answer := range c.pending {
		close(answer)
		delete(c.pending, id)
	}
	c.pending = nil
}

func describeReadError(err error) string {
	switch {
	case errors.Is(err, websocket.ErrMessageTooBig):
		return fmt.Sprintf("frame larger than %d bytes", protocol.MaxFrameSize)
	case websocket.CloseStatus(err) != -1:
		return fmt.Sprintf("closed by device with status %d", websocket.CloseStatus(err))
	default:
		return "read failed: " + err.Error()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
