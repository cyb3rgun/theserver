package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/cyb3rgun/theserver/internal/config"
	"github.com/cyb3rgun/theserver/internal/httpapi"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/protocol"
	"github.com/cyb3rgun/theserver/internal/store"
)

type harness struct {
	t      *testing.T
	st     *store.Store
	admin  *Admin
	key    []byte
	token  string
	id     string
	cookie *http.Cookie
	// lang, when set, is sent as the language cookie.
	lang string

	// settings is the configuration behind the API, kept in configPath.
	settings   *config.Runtime
	configPath string

	// Set by newLinkedHarness: a device link on a test server.
	link    *link.Server
	linkSrv *httptest.Server
	linkURL string
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return buildHarness(t, false)
}

// newLinkedHarness is newHarness with a running device link behind the API.
func newLinkedHarness(t *testing.T) *harness {
	t.Helper()
	return buildHarness(t, true)
}

func buildHarness(t *testing.T, withLink bool) *harness {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	row, token, err := st.AddAdminToken(context.Background(), "founder")
	if err != nil {
		t.Fatal(err)
	}
	var (
		deviceLink *link.Server
		linkSrv    *httptest.Server
	)
	if withLink {
		deviceLink = link.New(st, link.DefaultConfig(), quiet())
		mux := http.NewServeMux()
		mux.Handle(link.Path, deviceLink)
		linkSrv = httptest.NewTLSServer(mux)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			deviceLink.Close(ctx)
			linkSrv.Close()
		})
	}
	configPath := filepath.Join(t.TempDir(), "theserver.toml")
	if err := config.Write(configPath, nil); err != nil {
		t.Fatal(err)
	}
	cfg, sources, err := config.LoadWithSources(configPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := config.NewRuntime(cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	opts := httpapi.Options{
		Store:    st,
		Settings: runtime,
		Logger:   quiet(),
	}
	if deviceLink != nil {
		opts.Link = deviceLink
	}
	api := httpapi.New(opts)
	key := bytes.Repeat([]byte{7}, 32)
	a, err := New(Options{API: api.API(), Store: st, Key: key, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t: t, st: st, admin: a, key: key, token: token, id: row.ID,
		cookie: NewSessionCookie(key, row.ID, time.Now()),
		link:   deviceLink, linkSrv: linkSrv,
		settings: runtime, configPath: configPath,
	}
	if linkSrv != nil {
		h.linkURL = "wss" + strings.TrimPrefix(linkSrv.URL, "https") + link.Path
	}
	return h
}

// do sends a request, with the session cookie when withCookie is set.
func (h *harness) do(method, path string, form url.Values, withCookie bool, headers ...string) *httptest.ResponseRecorder {
	h.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	if withCookie {
		req.AddCookie(h.cookie)
	}
	if h.lang != "" {
		req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: h.lang})
	}
	rec := httptest.NewRecorder()
	h.admin.ServeHTTP(rec, req)
	return rec
}

