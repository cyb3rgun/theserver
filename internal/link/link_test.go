package link

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/cyb3rgun/theserver/internal/protocol"
	"github.com/cyb3rgun/theserver/internal/store"
)

// testConfig keeps the link fast enough for tests.
func testConfig() Config {
	cfg := DefaultConfig()
	cfg.AckInterval = 20 * time.Millisecond
	cfg.HelloTimeout = 2 * time.Second
	cfg.CommandTimeout = 2 * time.Second
	cfg.StatusCheck = 50 * time.Millisecond
	return cfg
}

// syncBuffer collects the server log; it is printed when a test fails.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	t     *testing.T
	store *store.Store
	link  *Server
	srv   *httptest.Server
	url   string
	logs  *syncBuffer
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	link := New(st, cfg, logger)

	watchCtx, stopWatch := context.WithCancel(context.Background())
	watching := make(chan struct{})
	go func() {
		link.Watch(watchCtx)
		close(watching)
	}()

	mux := http.NewServeMux()
	mux.Handle(Path, link)
	srv := httptest.NewTLSServer(mux)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := link.Close(ctx); err != nil {
			t.Errorf("link.Close: %v", err)
		}
		stopWatch()
		<-watching
		srv.Close()
		st.Close()
		if t.Failed() {
			t.Log("server log:\n" + logs.String())
		}
	})

	return &harness{
		t:     t,
		store: st,
		link:  link,
		srv:   srv,
		url:   "wss" + strings.TrimPrefix(srv.URL, "https") + Path,
		logs:  logs,
	}
}

// addDevice registers an approved device and returns its token.
func (h *harness) addDevice(id string) string {
	h.t.Helper()
	token, err := store.NewDeviceToken()
	if err != nil {
		h.t.Fatal(err)
	}
	err = h.store.UpsertDevice(context.Background(), store.Device{
		ID: id, Kind: store.KindTarget, Class: store.ClassESP,
		Status: store.StatusApproved, TokenHash: store.HashToken(token),
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return token
}

func (h *harness) countEvents(deviceID string) int {
	h.t.Helper()
	events, err := h.store.ListEvents(context.Background(), store.Filter{DeviceID: deviceID, Limit: 10_000})
	if err != nil {
		h.t.Fatal(err)
	}
	return len(events)
}

func (h *harness) online(deviceID string) bool {
	for _, status := range h.link.Online() {
		if status.DeviceID == deviceID {
			return true
		}
	}
	return false
}

// eventually polls cond for up to wait.
func eventually(t *testing.T, wait time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not happen within %s", what, wait)
}

// client is a minimal device written against internal/protocol only.
type client struct {
	t  *testing.T
	ws *websocket.Conn
}

func (h *harness) dialOptions(token string) *websocket.DialOptions {
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	return &websocket.DialOptions{HTTPClient: h.srv.Client(), HTTPHeader: header}
}

func (h *harness) dial(token string) *client {
	h.t.Helper()
	return h.dialWith(h.dialOptions(token))
}

func (h *harness) dialWith(opts *websocket.DialOptions) *client {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, h.url, opts)
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	ws.SetReadLimit(protocol.MaxFrameSize)
	c := &client{t: h.t, ws: ws}
	h.t.Cleanup(func() { ws.CloseNow() })
	return c
}

func (c *client) send(msg protocol.Message) {
	c.t.Helper()
	frame, err := protocol.Encode(msg)
	if err != nil {
		c.t.Fatal(err)
	}
	c.sendRaw(websocket.MessageBinary, frame)
}

func (c *client) sendRaw(typ websocket.MessageType, data []byte) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, typ, data); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// read returns the next message or the error that ended the connection.
func (c *client) read(wait time.Duration) (protocol.Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	typ, data, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageBinary {
		return nil, errors.New("server sent a text frame")
	}
	return protocol.Decode(data)
}

func (c *client) recv() protocol.Message {
	c.t.Helper()
	msg, err := c.read(5 * time.Second)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return msg
}

