package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/config"
	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/protocol"
	"github.com/cyb3rgun/theserver/internal/simtarget"
	"github.com/cyb3rgun/theserver/internal/store"
)

type apiHarness struct {
	t     *testing.T
	st    *store.Store
	link  *fakeLink
	srv   *Server
	token string
	admin store.AdminToken
}

func newAPI(t *testing.T) *apiHarness {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	admin, token, err := st.AddAdminToken(context.Background(), "tests")
	if err != nil {
		t.Fatal(err)
	}
	fl := &fakeLink{}
	srv := New(Options{
		Store:    st,
		Link:     fl,
		Settings: func() []config.Setting { return config.Describe(config.Default(), config.Sources{}) },
		Logger:   quiet(),
	})
	return &apiHarness{t: t, st: st, link: fl, srv: srv, token: token, admin: admin}
}

// call sends a request with the harness token and returns the recorder.
func (h *apiHarness) call(method, path, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.callAs(method, path, body, "Bearer "+h.token)
}

func (h *apiHarness) callAs(method, path, body, authorization string) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

func (h *apiHarness) device(id, status string, tokenHash []byte) {
	h.t.Helper()
	err := h.st.UpsertDevice(context.Background(), store.Device{
		ID: id, Kind: store.KindTarget, Room: "hall", Status: status, TokenHash: tokenHash,
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return v
}

func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	body := decode[ErrorBody](t, rec, status)
	if body.Error.Code != code || body.Error.Message == "" {
		t.Errorf("error body is %+v, want code %s with a message", body, code)
	}
}

// concrete turns a route pattern into a path that reaches it.
func concrete(path string) string {
	return Prefix + strings.NewReplacer("{id}", "x-1").Replace(path)
}

func TestEveryRouteNeedsAValidToken(t *testing.T) {
	h := newAPI(t)
	old, revokedToken, err := h.st.AddAdminToken(context.Background(), "old")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.RevokeAdminToken(context.Background(), old.ID); err != nil {
		t.Fatal(err)
	}

	for _, route := range Routes() {
		path := concrete(route.Path)
		if !route.Auth {
			if rec := h.callAs(route.Method, path, "", ""); rec.Code != http.StatusOK {
				t.Errorf("%s %s without a token answered %d, want 200", route.Method, path, rec.Code)
			}
			continue
		}
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			rec := h.callAs(route.Method, path, "", "")
			expectError(t, rec, http.StatusUnauthorized, codeUnauthorized)
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("401 without WWW-Authenticate")
			}
			expectError(t, h.callAs(route.Method, path, "", "Bearer not-a-token"), http.StatusUnauthorized, codeUnauthorized)
			expectError(t, h.callAs(route.Method, path, "", "Basic "+h.token), http.StatusUnauthorized, codeUnauthorized)
			expectError(t, h.callAs(route.Method, path, "", "Bearer "+revokedToken), http.StatusForbidden, codeForbidden)
		})
	}
}

func TestAsAdminAuthorizesInProcess(t *testing.T) {
	h := newAPI(t)
	call := func(ctx context.Context) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, Prefix+"/devices", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		h.srv.API().ServeHTTP(rec, req)
		return rec
	}
	if rec := call(AsAdmin(context.Background(), h.admin.ID)); rec.Code != http.StatusOK {
		t.Errorf("an in process request answered %d, want 200", rec.Code)
	}
	expectError(t, call(AsAdmin(context.Background(), "adm-unknown")), http.StatusUnauthorized, codeUnauthorized)
	if err := h.st.RevokeAdminToken(context.Background(), h.admin.ID); err != nil {
		t.Fatal(err)
	}
	expectError(t, call(AsAdmin(context.Background(), h.admin.ID)), http.StatusForbidden, codeForbidden)
}

