package simtarget

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cyb3rgun/theserver/internal/link"
	"github.com/cyb3rgun/theserver/internal/protocol"
	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/scenario/scenariotest"
	"github.com/cyb3rgun/theserver/internal/store"
)

// packageServer stands in for the download of the API in this package's
// tests: it serves packages to devices by their token, with Range and the
// manifest hash, and can cut off the first response.
type packageServer struct {
	t     *testing.T
	store *store.Store

	mu       sync.Mutex
	packages map[string][]byte // by id/version
	header   map[string]bool   // whether to send X-Manifest-SHA256
	cut      map[string]bool   // cut off the next response after half the bytes
	whole    map[string]bool   // ignore Range and send the whole package
	requests []string          // id/version and Range of every request
}

func (p *packageServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if _, err := p.store.DeviceByToken(r.Context(), token); err != nil {
		http.Error(w, "unknown token", http.StatusUnauthorized)
		return
	}
	key := r.PathValue("id") + "/" + r.PathValue("version")
	p.mu.Lock()
	data, ok := p.packages[key]
	withHeader, cut, whole := p.header[key], p.cut[key], p.whole[key]
	p.cut[key] = false
	p.requests = append(p.requests, key+" "+r.Header.Get("Range"))
	p.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if withHeader {
		pkg, err := scenario.ReadZip(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			p.t.Error(err)
			return
		}
		w.Header().Set("X-Manifest-SHA256", scenario.Hash(pkg.Manifest))
	}
	if cut {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		w.Write(data[:len(data)/2])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	if whole {
		r.Header.Del("Range")
	}
	http.ServeContent(w, r, "package.zip", time.Time{}, bytes.NewReader(data))
}

func (p *packageServer) add(key string, data []byte, header, cut bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.packages[key], p.header[key], p.cut[key] = data, header, cut
}

func (p *packageServer) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

type contentHarness struct {
	t        *testing.T
	store    *store.Store
	link     *link.Server
	packages *packageServer
	server   string
	token    string
	logs     *lockedBuffer
}

func newContentHarness(t *testing.T) *contentHarness {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDevice(context.Background(), store.Device{
		ID: "tgt-01", Kind: store.KindTarget, Status: store.StatusApproved, TokenHash: store.HashToken(token),
	}); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	cfg := link.DefaultConfig()
	cfg.AckInterval = 20 * time.Millisecond
	deviceLink := link.New(st, cfg, slog.New(slog.NewTextHandler(logs, nil)))
	packages := &packageServer{t: t, store: st, packages: map[string][]byte{}, header: map[string]bool{}, cut: map[string]bool{}, whole: map[string]bool{}}
	mux := http.NewServeMux()
	mux.Handle(link.Path, deviceLink)
	mux.Handle("GET /api/v1/scenarios/{id}/{version}/package.zip", packages)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		deviceLink.Close(ctx)
		srv.Close()
		st.Close()
		if t.Failed() {
			t.Log("server log:\n" + logs.String())
		}
	})
	return &contentHarness{
		t: t, store: st, link: deviceLink, packages: packages, token: token, logs: logs,
		server: "wss" + strings.TrimPrefix(srv.URL, "https"),
	}
}

// run starts the simulated target in the background with the journal in
// journalDir; stop ends it and returns what it did.
func (h *contentHarness) run(journalDir string, opts Options) (stop func() Stats) {
	h.t.Helper()
	opts.Server, opts.DeviceID, opts.Token, opts.Insecure = h.server, "tgt-01", h.token, true
	opts.Rate, opts.Health, opts.Reconnect = 2, time.Hour, 100*time.Millisecond
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	journal, err := OpenJournal(journalDir)
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Stats, 1)
	go func() {
		stats, err := Run(ctx, opts, journal)
		if err != nil {
			h.t.Errorf("Run: %v", err)
		}
		done <- stats
	}()
	return func() Stats {
		cancel()
		select {
		case stats := <-done:
			return stats
		case <-time.After(10 * time.Second):
			h.t.Fatal("the simulated target did not stop")
			return Stats{}
		}
	}
}