func (h *harness) html(method, path string, form url.Values) string {
	h.t.Helper()
	rec := h.do(method, path, form, true)
	if rec.Code != http.StatusOK {
		h.t.Fatalf("%s %s answered %d: %s", method, path, rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		h.t.Fatalf("%s %s has content type %q", method, path, ct)
	}
	return rec.Body.String()
}

func contains(t *testing.T, page string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

var protected = []struct{ method, path string }{
	{"GET", "/admin/devices"},
	{"GET", "/admin/devices/table"},
	{"POST", "/admin/devices/tgt-01/approve"},
	{"POST", "/admin/devices/tgt-01/block"},
	{"POST", "/admin/devices/tgt-01/reset"},
	{"POST", "/admin/devices/tgt-01/token"},
	{"GET", "/admin/sessions"},
	{"POST", "/admin/sessions"},
	{"POST", "/admin/sessions/s-1/start"},
	{"POST", "/admin/sessions/s-1/stop"},
	{"POST", "/admin/sessions/s-1/devices"},
	{"GET", "/admin/ranking"},
	{"GET", "/admin/ranking/table"},
	{"GET", "/admin/settings"},
	{"POST", "/admin/settings"},
	{"POST", "/admin/settings/reset"},
}

func TestLoginRequiredEverywhere(t *testing.T) {
	h := newHarness(t)
	h.device("tgt-01", store.StatusApproved)
	if err := h.st.CreateSession(context.Background(), store.Session{ID: "s-1"}); err != nil {
		t.Fatal(err)
	}

	bad := map[string]*http.Cookie{
		"no cookie":       nil,
		"tampered cookie": {Name: CookieName, Value: strings.Replace(h.cookie.Value, "A", "B", 1) + "x"},
		"garbage cookie":  {Name: CookieName, Value: "nothing.here"},
		"expired cookie":  NewSessionCookie(h.key, h.id, time.Now().Add(-13*time.Hour)),
		"other key":       NewSessionCookie(bytes.Repeat([]byte{8}, 32), h.id, time.Now()),
		"unknown token":   NewSessionCookie(h.key, "adm-00000000", time.Now()),
	}
	for name, cookie := range bad {
		for _, p := range protected {
			req := httptest.NewRequest(p.method, p.path, nil)
			if cookie != nil {
				req.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			h.admin.ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
				t.Errorf("%s: %s %s answered %d to %q", name, p.method, p.path, rec.Code, rec.Header().Get("Location"))
			}

			req = httptest.NewRequest(p.method, p.path, nil)
			req.Header.Set("HX-Request", "true")
			if cookie != nil {
				req.AddCookie(cookie)
			}
			rec = httptest.NewRecorder()
			h.admin.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") != "/admin/login" {
				t.Errorf("%s: htmx %s %s answered %d, HX-Redirect %q", name, p.method, p.path, rec.Code, rec.Header().Get("HX-Redirect"))
			}
		}
	}

	// Nothing was changed by the refused requests.
	device, _ := h.st.GetDevice(context.Background(), "tgt-01")
	session, _ := h.st.GetSession(context.Background(), "s-1")
	if device.Status != store.StatusApproved || device.SeqEpoch != 1 || session.State != store.SessionCreated {
		t.Errorf("a refused request changed state: %+v, %+v", device, session)
	}
}

func TestLoginAndLogout(t *testing.T) {
	h := newHarness(t)

	page := h.do("GET", "/admin/login", nil, false)
	if page.Code != 200 {
		t.Fatalf("login page answered %d", page.Code)
	}
	contains(t, page.Body.String(), `action="/admin/login"`, `type="password"`, `name="token"`)

	wrong := h.do("POST", "/admin/login", url.Values{"token": {"nope"}}, false)
	if wrong.Code != http.StatusUnauthorized || len(wrong.Result().Cookies()) != 0 {
		t.Errorf("a wrong token answered %d with cookies %v", wrong.Code, wrong.Result().Cookies())
	}
	contains(t, wrong.Body.String(), "not a valid admin token")

	ok := h.do("POST", "/admin/login", url.Values{"token": {h.token}}, false)
	if ok.Code != http.StatusSeeOther || ok.Header().Get("Location") != "/admin/devices" {
		t.Fatalf("login answered %d to %q", ok.Code, ok.Header().Get("Location"))
	}
	cookies := ok.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login set %d cookies", len(cookies))
	}
	c := cookies[0]
	if c.Name != CookieName || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode ||
		c.Path != "/admin" || c.MaxAge != int((12*time.Hour).Seconds()) {
		t.Errorf("the session cookie is %+v", c)
	}
	if strings.Contains(c.Value, h.token) {
		t.Error("the cookie carries the admin token")
	}
	h.cookie = c

	if rec := h.do("GET", "/admin/login", nil, true); rec.Code != http.StatusSeeOther {
		t.Errorf("the login page for a logged in admin answered %d", rec.Code)
	}
	contains(t, h.html("GET", "/admin/devices", nil), "founder", `action="/admin/logout"`)

	out := h.do("POST", "/admin/logout", url.Values{}, true)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/admin/login" {
		t.Errorf("logout answered %d to %q", out.Code, out.Header().Get("Location"))
	}
	gone := out.Result().Cookies()
	if len(gone) != 2 || gone[0].Name != CookieName || gone[0].MaxAge >= 0 || gone[1].Name != i18n.CookieName || gone[1].MaxAge >= 0 {
		t.Errorf("logout left the session or language cookie: %+v", gone)
	}

	if err := h.st.RevokeAdminToken(context.Background(), h.id); err != nil {
		t.Fatal(err)
	}
	revoked := h.do("POST", "/admin/login", url.Values{"token": {h.token}}, false)
	if revoked.Code != http.StatusForbidden {
		t.Errorf("a revoked token answered %d at login", revoked.Code)
	}
	contains(t, revoked.Body.String(), "revoked")
}