func TestDeviceRoutes(t *testing.T) {
	h := newAPI(t)
	ctx := context.Background()
	h.device("tgt-01", store.StatusPending, store.HashToken("first"))
	h.device("tgt-02", store.StatusApproved, nil)
	h.link.online = []link.DeviceStatus{{DeviceID: "tgt-02", ConnectedAt: time.UnixMilli(5000), LastAck: 7}}

	list := decode[struct{ Devices []Device }](t, h.call("GET", Prefix+"/devices", ""), 200)
	if len(list.Devices) != 2 || list.Devices[0].ID != "tgt-01" || list.Devices[1].ID != "tgt-02" {
		t.Fatalf("devices are %+v", list.Devices)
	}
	one, two := list.Devices[0], list.Devices[1]
	if one.Online || !one.HasToken || one.SeqEpoch != 1 || one.Status != "pending" || one.Room != "hall" {
		t.Errorf("tgt-01 is %+v", one)
	}
	if !two.Online || two.HasToken || two.ConnectedAt != 5000 || two.LastAck != 7 {
		t.Errorf("tgt-02 is %+v", two)
	}
	if strings.Contains(h.call("GET", Prefix+"/devices", "").Body.String(), "token_hash") {
		t.Error("the device list shows a token hash")
	}

	got := decode[Device](t, h.call("GET", Prefix+"/devices/tgt-01", ""), 200)
	if got.ID != "tgt-01" {
		t.Errorf("GET /devices/tgt-01 returned %+v", got)
	}
	for _, path := range []string{"/devices/nobody", "/devices/nobody/approve", "/devices/nobody/block", "/devices/nobody/reset", "/devices/nobody/token"} {
		method := "POST"
		if path == "/devices/nobody" {
			method = "GET"
		}
		expectError(t, h.call(method, Prefix+path, ""), http.StatusNotFound, codeNotFound)
	}

	approved := decode[Device](t, h.call("POST", Prefix+"/devices/tgt-01/approve", ""), 200)
	if approved.Status != store.StatusApproved {
		t.Errorf("after approve the status is %s", approved.Status)
	}

	blocked := decode[Device](t, h.call("POST", Prefix+"/devices/tgt-02/block", ""), 200)
	if why, ok := h.link.disconnected["tgt-02"]; blocked.Status != store.StatusBlocked || !ok || why != link.Revoked {
		t.Errorf("block left %+v and disconnect %v", blocked, h.link.disconnected)
	}

	reset := decode[Device](t, h.call("POST", Prefix+"/devices/tgt-01/reset", ""), 200)
	if reset.SeqEpoch != 2 || h.link.disconnected["tgt-01"] != link.Reset {
		t.Errorf("reset left %+v and disconnect %v", reset, h.link.disconnected)
	}

	delete(h.link.disconnected, "tgt-01")
	rec := h.call("POST", Prefix+"/devices/tgt-01/token", "")
	fresh := decode[NewToken](t, rec, 200)
	if fresh.DeviceID != "tgt-01" || len(fresh.Token) != 43 || fresh.Warning == "" {
		t.Fatalf("new token answer is %+v", fresh)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the new token may be cached")
	}
	if d, err := h.st.DeviceByToken(ctx, fresh.Token); err != nil || d.ID != "tgt-01" {
		t.Errorf("the new token finds %q, %v", d.ID, err)
	}
	if _, err := h.st.DeviceByToken(ctx, "first"); err == nil {
		t.Error("the old token still finds the device")
	}
	if h.link.disconnected["tgt-01"] != link.TokenReplaced {
		t.Errorf("the token change disconnected with %v", h.link.disconnected["tgt-01"])
	}
}