// hello sends a hello and returns the welcome.
func (c *client) hello(dev string, last uint64) protocol.Welcome {
	c.t.Helper()
	c.send(protocol.Hello{Dev: dev, FW: "sim-1", Cls: protocol.ClassESP, Last: last, Proto: protocol.Version})
	msg := c.recv()
	welcome, ok := msg.(protocol.Welcome)
	if !ok {
		c.t.Fatalf("expected welcome, got %#v", msg)
	}
	return welcome
}

// waitAck reads until an ack of exactly seq arrives. Lower acks are skipped;
// anything else fails the test.
func (c *client) waitAck(seq uint64) {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg := c.recv()
		ack, ok := msg.(protocol.Ack)
		if !ok {
			c.t.Fatalf("waiting for ack %d, got %#v", seq, msg)
		}
		switch {
		case ack.Seq == seq:
			return
		case ack.Seq > seq:
			c.t.Fatalf("got ack %d, want %d", ack.Seq, seq)
		}
	}
	c.t.Fatalf("no ack %d in time", seq)
}

// expectError reads the err message the server sends before it closes, and
// the close itself.
func (c *client) expectError(code string) protocol.Error {
	c.t.Helper()
	msg, err := c.read(5 * time.Second)
	for err == nil {
		if e, ok := msg.(protocol.Error); ok {
			if e.C != code {
				c.t.Fatalf("got err %s (%s), want %s", e.C, e.M, code)
			}
			if _, err := c.read(5 * time.Second); err == nil {
				c.t.Fatal("the server sent another frame after err")
			}
			return e
		}
		if _, isAck := msg.(protocol.Ack); !isAck {
			c.t.Fatalf("waiting for err %s, got %#v", code, msg)
		}
		msg, err = c.read(5 * time.Second)
	}
	c.t.Fatalf("connection ended without err %s: %v", code, err)
	return protocol.Error{}
}

// expectClosed reads until the connection ends and returns its close status.
func (c *client) expectClosed(wait time.Duration) websocket.StatusCode {
	c.t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		_, err := c.read(time.Until(deadline))
		if err != nil {
			return websocket.CloseStatus(err)
		}
	}
	c.t.Fatalf("the connection was still open after %s", wait)
	return 0
}

func shotEvent(t *testing.T, seq uint64, n byte) protocol.Event {
	t.Helper()
	data, err := protocol.EncodeData(protocol.ShotData{Ctl: "c-1", Cseq: seq})
	if err != nil {
		t.Fatal(err)
	}
	var id protocol.EventID
	id[6], id[8], id[15] = 0x40, 0x80, n
	return protocol.Event{ID: id, Seq: seq, K: protocol.KindShot, Ts: time.Now().UnixMilli(), D: data}
}

// connectAndStore runs a first connection that stores events 1..n and closes.
func (h *harness) connectAndStore(token, dev string, n uint64) {
	h.t.Helper()
	c := h.dial(token)
	c.hello(dev, 0)
	c.waitAck(0)
	for seq := uint64(1); seq <= n; seq++ {
		c.send(shotEvent(h.t, seq, byte(seq)))
	}
	c.waitAck(n)
	c.ws.Close(websocket.StatusNormalClosure, "done")
	eventually(h.t, 2*time.Second, "the first connection to end", func() bool { return !h.online(dev) })
}

func TestUnauthorizedGets401(t *testing.T) {
	h := newHarness(t, testConfig())
	h.addDevice("tgt-01")
	blockedToken := h.addDevice("tgt-02")
	if err := h.store.SetStatus(context.Background(), "tgt-02", store.StatusBlocked); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		header string
	}{
		{name: "wrong token", header: "Bearer not-a-token"},
		{name: "no header", header: ""},
		{name: "wrong scheme", header: "Basic " + blockedToken},
		{name: "blocked device", header: "Bearer " + blockedToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := h.dialOptions("")
			if tt.header != "" {
				opts.HTTPHeader.Set("Authorization", tt.header)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ws, resp, err := websocket.Dial(ctx, h.url, opts)
			if err == nil {
				ws.CloseNow()
				t.Fatal("the upgrade was accepted")
			}
			if resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("response %v, want 401", resp)
			}
			if resp.Header.Get("Content-Type") != "application/cbor" {
				t.Errorf("content type is %q", resp.Header.Get("Content-Type"))
			}
			body, _ := io.ReadAll(resp.Body)
			msg, err := protocol.Decode(body)
			if err != nil {
				t.Fatalf("the 401 body is not a protocol message: %v", err)
			}
			if e, ok := msg.(protocol.Error); !ok || e.C != protocol.CodeUnauthorized {
				t.Errorf("the 401 body is %#v, want err unauthorized", msg)
			}
		})
	}
}