func TestRevokedTokenEndsTheSession(t *testing.T) {
	h := newHarness(t)
	h.html("GET", "/admin/devices", nil)
	if err := h.st.RevokeAdminToken(context.Background(), h.id); err != nil {
		t.Fatal(err)
	}
	rec := h.do("GET", "/admin/devices", nil, true)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
		t.Errorf("a revoked session answered %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func (h *harness) device(id, status string) {
	h.t.Helper()
	if err := h.st.UpsertDevice(context.Background(), store.Device{
		ID: id, Kind: store.KindTarget, Room: "hall", Status: status, TokenHash: store.HashToken(id),
	}); err != nil {
		h.t.Fatal(err)
	}
}

func TestDevicesPageAndActions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.device("tgt-01", store.StatusApproved)
	h.device("tgt-02", store.StatusPending)

	page := h.html("GET", "/admin/devices", nil)
	contains(t, page, "tgt-01", "tgt-02", "approved", "pending", "offline", `hx-trigger="every 5s"`,
		`hx-post="/admin/devices/tgt-01/reset"`, `hx-post="/admin/devices/tgt-02/approve"`,
		`hx-post="/admin/devices/tgt-01/token"`, "htmx.min.js", `integrity="sha384-`)
	if strings.Contains(page, `hx-post="/admin/devices/tgt-01/approve"`) {
		t.Error("an approved device offers Approve")
	}

	table := h.html("GET", "/admin/devices/table", nil)
	if strings.Contains(table, "<html") || !strings.HasPrefix(strings.TrimSpace(table), `<section id="devices"`) {
		t.Errorf("the table fragment is a whole page or lacks its section: %.80s", table)
	}

	approved := h.html("POST", "/admin/devices/tgt-02/approve", url.Values{})
	contains(t, approved, "Device tgt-02 is approved.")
	if d, _ := h.st.GetDevice(ctx, "tgt-02"); d.Status != store.StatusApproved {
		t.Errorf("tgt-02 is %s after approve", d.Status)
	}

	reset := h.html("POST", "/admin/devices/tgt-01/reset", url.Values{})
	contains(t, reset, "starts over at seq 1 in epoch 2")
	if d, _ := h.st.GetDevice(ctx, "tgt-01"); d.SeqEpoch != 2 {
		t.Errorf("tgt-01 is in epoch %d after reset", d.SeqEpoch)
	}

	blocked := h.html("POST", "/admin/devices/tgt-02/block", url.Values{})
	contains(t, blocked, "Device tgt-02 is blocked")

	dialog := h.html("POST", "/admin/devices/tgt-01/token", url.Values{})
	contains(t, dialog, "<dialog open", "New token for tgt-01", `method="dialog"`, "shown once")
	token := regexp.MustCompile(`id="new-token">([A-Za-z0-9_-]{43})<`).FindStringSubmatch(dialog)
	if token == nil {
		t.Fatalf("no token in the dialog: %s", dialog)
	}
	if d, err := h.st.DeviceByToken(ctx, token[1]); err != nil || d.ID != "tgt-01" {
		t.Errorf("the shown token finds %q, %v", d.ID, err)
	}
	if _, err := h.st.DeviceByToken(ctx, "tgt-01"); err == nil {
		t.Error("the old token still works")
	}

	missing := h.html("POST", "/admin/devices/nobody/reset", url.Values{})
	contains(t, missing, `class="error"`, "Not found.")
	unknown := h.html("POST", "/admin/devices/tgt-01/explode", url.Values{})
	contains(t, unknown, "Unknown action explode")
	noToken := h.html("POST", "/admin/devices/nobody/token", url.Values{})
	contains(t, noToken, "No new token", "Not found.")
}