func TestSessionRoutes(t *testing.T) {
	h := newAPI(t)
	h.device("tgt-01", store.StatusApproved, nil)

	rec := h.call("POST", Prefix+"/sessions", `{"id":"evening","scenario":"range","room":"hall"}`)
	created := decode[Session](t, rec, http.StatusCreated)
	if created.ID != "evening" || created.State != store.SessionCreated || created.Scenario != "range" || created.Devices == nil {
		t.Errorf("created %+v", created)
	}
	if rec.Header().Get("Location") != Prefix+"/sessions/evening" {
		t.Errorf("Location is %q", rec.Header().Get("Location"))
	}

	generated := decode[Session](t, h.call("POST", Prefix+"/sessions", `{}`), http.StatusCreated)
	if !strings.HasPrefix(generated.ID, "s-") || len(generated.ID) != 8 {
		t.Errorf("the server chose the id %q", generated.ID)
	}

	expectError(t, h.call("POST", Prefix+"/sessions", `{"id":"evening"}`), http.StatusConflict, codeConflict)
	for _, body := range []string{``, `{`, `{"id":"a b"}`, `{"id":"x","color":"red"}`, `{} {}`, `[]`} {
		expectError(t, h.call("POST", Prefix+"/sessions", body), http.StatusBadRequest, codeBadRequest)
	}

	added := decode[Session](t, h.call("POST", Prefix+"/sessions/evening/devices", `{"device_id":"tgt-01"}`), 200)
	if len(added.Devices) != 1 || added.Devices[0] != "tgt-01" {
		t.Errorf("devices after add are %v", added.Devices)
	}
	decode[Session](t, h.call("POST", Prefix+"/sessions/evening/devices", `{"device_id":"tgt-01"}`), 200)
	expectError(t, h.call("POST", Prefix+"/sessions/evening/devices", `{"device_id":"nobody"}`), 404, codeNotFound)
	expectError(t, h.call("POST", Prefix+"/sessions/evening/devices", `{}`), 400, codeBadRequest)
	expectError(t, h.call("POST", Prefix+"/sessions/nothing/devices", `{"device_id":"tgt-01"}`), 404, codeNotFound)

	started := decode[Session](t, h.call("POST", Prefix+"/sessions/evening/start", ""), 200)
	if started.State != store.SessionRunning || started.StartedAt == 0 {
		t.Errorf("started %+v", started)
	}
	expectError(t, h.call("POST", Prefix+"/sessions/evening/start", ""), http.StatusConflict, codeBadTransition)
	stopped := decode[Session](t, h.call("POST", Prefix+"/sessions/evening/stop", ""), 200)
	if stopped.State != store.SessionStopped || stopped.EndedAt == 0 {
		t.Errorf("stopped %+v", stopped)
	}
	expectError(t, h.call("POST", Prefix+"/sessions/nothing/start", ""), 404, codeNotFound)
	expectError(t, h.call("POST", Prefix+"/sessions/nothing/stop", ""), 404, codeNotFound)

	list := decode[struct{ Sessions []Session }](t, h.call("GET", Prefix+"/sessions", ""), 200)
	if len(list.Sessions) != 2 {
		t.Errorf("GET /sessions listed %d sessions, want 2", len(list.Sessions))
	}
}

// seedEvents stores hits and misses of two controllers for tgt-01, half of
// them in session s-1.
func seedEvents(t *testing.T, h *apiHarness) {
	t.Helper()
	ctx := context.Background()
	h.device("tgt-01", store.StatusApproved, nil)
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
		events = append(events, store.Event{
			ID: id, Seq: seq, Kind: kind, ControllerID: ctl, SessionID: session,
			TsDevice: int64(seq), Payload: payload,
		})
	}
	add(1, protocol.KindHit, "s-1", protocol.HitData{Ctl: "ctl-01", Cseq: 1, Zone: "head", Pts: 100})
	add(2, protocol.KindMiss, "s-1", protocol.ShotData{Ctl: "ctl-02", Cseq: 1})
	add(3, protocol.KindHit, "", protocol.HitData{Ctl: "ctl-02", Cseq: 2, Zone: "torso", Pts: 50})
	add(4, protocol.KindHit, "", protocol.HitData{Ctl: "ctl-02", Cseq: 3, Zone: "torso", Pts: 50})
	add(5, protocol.KindHealth, "", protocol.HealthData{Up: 5})
	if _, err := h.st.AppendEvents(ctx, "tgt-01", events); err != nil {
		t.Fatal(err)
	}
}

func TestRankingsRoute(t *testing.T) {
	h := newAPI(t)
	seedEvents(t, h)

	all := decode[Ranking](t, h.call("GET", Prefix+"/rankings", ""), 200)
	if all.Session != "" || all.ComputedAt == 0 || len(all.Entries) != 2 {
		t.Fatalf("ranking of everything is %+v", all)
	}
	if all.Entries[0].ControllerID != "ctl-02" || all.Entries[0].Points != 100 || all.Entries[0].Hits != 2 || all.Entries[0].Misses != 1 {
		t.Errorf("first place is %+v", all.Entries[0])
	}
	if all.Entries[1].ControllerID != "ctl-01" || all.Entries[1].Points != 100 || all.Entries[1].Hits != 1 {
		t.Errorf("second place is %+v", all.Entries[1])
	}

	one := decode[Ranking](t, h.call("GET", Prefix+"/rankings?session=s-1", ""), 200)
	if one.Session != "s-1" || len(one.Entries) != 2 || one.Entries[0].ControllerID != "ctl-01" {
		t.Errorf("ranking of s-1 is %+v", one)
	}
	expectError(t, h.call("GET", Prefix+"/rankings?session=nothing", ""), 404, codeNotFound)
}