func TestHelloThenTwoEventsAckTwo(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")

	c := h.dial(token)
	welcome := c.hello("tgt-01", 0)
	if welcome.Ack != 0 || welcome.Ses != nil {
		t.Errorf("welcome is %+v, want ack 0 and no session", welcome)
	}
	if drift := time.Since(time.UnixMilli(welcome.Now)); drift > 5*time.Second || drift < -5*time.Second {
		t.Errorf("welcome.now is %s away from the clock", drift)
	}
	c.waitAck(0)

	first, second := shotEvent(t, 1, 1), shotEvent(t, 2, 2)
	c.send(first)
	c.send(second)
	c.waitAck(2)

	stored, err := h.store.ListEvents(context.Background(), store.Filter{DeviceID: "tgt-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored %d events, want 2", len(stored))
	}
	for i, sent := range []protocol.Event{first, second} {
		got := stored[i]
		if got.ID != store.EventID(sent.ID) || got.Seq != sent.Seq || got.Kind != sent.K || got.TsDevice != sent.Ts {
			t.Errorf("event %d stored as %+v, sent as %+v", i, got, sent)
		}
		if !bytes.Equal(got.Payload, sent.D) {
			t.Errorf("event %d payload stored as %x, sent as %x", i, got.Payload, []byte(sent.D))
		}
		if got.ControllerID != "c-1" {
			t.Errorf("event %d controller is %q, want c-1 from the payload", i, got.ControllerID)
		}
	}

	if !h.online("tgt-01") {
		t.Error("the device is not listed online")
	}
	status := h.link.Online()[0]
	if status.LastAck != 2 || status.LastEventAt.IsZero() || status.ConnectedAt.IsZero() {
		t.Errorf("online status is %+v", status)
	}
	device, err := h.store.GetDevice(context.Background(), "tgt-01")
	if err != nil {
		t.Fatal(err)
	}
	if device.LastSeen == 0 || device.FirmwareVersion != "sim-1" {
		t.Errorf("last seen %d and firmware %q were not recorded", device.LastSeen, device.FirmwareVersion)
	}
}

func TestReconnectAtServerAckReplaysNothing(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	h.connectAndStore(token, "tgt-01", 2)

	c := h.dial(token)
	welcome := c.hello("tgt-01", 2)
	if welcome.Ack != 2 {
		t.Fatalf("welcome.ack is %d, want 2", welcome.Ack)
	}
	c.waitAck(2)

	// Nothing more is expected from the server, and nothing new is stored.
	if msg, err := c.read(300 * time.Millisecond); err == nil {
		t.Errorf("the server sent %#v after an empty replay", msg)
	}
	if n := h.countEvents("tgt-01"); n != 2 {
		t.Errorf("the journal holds %d events, want 2", n)
	}
}

func TestReconnectBehindServerIsSeqRegression(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	h.connectAndStore(token, "tgt-01", 2)

	c := h.dial(token)
	c.send(protocol.Hello{Dev: "tgt-01", FW: "sim-1", Cls: protocol.ClassESP, Last: 1, Proto: protocol.Version})
	e := c.expectError(protocol.CodeSeqRegression)
	if !strings.Contains(e.M, "1") || !strings.Contains(e.M, "2") {
		t.Errorf("the message %q does not name both sequence numbers", e.M)
	}
	eventually(t, 2*time.Second, "the device to go offline", func() bool { return !h.online("tgt-01") })
	if n := h.countEvents("tgt-01"); n != 2 {
		t.Errorf("the journal holds %d events, want 2", n)
	}
}