func TestSessionsPageAndActions(t *testing.T) {
	h := newHarness(t)
	h.device("tgt-01", store.StatusApproved)

	page := h.html("GET", "/admin/sessions", nil)
	contains(t, page, "No sessions yet.", `hx-post="/admin/sessions"`)

	created := h.html("POST", "/admin/sessions", url.Values{"id": {"evening"}, "scenario": {"range"}, "room": {"hall"}})
	contains(t, created, "Session evening is created.", "evening", "range", "created",
		`hx-post="/admin/sessions/evening/start"`, `hx-post="/admin/sessions/evening/devices"`, `<option value="tgt-01">`)

	duplicate := h.html("POST", "/admin/sessions", url.Values{"id": {"evening"}})
	contains(t, duplicate, `class="error"`, "exists")

	added := h.html("POST", "/admin/sessions/evening/devices", url.Values{"device_id": {"tgt-01"}})
	contains(t, added, "Device tgt-01 is in session evening.")

	started := h.html("POST", "/admin/sessions/evening/start", url.Values{})
	contains(t, started, "Session evening is running.", `hx-post="/admin/sessions/evening/stop"`)
	again := h.html("POST", "/admin/sessions/evening/start", url.Values{})
	contains(t, again, `class="error"`, "This is not possible in the current state.")
	if strings.Contains(again, "is running.") {
		t.Error("a refused start still shows the success notice")
	}

	stopped := h.html("POST", "/admin/sessions/evening/stop", url.Values{})
	contains(t, stopped, "Session evening is stopped.", "stopped")

	session, err := h.st.GetSession(context.Background(), "evening")
	if err != nil || session.State != store.SessionStopped || len(session.Devices) != 1 {
		t.Errorf("the session is %+v, %v", session, err)
	}

	generated := h.html("POST", "/admin/sessions", url.Values{})
	if !regexp.MustCompile(`Session s-[0-9a-f]{6} is created\.`).MatchString(generated) {
		t.Errorf("a session without id did not get one: %s", generated)
	}
}

// TestRankingFragmentRendersSeededData checks the live ranking with events
// written straight into the journal.
func TestRankingFragmentRendersSeededData(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.device("tgt-01", store.StatusApproved)
	if err := h.st.CreateSession(ctx, store.Session{ID: "s-1"}); err != nil {
		t.Fatal(err)
	}
	var events []store.Event
	add := func(seq uint64, kind, session string, data any) {
		payload, err := protocol.EncodeData(data)
		if err != nil {
			t.Fatal(err)
		}
		ctl, _ := protocol.ControllerID(payload)
		var id store.EventID
		id[6], id[8], id[15] = 0x40, 0x80, byte(seq)
		events = append(events, store.Event{ID: id, Seq: seq, Kind: kind, ControllerID: ctl,
			SessionID: session, TsDevice: 1_700_000_000_000, Payload: payload})
	}
	add(1, protocol.KindHit, "s-1", protocol.HitData{Ctl: "ctl-07", Cseq: 1, Zone: "head", Pts: 100})
	add(2, protocol.KindHit, "s-1", protocol.HitData{Ctl: "ctl-07", Cseq: 2, Zone: "arm", Pts: 25})
	add(3, protocol.KindMiss, "s-1", protocol.ShotData{Ctl: "ctl-07", Cseq: 3})
	add(4, protocol.KindHit, "s-1", protocol.HitData{Ctl: "ctl-09", Cseq: 1, Zone: "torso", Pts: 50})
	add(5, protocol.KindHit, "", protocol.HitData{Ctl: "ctl-11", Cseq: 1, Zone: "head", Pts: 100})
	if _, err := h.st.AppendEvents(ctx, "tgt-01", events); err != nil {
		t.Fatal(err)
	}

	fragment := h.html("GET", "/admin/ranking/table?session=s-1", nil)
	if strings.Contains(fragment, "<html") {
		t.Error("the fragment is a whole page")
	}
	contains(t, fragment, `<section id="ranking"`, `hx-get="/admin/ranking/table?session=s-1"`, `hx-trigger="every 2s"`,
		"ctl-07", "125", "ctl-09", "50", "67 %", "Session s-1", "<tfoot>", ">175<")
	if strings.Contains(fragment, "ctl-11") {
		t.Error("the ranking of s-1 shows a controller of no session")
	}
	if strings.Index(fragment, "ctl-07") > strings.Index(fragment, "ctl-09") {
		t.Error("ctl-07 with 125 points is not placed above ctl-09 with 50")
	}

	all := h.html("GET", "/admin/ranking/table", nil)
	contains(t, all, "ctl-11", "All sessions", `hx-get="/admin/ranking/table?session="`)

	page := h.html("GET", "/admin/ranking?session=s-1", nil)
	contains(t, page, `<option value="s-1" selected>`, `hx-get="/admin/ranking/table"`, `hx-include="this"`, "ctl-07")

	missing := h.html("GET", "/admin/ranking/table?session=nothing", nil)
	contains(t, missing, `class="error"`, "No hits or misses yet.")
}