func TestEventsRoute(t *testing.T) {
	h := newAPI(t)
	seedEvents(t, h)

	type page struct {
		Events []Event
		Limit  int
		Offset int
	}
	all := decode[page](t, h.call("GET", Prefix+"/events", ""), 200)
	if len(all.Events) != 5 || all.Limit != 100 || all.Offset != 0 {
		t.Fatalf("GET /events returned %d events, limit %d, offset %d", len(all.Events), all.Limit, all.Offset)
	}
	first := all.Events[0]
	if first.DeviceID != "tgt-01" || first.SeqEpoch != 1 || first.Seq != 1 || first.Kind != "hit" ||
		first.ControllerID != "ctl-01" || first.SessionID != "s-1" || len(first.ID) != 36 {
		t.Errorf("the first event is %+v", first)
	}
	if pts, ok := decodePoints(first.Payload); !ok || pts != 100 {
		t.Errorf("the payload came back as %x", first.Payload)
	}

	tests := []struct {
		query string
		want  int
	}{
		{"?device=tgt-01", 5},
		{"?device=nobody", 0},
		{"?session=s-1", 2},
		{"?kind=hit", 3},
		{"?controller=ctl-02", 3},
		{"?kind=hit&controller=ctl-02", 2},
		{"?limit=2", 2},
		{"?limit=2&offset=4", 1},
		{"?from=1&to=9999999999999", 5},
		{"?from=9999999999999", 0},
	}
	for _, tt := range tests {
		got := decode[page](t, h.call("GET", Prefix+"/events"+tt.query, ""), 200)
		if len(got.Events) != tt.want {
			t.Errorf("GET /events%s returned %d events, want %d", tt.query, len(got.Events), tt.want)
		}
	}
	for _, bad := range []string{"?limit=x", "?limit=-1", "?limit=1001", "?offset=1.5", "?from=yesterday", "?to=-3"} {
		expectError(t, h.call("GET", Prefix+"/events"+bad, ""), 400, codeBadRequest)
	}
}

func decodePoints(payload []byte) (int64, bool) {
	var hit protocol.HitData
	if err := protocol.DecodeData(payload, &hit); err != nil {
		return 0, false
	}
	return hit.Pts, true
}

func TestOnlineRoute(t *testing.T) {
	h := newAPI(t)
	empty := decode[struct{ Devices []Online }](t, h.call("GET", Prefix+"/online", ""), 200)
	if empty.Devices == nil || len(empty.Devices) != 0 {
		t.Errorf("nobody online reads %+v", empty.Devices)
	}
	h.link.online = []link.DeviceStatus{
		{DeviceID: "tgt-01", RemoteAddr: "10.0.0.5:4000", ConnectedAt: time.UnixMilli(1000), LastEventAt: time.UnixMilli(2000), LastAck: 3},
		{DeviceID: "tgt-02", ConnectedAt: time.UnixMilli(1500)},
	}
	got := decode[struct{ Devices []Online }](t, h.call("GET", Prefix+"/online", ""), 200)
	want := []Online{
		{DeviceID: "tgt-01", RemoteAddr: "10.0.0.5:4000", ConnectedAt: 1000, LastEventAt: 2000, LastAck: 3},
		{DeviceID: "tgt-02", ConnectedAt: 1500},
	}
	if len(got.Devices) != 2 || got.Devices[0] != want[0] || got.Devices[1] != want[1] {
		t.Errorf("online is %+v, want %+v", got.Devices, want)
	}
}

func TestSettingsRoute(t *testing.T) {
	h := newAPI(t)
	got := decode[struct{ Settings []config.Setting }](t, h.call("GET", Prefix+"/settings", ""), 200)
	if len(got.Settings) != len(config.Describe(config.Default(), config.Sources{})) {
		t.Fatalf("GET /settings listed %d settings", len(got.Settings))
	}
	first := got.Settings[0]
	if first.Key != "server.listen_addr" || first.Value != ":8443" || first.Source != config.SourceDefault || first.Flag != "--listen" {
		t.Errorf("the first setting is %+v", first)
	}

	bare := New(Options{Store: h.st, Logger: quiet()})
	req := httptest.NewRequest("GET", Prefix+"/settings", nil)
	req.Header.Set("Authorization", "Bearer "+h.token)
	rec := httptest.NewRecorder()
	bare.ServeHTTP(rec, req)
	expectError(t, rec, http.StatusInternalServerError, codeInternal)
}