// TestResendAfterDropStoresOnlyTheMissingEvent is the replay case: the server
// stored 1 and 2, the connection dropped before 3 arrived, and the device
// resends everything it still holds.
func TestResendAfterDropStoresOnlyTheMissingEvent(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")

	first := h.dial(token)
	first.hello("tgt-01", 0)
	first.waitAck(0)
	first.send(shotEvent(t, 1, 1))
	first.send(shotEvent(t, 2, 2))
	first.waitAck(2)
	first.ws.CloseNow() // the drop, without a close handshake
	eventually(t, 2*time.Second, "the dropped connection to end", func() bool { return !h.online("tgt-01") })

	second := h.dial(token)
	welcome := second.hello("tgt-01", 3)
	if welcome.Ack != 2 {
		t.Fatalf("welcome.ack is %d, want 2", welcome.Ack)
	}
	for seq := uint64(1); seq <= 3; seq++ {
		second.send(shotEvent(t, seq, byte(seq)))
	}
	second.waitAck(3)

	if n := h.countEvents("tgt-01"); n != 3 {
		t.Fatalf("the journal holds %d events, want exactly 3", n)
	}

	// The disconnect log of the second connection counts one stored event and
	// two duplicates, all three replayed.
	second.ws.Close(websocket.StatusNormalClosure, "done")
	eventually(t, 2*time.Second, "the second connection to end", func() bool {
		return strings.Contains(h.logs.String(), "stored=1 duplicates=2 replayed=3")
	})
}

func TestConflictClosesWithBadMessage(t *testing.T) {
	tests := []struct {
		name  string
		event func(t *testing.T) protocol.Event
		seq   string
	}{
		{name: "seq reused by another event", event: func(t *testing.T) protocol.Event {
			return shotEvent(t, 2, 99)
		}, seq: "seq 2"},
		{name: "event id moved to another seq", event: func(t *testing.T) protocol.Event {
			moved := shotEvent(t, 3, 1)
			return moved
		}, seq: "seq 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, testConfig())
			token := h.addDevice("tgt-01")
			h.connectAndStore(token, "tgt-01", 2)

			c := h.dial(token)
			c.hello("tgt-01", 3)
			c.send(tt.event(t))
			e := c.expectError(protocol.CodeBadMessage)
			if !strings.Contains(e.M, tt.seq) {
				t.Errorf("the message %q does not name %s", e.M, tt.seq)
			}
			if n := h.countEvents("tgt-01"); n != 2 {
				t.Errorf("the journal holds %d events, want 2", n)
			}

			// The server is still there for an honest device.
			again := h.dial(token)
			if welcome := again.hello("tgt-01", 3); welcome.Ack != 2 {
				t.Fatalf("welcome.ack is %d, want 2", welcome.Ack)
			}
			again.send(shotEvent(t, 3, 3))
			again.waitAck(3)
		})
	}
}