func TestStaticFilesAndHeaders(t *testing.T) {
	h := newHarness(t)

	js := h.do("GET", "/admin/static/htmx.min.js", nil, false)
	if js.Code != 200 || !strings.Contains(js.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("htmx answered %d with %q", js.Code, js.Header().Get("Content-Type"))
	}
	sum := sha256.Sum256(js.Body.Bytes())
	if hex.EncodeToString(sum[:]) != HTMXSHA256 {
		t.Errorf("the served htmx has SHA-256 %x, D-027 records %s", sum, HTMXSHA256)
	}
	if !bytes.HasPrefix(js.Body.Bytes(), []byte("var htmx=")) {
		t.Error("the served file does not look like htmx")
	}
	css := h.do("GET", "/admin/static/admin.css", nil, false)
	if css.Code != 200 || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") {
		t.Errorf("the stylesheet answered %d with %q", css.Code, css.Header().Get("Content-Type"))
	}

	page := h.do("GET", "/admin/devices", nil, true)
	csp := page.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "style-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the CSP %q lacks %s", csp, want)
		}
	}
	if page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("page headers are %v", page.Header())
	}
	if strings.Contains(page.Body.String(), "<script>") || strings.Contains(page.Body.String(), "style=") {
		t.Error("a page carries inline script or style, which the CSP forbids")
	}

	h.device("tgt-01", store.StatusApproved)
	cross := h.do("POST", "/admin/devices/tgt-01/reset", url.Values{}, true, "Sec-Fetch-Site", "cross-site")
	if cross.Code != http.StatusForbidden {
		t.Errorf("a cross site POST answered %d, want 403", cross.Code)
	}
	if d, _ := h.st.GetDevice(context.Background(), "tgt-01"); d.SeqEpoch != 1 {
		t.Error("the cross site POST reset the device")
	}
	same := h.do("POST", "/admin/devices/tgt-01/reset", url.Values{}, true, "Sec-Fetch-Site", "same-origin")
	if same.Code != http.StatusOK {
		t.Errorf("a same origin POST answered %d", same.Code)
	}
}