// held lists the versions the server records as held by tgt-01.
func (h *contentHarness) held() string {
	h.t.Helper()
	list, err := h.store.DeviceScenarios(context.Background(), "tgt-01")
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, holding := range list {
		if holding.Current {
			out = append(out, holding.ScenarioID+"@"+strconv.Itoa(holding.Version))
		}
	}
	return strings.Join(out, ",")
}

func (h *contentHarness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("%s did not happen in time", what)
}

// announce sends content_available once the target is online.
func (h *contentHarness) announce(content protocol.ContentAvailable) error {
	h.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		err := h.link.Announce(context.Background(), "tgt-01", content)
		if !errors.Is(err, link.ErrDeviceOffline) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func announcement(t *testing.T, data []byte) protocol.ContentAvailable {
	t.Helper()
	pkg, err := scenario.ReadZip(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return protocol.ContentAvailable{
		ID: pkg.Manifest.Scenario.ID, Ver: uint64(pkg.Manifest.Scenario.Version),
		Sha: scenario.Hash(pkg.Manifest), Size: uint64(len(data)),
	}
}

// withVersion is the VIDEO fixture zipped with its manifest at version.
func withVersion(t *testing.T, version string) []byte {
	t.Helper()
	files := scenariotest.Files(t, scenariotest.Dir(scenariotest.Video))
	manifest := string(files[scenario.ManifestName])
	files[scenario.ManifestName] = []byte(strings.Replace(manifest, "version     = 1\n", "version     = "+version+"\n", 1))
	data, err := scenariotest.ZipFiles(files, "")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The simulated target reports what it holds, takes an announcement,
// downloads the package with its token, resumes a cut download with Range,
// checks the hash and reports it installed; a package whose manifest hash
// differs from the announcement is reported failed (protocol section 8.10).
func TestSimulatorInstallsAnnouncedContent(t *testing.T) {
	h := newContentHarness(t)
	contentDir := filepath.Join(t.TempDir(), "content")
	journalDir := t.TempDir()
	var simLog lockedBuffer
	stop := h.run(journalDir, Options{
		Holdings:   []protocol.Holding{{ID: "zombie-alley", Ver: 1}},
		ContentDir: contentDir,
		Logger:     slog.New(slog.NewTextHandler(&simLog, nil)),
	})
	h.waitFor("the configured holdings", func() bool { return h.held() == "zombie-alley@1" })

	video := scenariotest.Zip(t, scenariotest.Video)
	h.packages.add("night-range/1", video, true, true)
	content := announcement(t, video)
	if err := h.announce(content); err != nil {
		t.Fatalf("announce: %v", err)
	}
	h.waitFor("the installed package", func() bool { return h.held() == "night-range@1,zombie-alley@1" })
	kept, err := os.ReadFile(filepath.Join(contentDir, "night-range", "1", "package.zip"))
	if err != nil || !bytes.Equal(kept, video) {
		t.Errorf("the installed package differs from the served one: %v", err)
	}
	half := strconv.Itoa(len(video) / 2)
	if got := strings.Join(h.packages.seen(), "; "); got != "night-range/1 ; night-range/1 bytes="+half+"-" {
		t.Errorf("the downloads were %s", got)
	}

	// Announced again, the version is held: no download.
	if err := h.announce(content); err != nil {
		t.Fatal(err)
	}

	// A server that ignores Range after a cut download sends it all again.
	fourth := withVersion(t, "4")
	h.packages.add("night-range/4", fourth, true, true)
	h.packages.mu.Lock()
	h.packages.whole["night-range/4"] = true
	h.packages.mu.Unlock()
	if err := h.announce(announcement(t, fourth)); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the package sent whole", func() bool { return h.held() == "night-range@4,night-range@1,zombie-alley@1" })
	if kept, err := os.ReadFile(filepath.Join(contentDir, "night-range", "4", "package.zip")); err != nil || !bytes.Equal(kept, fourth) {
		t.Errorf("the package sent whole was kept wrong: %v", err)
	}

	// A manifest hash that differs from the announcement, found after the
	// download and from the header.
	second := withVersion(t, "2")
	h.packages.add("night-range/2", second, false, false)
	wrong := announcement(t, second)
	wrong.Sha = content.Sha
	if err := h.announce(wrong); err != nil {
		t.Fatal(err)
	}
	third := withVersion(t, "3")
	h.packages.add("night-range/3", third, true, false)
	wrongHeader := announcement(t, third)
	wrongHeader.Sha = strings.Repeat("0", 64)
	if err := h.announce(wrongHeader); err != nil {
		t.Fatal(err)
	}
	h.waitFor("both failures", func() bool {
		return strings.Count(h.logs.String(), `state=failed`) == 2
	})
	if h.held() != "night-range@4,night-range@1,zombie-alley@1" {
		t.Errorf("a failed install counts as held: %s", h.held())
	}
	stats := stop()

	logs := h.logs.String()
	for _, want := range []string{
		`msg="content announced" component=link device=tgt-01 scenario=night-range version=1 sha256=` + content.Sha,
		`msg="content reported" component=link device=tgt-01 remote=`,
		`scenario=night-range version=1 state=installing`,
		`scenario=night-range version=1 state=installed`,
		`scenario=night-range version=2 state=failed reason="the manifest hash ` + announcement(t, second).Sha + ` does not match the announced ` + content.Sha + `"`,
		`scenario=night-range version=3 state=failed reason="the server offers the manifest hash ` + announcement(t, third).Sha,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the server log lacks %s", want)
		}
	}
	if !strings.Contains(simLog.String(), "download cut off, resuming") || !strings.Contains(simLog.String(), `msg="content announced, already held"`) {
		t.Errorf("the target log lacks the resume or the held announcement:\n%s", simLog.String())
	}
	if stats.Installs != 2 || stats.InstallsFailed != 2 || FormatHoldings(stats.Held) != "night-range@1,night-range@4,zombie-alley@1" {
		t.Errorf("the target reports %+v", stats)
	}
	if entries, _ := os.ReadDir(contentDir); len(entries) != 1 {
		t.Errorf("the content directory holds %d entries, want only night-range", len(entries))
	}

	// After a restart the target reports what its content directory holds.
	stop = h.run(journalDir, Options{ContentDir: contentDir})
	h.waitFor("the holdings after the restart", func() bool { return h.held() == "night-range@4,night-range@1" })
	stop()
}

func TestSimulatorRefusesUnusableAnnouncements(t *testing.T) {
	h := newContentHarness(t)
	stop := h.run(t.TempDir(), Options{})
	defer stop()
	content := announcement(t, scenariotest.Zip(t, scenariotest.Video))
	if err := h.announce(content); !errors.Is(err, link.ErrRefused) || !strings.Contains(err.Error(), "without a content directory") {
		t.Errorf("an announcement without a content directory gave %v", err)
	}
	for _, args := range []map[string]any{
		{"id": "Night Range", "ver": 1, "sha": content.Sha, "size": 10},
		{"id": "night-range", "ver": 0, "sha": content.Sha, "size": 10},
		{"id": "night-range", "ver": 1, "sha": "abc", "size": 10},
		{"id": "night-range", "ver": "one"},
	} {
		result, err := h.link.SendCommand(context.Background(), "tgt-01", protocol.CommandContentAvailable, args)
		if err != nil || result.OK || result.E == "" {
			t.Errorf("%v was answered %+v, %v", args, result, err)
		}
	}
}

func TestParseHoldingsAndHTTPBase(t *testing.T) {
	holdings, err := ParseHoldings(" night-range@2, zombie-alley@1 ,")
	if err != nil || FormatHoldings(holdings) != "night-range@2,zombie-alley@1" {
		t.Errorf("ParseHoldings gave %v, %v", holdings, err)
	}
	for _, bad := range []string{"night-range", "night-range@0", "Night@1", "a@b", "a@4294967296"} {
		if _, err := ParseHoldings(bad); err == nil {
			t.Errorf("ParseHoldings(%q) was accepted", bad)
		}
	}
	for in, want := range map[string]string{
		"wss://127.0.0.1:8443":          "https://127.0.0.1:8443",
		"wss://example.test/link/v1/":   "https://example.test",
		"ws://127.0.0.1:9000/link/v1?x": "http://127.0.0.1:9000",
	} {
		if got, err := httpBase(in); err != nil || got != want {
			t.Errorf("httpBase(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := httpBase("https://127.0.0.1"); err == nil {
		t.Error("an https server address was accepted")
	}
}