func TestSendCommandReceivesResult(t *testing.T) {
	cfg := testConfig()
	cfg.CommandTimeout = 300 * time.Millisecond
	h := newHarness(t, cfg)
	token := h.addDevice("tgt-01")
	ctx := context.Background()

	if _, err := h.link.SendCommand(ctx, "tgt-01", protocol.CommandTimeMark, nil); !errors.Is(err, ErrDeviceOffline) {
		t.Errorf("a command to an offline device returned %v, want ErrDeviceOffline", err)
	}

	c := h.dial(token)
	c.hello("tgt-01", 0)

	// The device answers every command but the one named silent.
	seen := make(chan protocol.Command, 4)
	go func() {
		for {
			msg, err := c.read(10 * time.Second)
			if err != nil {
				return
			}
			cmd, ok := msg.(protocol.Command)
			if !ok {
				continue
			}
			seen <- cmd
			if cmd.N == "silent" {
				continue
			}
			frame, _ := protocol.Encode(protocol.Result{ID: cmd.ID, OK: true, R: map[string]any{"echo": cmd.N}})
			c.ws.Write(context.Background(), websocket.MessageBinary, frame)
		}
	}()

	result, err := h.link.SendCommand(ctx, "tgt-01", protocol.CommandTimeMark, map[string]any{"now": int64(1234)})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if !result.OK || result.ID != 1 || result.R["echo"] != protocol.CommandTimeMark {
		t.Errorf("result is %+v", result)
	}
	if cmd := <-seen; cmd.ID != 1 || cmd.A["now"] != uint64(1234) {
		t.Errorf("the device received %+v", cmd)
	}

	result, err = h.link.SendCommand(ctx, "tgt-01", protocol.CommandReboot, nil)
	if err != nil || result.ID != 2 {
		t.Errorf("second command returned %+v, %v, want id 2", result, err)
	}
	<-seen

	if _, err := h.link.SendCommand(ctx, "tgt-01", "silent", nil); !errors.Is(err, ErrCommandTimeout) {
		t.Errorf("an unanswered command returned %v, want ErrCommandTimeout", err)
	}
	if !strings.Contains(h.logs.String(), "command timed out") {
		t.Error("the timeout was not logged")
	}
}

func TestPingTimeoutMarksOffline(t *testing.T) {
	cfg := testConfig()
	cfg.PingInterval = 100 * time.Millisecond
	cfg.PongTimeout = 100 * time.Millisecond
	h := newHarness(t, cfg)
	token := h.addDevice("tgt-01")

	opts := h.dialOptions(token)
	var pings sync.WaitGroup
	pings.Add(1)
	var once sync.Once
	opts.OnPingReceived = func(context.Context, []byte) bool {
		once.Do(pings.Done)
		return false // never answer with a pong
	}
	c := h.dialWith(opts)
	c.hello("tgt-01", 0)
	if !h.online("tgt-01") {
		t.Fatal("the device is not online after hello")
	}

	// Keep reading, so the pings are seen and not answered.
	go func() {
		for {
			if _, err := c.read(10 * time.Second); err != nil {
				return
			}
		}
	}()
	pings.Wait()
	eventually(t, 3*time.Second, "the silent device to go offline", func() bool { return !h.online("tgt-01") })
	eventually(t, 2*time.Second, "the disconnect to be logged", func() bool {
		return strings.Contains(h.logs.String(), `reason="ping timeout"`)
	})
}

func TestAnsweredPingsKeepTheDevice(t *testing.T) {
	cfg := testConfig()
	cfg.PingInterval = 50 * time.Millisecond
	cfg.PongTimeout = 200 * time.Millisecond
	h := newHarness(t, cfg)
	token := h.addDevice("tgt-01")

	c := h.dial(token)
	c.hello("tgt-01", 0)
	go func() {
		for {
			if _, err := c.read(10 * time.Second); err != nil {
				return
			}
		}
	}()
	time.Sleep(600 * time.Millisecond)
	if !h.online("tgt-01") {
		t.Error("a device that answers pings was dropped")
	}
}

func TestHelloIsRequired(t *testing.T) {
	cfg := testConfig()
	cfg.HelloTimeout = 200 * time.Millisecond
	h := newHarness(t, cfg)
	token := h.addDevice("tgt-01")
	other := h.addDevice("tgt-02")

	t.Run("nothing sent", func(t *testing.T) {
		c := h.dial(token)
		start := time.Now()
		c.expectClosed(3 * time.Second)
		if waited := time.Since(start); waited > 2*time.Second {
			t.Errorf("the connection lived %s without a hello", waited)
		}
	})
	t.Run("event before hello", func(t *testing.T) {
		c := h.dial(token)
		c.send(shotEvent(t, 1, 1))
		c.expectError(protocol.CodeBadMessage)
	})
	t.Run("protocol 2", func(t *testing.T) {
		c := h.dial(token)
		c.send(protocol.Hello{Dev: "tgt-01", Cls: protocol.ClassESP, Proto: 2})
		c.expectError(protocol.CodeProtoUnsupported)
	})
	t.Run("hello for another device", func(t *testing.T) {
		c := h.dial(other)
		c.send(protocol.Hello{Dev: "tgt-01", Cls: protocol.ClassESP, Proto: protocol.Version})
		c.expectError(protocol.CodeUnauthorized)
	})
	t.Run("unknown class", func(t *testing.T) {
		c := h.dial(token)
		c.send(protocol.Hello{Dev: "tgt-01", Cls: "toaster", Proto: protocol.Version})
		c.expectError(protocol.CodeBadMessage)
	})
}