func TestRootRedirects(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/admin", "/admin/"} {
		rec := h.do("GET", path, nil, false)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/devices" {
			t.Errorf("%s answered %d to %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", KeyFileName)
	first, err := LoadOrCreateKey(path)
	if err != nil || len(first) != 32 {
		t.Fatalf("LoadOrCreateKey returned %d bytes, %v", len(first), err)
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(first, again) {
		t.Errorf("the second call gave another key: %v", err)
	}
	short := filepath.Join(t.TempDir(), KeyFileName)
	if err := os.WriteFile(short, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(short); err == nil {
		t.Error("a short key was accepted")
	}
	if _, err := New(Options{API: http.NotFoundHandler(), Store: &store.Store{}, Key: []byte("short")}); err == nil {
		t.Error("New accepted a short key")
	}
}

// connectDevice registers id as an approved target, connects it to the link
// and reads its welcome and first ack. The test then reads nothing, so a close
// from the server cannot finish its handshake until expectClosed reads again.
func (h *harness) connectDevice(id string) *websocket.Conn {
	h.t.Helper()
	ctx := context.Background()
	token := "token-of-" + id
	if _, err := h.st.GetDevice(ctx, id); errors.Is(err, store.ErrDeviceNotFound) {
		if err := h.st.UpsertDevice(ctx, store.Device{
			ID: id, Kind: store.KindTarget, Class: store.ClassESP,
			Status: store.StatusApproved, TokenHash: store.HashToken(token),
		}); err != nil {
			h.t.Fatal(err)
		}
	}
	d, err := h.st.GetDevice(ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}
	if !bytes.Equal(d.TokenHash, store.HashToken(token)) {
		if err := h.st.SetDeviceToken(ctx, id, store.HashToken(token)); err != nil {
			h.t.Fatal(err)
		}
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	ws, _, err := websocket.Dial(dialCtx, h.linkURL, &websocket.DialOptions{
		HTTPClient: h.linkSrv.Client(), HTTPHeader: header,
	})
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	h.t.Cleanup(func() { ws.CloseNow() })
	hello, err := protocol.Encode(protocol.Hello{Dev: id, FW: "test", Cls: protocol.ClassESP, Last: 0, Proto: protocol.Version})
	if err != nil {
		h.t.Fatal(err)
	}
	if err := ws.Write(dialCtx, websocket.MessageBinary, hello); err != nil {
		h.t.Fatal(err)
	}
	for _, want := range []string{protocol.TypeWelcome, protocol.TypeAck} {
		_, frame, err := ws.Read(dialCtx)
		if err != nil {
			h.t.Fatalf("waiting for %s: %v", want, err)
		}
		msg, err := protocol.Decode(frame)
		if err != nil || msg.Type() != want {
			h.t.Fatalf("got %v, %v, want %s", msg, err, want)
		}
	}
	return ws
}

func expectClosed(t *testing.T, ws *websocket.Conn, want websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if got := websocket.CloseStatus(err); got != want {
				t.Errorf("the device connection ended with %d (%v), want %d", got, err, want)
			}
			return
		}
	}
}

func deviceRow(t *testing.T, page, id string) string {
	t.Helper()
	row := regexp.MustCompile(`(?s)<tr id="device-` + regexp.QuoteMeta(id) + `">.*?</tr>`).FindString(page)
	if row == "" {
		t.Fatalf("the page has no row for %s: %s", id, page)
	}
	return row
}

// After a reset or a new token the answer must already show the device
// offline, while its connection may still be closing.
func TestDeviceIsOfflineAtOnceAfterResetAndNewToken(t *testing.T) {
	h := newLinkedHarness(t)
	ws := h.connectDevice("tgt-01")
	contains(t, deviceRow(t, h.html("GET", "/admin/devices/table", nil), "tgt-01"), "badge online")

	page := h.html("POST", "/admin/devices/tgt-01/reset", url.Values{})
	row := deviceRow(t, page, "tgt-01")
	contains(t, row, "badge offline", `<td class="number">2</td>`, `<span class="muted">none</span>`)
	if strings.Contains(row, "badge online") {
		t.Errorf("right after the reset the row still shows the device online: %s", row)
	}
	expectClosed(t, ws, websocket.StatusServiceRestart)

	ws = h.connectDevice("tgt-01")
	contains(t, deviceRow(t, h.html("GET", "/admin/devices/table", nil), "tgt-01"), "badge online")

	page = h.html("POST", "/admin/devices/tgt-01/token", url.Values{})
	contains(t, page, "<dialog open", `id="new-token"`,
		`<hx-partial hx-target="#devices" hx-swap="outerHTML">`, "Device tgt-01 has a new token; its connection was closed.")
	row = deviceRow(t, page, "tgt-01")
	contains(t, row, "badge offline", `<span class="muted">none</span>`)
	if strings.Contains(row, "badge online") {
		t.Errorf("right after the new token the row still shows the device online: %s", row)
	}
	expectClosed(t, ws, websocket.StatusPolicyViolation)
}

// A new login lasts as long as admin.session_hours says at that moment.
func TestSessionLifetimeFollowsTheSetting(t *testing.T) {
	h := newHarness(t)
	hours := 3
	h.admin.opts.SessionLifetime = func() time.Duration { return time.Duration(hours) * time.Hour }

	page := h.do("GET", "/admin/login", nil, false)
	contains(t, page.Body.String(), "The login lasts 3 hours in this browser.")
	login := h.do("POST", "/admin/login", url.Values{"token": {h.token}}, false)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != 3*3600 {
		t.Fatalf("the login cookie is %+v", cookies)
	}
	id, ok := tokenIDFromCookie(h.key, cookies[0].Value, time.Now().Add(2*time.Hour))
	if !ok || id != h.id {
		t.Error("the cookie is not valid within its hours")
	}
	if _, ok := tokenIDFromCookie(h.key, cookies[0].Value, time.Now().Add(3*time.Hour+time.Minute)); ok {
		t.Error("the cookie outlives its hours")
	}

	hours = 0
	login = h.do("POST", "/admin/login", url.Values{"token": {h.token}}, false)
	if c := login.Result().Cookies(); len(c) != 1 || c[0].MaxAge != int(SessionLifetime.Seconds()) {
		t.Errorf("without a usable setting the cookie is %+v", c)
	}
}