func TestUnknownAndWrongMethod(t *testing.T) {
	h := newAPI(t)
	expectError(t, h.call("GET", Prefix+"/nothing", ""), http.StatusNotFound, codeNotFound)
	expectError(t, h.call("GET", Prefix+"/devices/a/b/c", ""), http.StatusNotFound, codeNotFound)

	rec := h.call("DELETE", Prefix+"/devices", "")
	expectError(t, rec, http.StatusMethodNotAllowed, codeMethodNotAllowed)
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow is %q", allow)
	}
	rec = h.call("GET", Prefix+"/devices/tgt-01/reset", "")
	expectError(t, rec, http.StatusMethodNotAllowed, codeMethodNotAllowed)
	if allow := rec.Header().Get("Allow"); allow != "POST" {
		t.Errorf("Allow is %q", allow)
	}
	if allow := h.call("PUT", Prefix+"/sessions", "").Header().Get("Allow"); allow != "GET, POST, HEAD" {
		t.Errorf("Allow of /sessions is %q", allow)
	}
}

func TestOpenAPIRoute(t *testing.T) {
	h := newAPI(t)
	rec := h.callAs("GET", Prefix+"/openapi.yaml", "", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/yaml") {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !bytes.Equal(rec.Body.Bytes(), OpenAPI) || !bytes.HasPrefix(OpenAPI, []byte("openapi: 3.1")) {
		t.Error("the served document is not the embedded one")
	}
}

// TestRankingEndToEnd is the path of the pass in small: a simulated device
// connects over the real link, sends hits, and GET /rankings shows them.
func TestRankingEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs for a few seconds")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, adminToken, err := st.AddAdminToken(ctx, "tests")
	if err != nil {
		t.Fatal(err)
	}
	deviceToken, err := store.NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDevice(ctx, store.Device{
		ID: "tgt-01", Kind: store.KindTarget, Status: store.StatusApproved, TokenHash: store.HashToken(deviceToken),
	}); err != nil {
		t.Fatal(err)
	}

	cfg := link.DefaultConfig()
	cfg.AckInterval = 20 * time.Millisecond
	deviceLink := link.New(st, cfg, quiet())
	router := New(Options{Store: st, Link: deviceLink, Logger: quiet()})
	srv := httptest.NewTLSServer(router)
	defer srv.Close()
	defer deviceLink.Close(ctx)

	stats, err := simtarget.Run(ctx, simtarget.Options{
		Server:      "wss" + strings.TrimPrefix(srv.URL, "https"),
		DeviceID:    "tgt-01",
		Token:       deviceToken,
		Insecure:    true,
		Rate:        60,
		Controllers: 3,
		Duration:    1500 * time.Millisecond,
		Health:      time.Hour,
		Seed:        4,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, mustJournal(t))
	if err != nil || stats.Unacked != 0 {
		t.Fatalf("simtarget: %+v, %v", stats, err)
	}

	req, _ := http.NewRequest("GET", srv.URL+Prefix+"/rankings", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var ranking Ranking
	if err := json.NewDecoder(resp.Body).Decode(&ranking); err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /rankings: %d, %v", resp.StatusCode, err)
	}
	if len(ranking.Entries) != 3 {
		t.Fatalf("the ranking has %d controllers, want 3: %+v", len(ranking.Entries), ranking)
	}

	var hits int
	err = st.EachEvent(ctx, store.Filter{Kind: protocol.KindHit}, func(store.Event) error {
		hits++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var ranked int
	for _, e := range ranking.Entries {
		ranked += e.Hits
		if e.Hits > 0 && e.Points == 0 {
			t.Errorf("%s hit %d times for no points", e.ControllerID, e.Hits)
		}
	}
	if hits == 0 || ranked != hits {
		t.Errorf("the ranking counts %d hits, the journal holds %d", ranked, hits)
	}
}

func mustJournal(t *testing.T) *simtarget.Journal {
	t.Helper()
	journal, err := simtarget.OpenJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return journal
}