func TestBadFramesAfterHello(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")

	tests := []struct {
		name string
		send func(c *client)
		code string
		want websocket.StatusCode
	}{
		{name: "unknown type", code: protocol.CodeUnknownType, send: func(c *client) {
			// {"t":"nope"}
			c.sendRaw(websocket.MessageBinary, []byte{0xa1, 0x61, 0x74, 0x64, 0x6e, 0x6f, 0x70, 0x65})
		}},
		{name: "text frame", code: protocol.CodeBadMessage, send: func(c *client) {
			c.sendRaw(websocket.MessageText, []byte("hello"))
		}},
		{name: "not cbor", code: protocol.CodeBadMessage, send: func(c *client) {
			c.sendRaw(websocket.MessageBinary, []byte{0xff, 0x00})
		}},
		{name: "welcome from a device", code: protocol.CodeBadMessage, send: func(c *client) {
			c.send(protocol.Welcome{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := h.dial(token)
			c.hello("tgt-01", 0)
			c.waitAck(0)
			tt.send(c)
			c.expectError(tt.code)
		})
	}

	t.Run("frame above 4096 bytes", func(t *testing.T) {
		c := h.dial(token)
		c.ws.SetReadLimit(-1)
		c.hello("tgt-01", 0)
		c.waitAck(0)
		c.sendRaw(websocket.MessageBinary, make([]byte, protocol.MaxFrameSize+1))
		if status := c.expectClosed(3 * time.Second); status != websocket.StatusMessageTooBig {
			t.Errorf("close status is %d, want %d", status, websocket.StatusMessageTooBig)
		}
	})
}

func TestNewestConnectionWins(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")

	old := h.dial(token)
	old.hello("tgt-01", 0)
	old.waitAck(0)
	old.send(shotEvent(t, 1, 1))
	old.waitAck(1)

	fresh := h.dial(token)
	if welcome := fresh.hello("tgt-01", 1); welcome.Ack != 1 {
		t.Fatalf("welcome.ack is %d, want 1", welcome.Ack)
	}
	old.expectClosed(3 * time.Second)

	fresh.send(shotEvent(t, 2, 2))
	fresh.waitAck(2)
	if online := h.link.Online(); len(online) != 1 {
		t.Errorf("%d connections are listed for one device", len(online))
	}
}

func TestRevokedDeviceIsDropped(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")

	c := h.dial(token)
	c.hello("tgt-01", 0)
	c.waitAck(0)

	revoked := time.Now()
	if err := h.store.SetStatus(context.Background(), "tgt-01", store.StatusBlocked); err != nil {
		t.Fatal(err)
	}
	c.expectError(protocol.CodeUnauthorized)
	if took := time.Since(revoked); took > time.Second {
		t.Errorf("the revoked device was dropped after %s, want within a second", took)
	}
	eventually(t, time.Second, "the revoked device to go offline", func() bool { return !h.online("tgt-01") })

	opts := h.dialOptions(token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, resp, err := websocket.Dial(ctx, h.url, opts); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked device reconnected: %v", err)
	}
}

func TestWelcomeCarriesRunningSession(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	ctx := context.Background()
	if err := h.store.CreateSession(ctx, store.Session{ID: "s-1"}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.AddSessionDevice(ctx, "s-1", "tgt-01"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.StartSession(ctx, "s-1"); err != nil {
		t.Fatal(err)
	}

	c := h.dial(token)
	welcome := c.hello("tgt-01", 0)
	if welcome.Ses == nil || *welcome.Ses != "s-1" {
		t.Fatalf("welcome.ses is %v, want s-1", welcome.Ses)
	}
	c.waitAck(0)
	c.send(shotEvent(t, 1, 1))
	c.waitAck(1)

	events, err := h.store.ListEvents(ctx, store.Filter{SessionID: "s-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("%d events are in the running session, want 1", len(events))
	}
}

func TestCloseFlushesAndDisconnects(t *testing.T) {
	cfg := testConfig()
	cfg.AckInterval = time.Hour // only the close may flush
	h := newHarness(t, cfg)
	token := h.addDevice("tgt-01")

	c := h.dial(token)
	c.hello("tgt-01", 0)
	c.waitAck(0)
	for seq := uint64(1); seq <= 5; seq++ {
		c.send(shotEvent(t, seq, byte(seq)))
	}
	eventually(t, 2*time.Second, "the events to arrive", func() bool {
		online := h.link.Online()
		return len(online) == 1 && !online[0].LastEventAt.IsZero()
	})
	time.Sleep(100 * time.Millisecond)

	// A device reads all the time, so it answers the close handshake.
	closed := make(chan websocket.StatusCode, 1)
	go func() {
		for {
			if _, err := c.read(10 * time.Second); err != nil {
				closed <- websocket.CloseStatus(err)
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := h.link.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("Close took %s with a device that answers", took)
	}
	if status := <-closed; status != websocket.StatusGoingAway {
		t.Errorf("close status is %d, want %d", status, websocket.StatusGoingAway)
	}
	if n := h.countEvents("tgt-01"); n != 5 {
		t.Errorf("the journal holds %d events after the close, want 5", n)
	}

	// A closed server refuses new connections.
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	if _, resp, err := websocket.Dial(dialCtx, h.url, h.dialOptions(token)); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("a closed server accepted a device: %v", err)
	}
}

func TestBatchFlushesAtBatchSize(t *testing.T) {
	cfg := testConfig()
	cfg.AckInterval = time.Hour // only the batch size may flush
	cfg.AckBatch = 4
	h := newHarness(t, cfg)
	token := h.addDevice("tgt-01")

	c := h.dial(token)
	c.hello("tgt-01", 0)
	c.waitAck(0)
	for seq := uint64(1); seq <= 8; seq++ {
		c.send(shotEvent(t, seq, byte(seq)))
	}
	c.waitAck(4)
	c.waitAck(8)
	c.send(shotEvent(t, 9, 9))
	if msg, err := c.read(300 * time.Millisecond); err == nil {
		t.Errorf("a batch of one was flushed before its interval: %#v", msg)
	}
}

func TestWelcomeCarriesTheEpoch(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	c := h.dial(token)
	if welcome := c.hello("tgt-01", 0); welcome.Ep != 1 {
		t.Errorf("welcome.ep is %d, want 1 for a device that was never reset", welcome.Ep)
	}
}

// TestResetThenHelloFromZero is the reset of D-026: after the operator resets
// a device, a hello with last 0 is accepted and seq 1 counts again.
func TestResetThenHelloFromZero(t *testing.T) {
	h := newHarness(t, testConfig())
	token := h.addDevice("tgt-01")
	h.connectAndStore(token, "tgt-01", 2)

	if _, err := h.store.ResetDevice(context.Background(), "tgt-01"); err != nil {
		t.Fatal(err)
	}

	c := h.dial(token)
	welcome := c.hello("tgt-01", 0)
	if welcome.Ack != 0 || welcome.Ep != 2 {
		t.Fatalf("welcome after the reset is %+v, want ack 0 in epoch 2", welcome)
	}
	c.waitAck(0)
	c.send(shotEvent(t, 1, 11))
	c.send(shotEvent(t, 2, 12))
	c.waitAck(2)

	events, err := h.store.ListEvents(context.Background(), store.Filter{DeviceID: "tgt-01"})
	if err != nil {
		t.Fatal(err)
	}
	perEpoch := map[uint64]int{}
	for _, e := range events {
		perEpoch[e.SeqEpoch]++
	}
	if perEpoch[1] != 2 || perEpoch[2] != 2 {
		t.Errorf("events per epoch are %v, want 2 and 2", perEpoch)
	}
}

func TestResetDropsTheLiveConnection(t *testing.T) {
	tests := []struct {
		name   string
		action func(h *harness)
	}{
		{name: "reset seen by the watcher", action: func(h *harness) {
			if _, err := h.store.ResetDevice(context.Background(), "tgt-01"); err != nil {
				h.t.Fatal(err)
			}
		}},
		{name: "reset through Disconnect", action: func(h *harness) {
			if _, err := h.store.ResetDevice(context.Background(), "tgt-01"); err != nil {
				h.t.Fatal(err)
			}
			if !h.link.Disconnect("tgt-01", Reset) {
				h.t.Error("Disconnect found no connection")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.AckInterval = time.Hour // the events below stay pending
			h := newHarness(t, cfg)
			token := h.addDevice("tgt-01")

			c := h.dial(token)
			c.hello("tgt-01", 0)
			c.waitAck(0)
			c.send(shotEvent(t, 1, 1))
			c.send(shotEvent(t, 2, 2))
			eventually(t, 2*time.Second, "the events to arrive", func() bool {
				online := h.link.Online()
				return len(online) == 1 && !online[0].LastEventAt.IsZero()
			})

			start := time.Now()
			tt.action(h)
			if status := c.expectClosed(3 * time.Second); status != websocket.StatusServiceRestart {
				t.Errorf("close status is %d, want %d", status, websocket.StatusServiceRestart)
			}
			if took := time.Since(start); took > time.Second {
				t.Errorf("the reset device was dropped after %s", took)
			}
			eventually(t, 2*time.Second, "the device to go offline", func() bool { return !h.online("tgt-01") })

			// The pending events belonged to epoch 1 and were not stored in 2.
			if n := h.countEvents("tgt-01"); n != 0 {
				t.Errorf("%d events of the old epoch were stored", n)
			}

			again := h.dial(token)
			if welcome := again.hello("tgt-01", 2); welcome.Ep != 2 || welcome.Ack != 0 {
				t.Errorf("welcome after the reset is %+v, want ep 2 and ack 0", welcome)
			}
		})
	}
}

func TestTokenReplacedDropsTheDevice(t *testing.T) {
	tests := []struct {
		name       string
		disconnect bool
	}{
		{name: "seen by the watcher"},
		{name: "through Disconnect", disconnect: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, testConfig())
			oldToken := h.addDevice("tgt-01")

			c := h.dial(oldToken)
			c.hello("tgt-01", 0)
			c.waitAck(0)

			newToken, err := store.NewDeviceToken()
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if err := h.store.SetDeviceToken(context.Background(), "tgt-01", store.HashToken(newToken)); err != nil {
				t.Fatal(err)
			}
			if tt.disconnect && !h.link.Disconnect("tgt-01", TokenReplaced) {
				t.Error("Disconnect found no connection")
			}
			e := c.expectError(protocol.CodeUnauthorized)
			if !strings.Contains(e.M, "replaced") {
				t.Errorf("the message %q does not say the token was replaced", e.M)
			}
			if took := time.Since(start); took > time.Second {
				t.Errorf("the device was dropped after %s", took)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, resp, err := websocket.Dial(ctx, h.url, h.dialOptions(oldToken)); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("the old token still connects: %v", err)
			}
			fresh := h.dial(newToken)
			fresh.hello("tgt-01", 0)
			fresh.waitAck(0)
		})
	}
}

func TestDisconnectOfflineDevice(t *testing.T) {
	h := newHarness(t, testConfig())
	h.addDevice("tgt-01")
	if h.link.Disconnect("tgt-01", Reset) {
		t.Error("Disconnect reported a connection for an offline device")
	}
}
