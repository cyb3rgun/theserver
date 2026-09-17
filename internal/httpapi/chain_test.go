package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/content"
	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/simtarget"
	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/scenario/scenariotest"
)

// The whole chain over the network: upload, validate, publish, assign,
// announce, download, verify, report installed, with the real link and
// simulated targets. The second target connects later and hears of the
// scenario after its first health report.
func TestScenarioChainEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs for a few seconds")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, adminToken, err := st.AddAdminToken(ctx, "founder")
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, id := range []string{"tgt-01", "tgt-02"} {
		token, err := store.NewDeviceToken()
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = token
		device := store.Device{ID: id, Kind: store.KindTarget, Status: store.StatusApproved, TokenHash: store.HashToken(token)}
		if err := st.UpsertDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
	}
	packages, err := content.New(filepath.Join(t.TempDir(), "content"), st)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	cfg := link.DefaultConfig()
	cfg.AckInterval = 20 * time.Millisecond
	deviceLink := link.New(st, cfg, logger)
	router := New(Options{Store: st, Link: deviceLink, Content: packages, Logger: logger})
	srv := httptest.NewTLSServer(router)
	defer srv.Close()
	defer deviceLink.Close(ctx)
	defer func() {
		if t.Failed() {
			t.Log("server log:\n" + logs.String())
		}
	}()

	api := func(method, path string, body io.Reader, contentType string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+Prefix+path, body)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	expect := func(resp *http.Response, status int, into any) {
		t.Helper()
		if resp.StatusCode != status {
			data, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s %s answered %d, want %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, status, data)
		}
		if into != nil {
			if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
				t.Fatal(err)
			}
		}
	}
	jsonBody := func(text string) io.Reader { return strings.NewReader(text) }

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile(UploadField, "zombie-alley.zip")
	part.Write(scenariotest.Zip(t, scenariotest.Interactive))
	form.Close()
	var up Upload
	expect(api("POST", "/scenarios", &body, form.FormDataContentType()), http.StatusCreated, &up)
	expect(api("POST", "/scenarios/zombie-alley/1/publish", nil, ""), http.StatusOK, nil)
	expect(api("POST", "/sessions", jsonBody(`{"id": "evening"}`), "application/json"), http.StatusCreated, nil)
	for _, id := range []string{"tgt-01", "tgt-02"} {
		expect(api("POST", "/sessions/evening/devices", jsonBody(`{"device_id": "`+id+`"}`), "application/json"), http.StatusOK, nil)
	}

	runTarget := func(id string) (stop func() simtarget.Stats) {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan simtarget.Stats, 1)
		contentDir := filepath.Join(t.TempDir(), "content")
		journal := mustJournal(t)
		go func() {
			stats, err := simtarget.Run(runCtx, simtarget.Options{
				Server: "wss" + strings.TrimPrefix(srv.URL, "https"), DeviceID: id, Token: tokens[id], Insecure: true,
				Rate: 2, Health: time.Hour, Reconnect: 100 * time.Millisecond, ContentDir: contentDir,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}, journal)
			if err != nil {
				t.Errorf("%s: %v", id, err)
			}
			done <- stats
		}()
		var once *simtarget.Stats
		return func() simtarget.Stats {
			if once == nil {
				cancel()
				stats := <-done
				once = &stats
			}
			return *once
		}
	}
	held := func(id string) bool {
		ok, err := st.HoldsScenario(ctx, id, "zombie-alley", 1)
		return err == nil && ok
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("%s did not happen in time", what)
	}

	// tgt-01 is connected when the scenario is assigned.
	stopFirst := runTarget("tgt-01")
	defer stopFirst()
	waitFor("tgt-01 online with its holdings", func() bool {
		return strings.Contains(logs.String(), `msg="device holdings" component=link device=tgt-01`)
	})
	var assigned SessionAssignment
	expect(api("POST", "/sessions/evening/scenario", jsonBody(`{"id": "zombie-alley", "version": 1}`), "application/json"), http.StatusOK, &assigned)
	states := map[string]string{}
	for _, a := range assigned.Announcements {
		states[a.DeviceID] = a.State
	}
	if states["tgt-01"] != AnnouncedTaken || states["tgt-02"] != AnnouncedWaiting || len(states) != 2 {
		t.Fatalf("the announcements are %+v", assigned.Announcements)
	}
	waitFor("tgt-01 installed", func() bool { return held("tgt-01") })

	// tgt-02 connects later and is told after its first health report.
	stopSecond := runTarget("tgt-02")
	defer stopSecond()
	waitFor("tgt-02 installed", func() bool { return held("tgt-02") })

	var devices DeviceScenarios
	expect(api("GET", "/devices/tgt-02/scenarios", nil, ""), http.StatusOK, &devices)
	if len(devices.Assignments) != 1 || !devices.Assignments[0].Installed || len(devices.Holdings) != 1 || devices.Holdings[0].Latest != 1 {
		t.Errorf("tgt-02 holds %+v", devices)
	}
	first, second := stopFirst(), stopSecond()
	if first.Installs != 1 || second.Installs != 1 || first.InstallsFailed+second.InstallsFailed != 0 {
		t.Errorf("the targets report %+v and %+v", first, second)
	}

	size := strconv.FormatInt(up.Scenario.Size, 10)
	uploaded := `msg="scenario uploaded" component=api scenario=zombie-alley version=1 problems=0 size=` + size
	published := `msg="scenario published" component=api scenario=zombie-alley version=1 manifest_hash=` + up.Scenario.ManifestHash
	assignedLine := `msg="scenario assigned" component=api session=evening scenario=zombie-alley version=1`
	announced1 := `msg="content announced" component=link device=tgt-01 scenario=zombie-alley version=1 sha256=` + up.Scenario.ManifestHash + ` size=` + size
	waiting2 := `msg="content announcement waits for the device" component=link device=tgt-02 session=evening scenario=zombie-alley version=1`
	download1 := `msg="package download" component=api scenario=zombie-alley version=1 size=` + size + ` device=tgt-01 by=device`
	installing := `scenario=zombie-alley version=1 state=installing`
	installed := `scenario=zombie-alley version=1 state=installed`
	announced2 := `msg="content announced" component=link device=tgt-02 scenario=zombie-alley version=1`
	download2 := `msg="package download" component=api scenario=zombie-alley version=1 size=` + size + ` device=tgt-02 by=device`

	// The steps whose order is fixed; the announcement and the download of
	// one device run side by side.
	text := logs.String()
	for _, pair := range [][2]string{
		{uploaded, published}, {published, assignedLine}, {assignedLine, announced1}, {assignedLine, waiting2},
		{assignedLine, download1}, {assignedLine, installing}, {download1, installed},
		{installed, announced2}, {installed, download2},
	} {
		first, then := strings.Index(text, pair[0]), strings.Index(text, pair[1])
		switch {
		case first < 0:
			t.Errorf("the log lacks %s", pair[0])
		case then < 0:
			t.Errorf("the log lacks %s", pair[1])
		case then < first:
			t.Errorf("%s comes before %s", pair[1], pair[0])
		}
	}
}
