package config

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cyb3rgun/theserver/internal/settings"
)

// startRuntime writes content as the configuration file, loads it as serve
// does, and returns the Runtime.
func startRuntime(t *testing.T, content string, env map[string]string, flags map[string]string) (*Runtime, string) {
	t.Helper()
	path := writeFile(t, t.TempDir(), "theserver.toml", content)
	cfg, sources, err := LoadWithSources(path, envFrom(env), flags)
	if err != nil {
		t.Fatalf("LoadWithSources: %v", err)
	}
	r, err := NewRuntime(cfg, sources, discard())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return r, path
}

func discard() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func reload(t *testing.T, path string) (Config, Sources) {
	t.Helper()
	cfg, sources, err := LoadWithSources(path, nil, nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return cfg, sources
}

func TestLiveChangeTakesEffect(t *testing.T) {
	r, path := startRuntime(t, "[log]\nlevel = \"info\"\n", nil, nil)
	var mu sync.Mutex
	var seen []Config
	r.OnChange(func(c Config) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, c)
	})

	needsRestart, applied, err := r.Apply(map[string]any{"log.level": "debug", "link.ack_batch": "64", "admin.language": "de"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(needsRestart) != 0 || strings.Join(applied, ",") != "link.ack_batch,log.level,admin.language" {
		t.Errorf("Apply returned restart %v, applied %v", needsRestart, applied)
	}
	got := r.Config()
	if got.Log.Level != "debug" || got.Link.AckBatch != 64 || got.Admin.Language != "de" {
		t.Errorf("the values in effect are %+v", got)
	}
	mu.Lock()
	if len(seen) != 1 || seen[0] != got {
		t.Errorf("the hook saw %+v, want one call with %+v", seen, got)
	}
	mu.Unlock()
	if src := r.Sources().Of("link.ack_batch"); src != SourceFile {
		t.Errorf("link.ack_batch comes from %s after the change", src)
	}
	if len(r.RestartPending()) != 0 {
		t.Errorf("restart pending: %v", r.RestartPending())
	}

	fromFile, _ := reload(t, path)
	if fromFile != got {
		t.Errorf("the file holds %+v, the server runs %+v", fromFile, got)
	}
}

func TestRestartChangeIsReportedNotApplied(t *testing.T) {
	r, path := startRuntime(t, "[server]\nlisten_addr = \":8443\"\n", nil, nil)
	hooked := false
	r.OnChange(func(Config) { hooked = true })

	changes, err := r.Change(map[string]any{"server.listen_addr": "127.0.0.1:9000", "log.format": "json"})
	if err != nil {
		t.Fatalf("Change: %v", err)
	}
	want := []Change{
		{Key: "server.listen_addr", Old: ":8443", New: "127.0.0.1:9000", Restart: true},
		{Key: "log.format", Old: "text", New: "json", Restart: true},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes are %+v, want %+v", changes, want)
	}
	if hooked {
		t.Error("a restart change ran the live hooks")
	}
	if got := r.Config(); got.Server.ListenAddr != ":8443" || got.Log.Format != "text" {
		t.Errorf("a restart change was applied: %+v", got)
	}
	if got := strings.Join(r.RestartPending(), ","); got != "server.listen_addr,log.format" {
		t.Errorf("restart pending is %q", got)
	}
	if value, ok := r.Pending("server.listen_addr"); !ok || value != "127.0.0.1:9000" {
		t.Errorf("Pending = %v, %v", value, ok)
	}
	fromFile, _ := reload(t, path)
	if fromFile.Server.ListenAddr != "127.0.0.1:9000" || fromFile.Log.Format != "json" {
		t.Errorf("the next start would read %+v", fromFile)
	}

	// Setting it back to the value in effect clears the restart.
	needsRestart, applied, err := r.Apply(map[string]any{"server.listen_addr": ":8443"})
	if err != nil {
		t.Fatal(err)
	}
	if len(needsRestart) != 0 || len(applied) != 0 {
		t.Errorf("going back gave restart %v, applied %v", needsRestart, applied)
	}
	if got := strings.Join(r.RestartPending(), ","); got != "log.format" {
		t.Errorf("restart pending after going back is %q", got)
	}

	needsRestart, _, err = r.Apply(map[string]any{"store.busy_timeout_ms": 100})
	if err != nil || strings.Join(needsRestart, ",") != "store.busy_timeout_ms" {
		t.Errorf("Apply = %v, %v", needsRestart, err)
	}
}

func TestInvalidChangesAreRefusedAllOrNothing(t *testing.T) {
	r, path := startRuntime(t, "[log]\nlevel = \"info\"\n", nil, nil)
	before, _ := os.ReadFile(path)

	_, _, err := r.Apply(map[string]any{
		"log.level":            "loud",
		"link.ack_batch":       0,
		"server.listen_addr":   "nowhere",
		"link.ack_interval_ms": "soon",
		"link.hello_timeout_s": 7,
		"no.such":              1,
	})
	if err == nil {
		t.Fatal("Apply took invalid values")
	}
	codes := map[string]string{}
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("the error %v does not list the refused values", err)
	}
	for _, e := range joined.Unwrap() {
		var ve *settings.ValueError
		if !errors.As(e, &ve) {
			t.Errorf("%v is not a typed value error", e)
			continue
		}
		codes[ve.Key] = ve.Code()
	}
	want := map[string]string{
		"log.level":            "not_allowed",
		"link.ack_batch":       "out_of_range",
		"server.listen_addr":   "bad_address",
		"link.ack_interval_ms": "bad_duration",
		"no.such":              "unknown_setting",
	}
	if !reflect.DeepEqual(codes, want) {
		t.Errorf("refused with %v, want %v", codes, want)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("a refused change wrote the file")
	}
	if got := r.Config(); got != Default() {
		t.Errorf("a refused change was applied: %+v", got)
	}
}

func TestOverriddenSettingsCannotBeChanged(t *testing.T) {
	r, _ := startRuntime(t, "", map[string]string{"THESERVER_LOG_LEVEL": "warn"}, map[string]string{"listen": "127.0.0.1:9000"})
	_, _, err := r.Apply(map[string]any{"log.level": "debug", "server.listen_addr": ":9443"})
	var oe *OverrideError
	if !errors.Is(err, ErrOverridden) || !errors.As(err, &oe) {
		t.Fatalf("Apply = %v, want an override error", err)
	}
	for _, want := range []string{"log.level is set by the environment variable THESERVER_LOG_LEVEL", "server.listen_addr is set by the flag --listen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if _, _, err := r.Reset([]string{"log.level"}); !errors.Is(err, ErrOverridden) {
		t.Errorf("Reset of an overridden setting = %v", err)
	}
	if _, _, err := r.Apply(map[string]any{"log.format": "json"}); err != nil {
		t.Errorf("a setting from the file could not be changed: %v", err)
	}
}

// Without --config the first change creates theserver.toml in the data
// directory, logs where, and the next start without --config reads it.
func TestFirstChangeCreatesTheFileInTheDataDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	flags := map[string]string{"data-dir": dir}
	cfg, sources, err := LoadWithSources("", nil, flags)
	if err != nil {
		t.Fatal(err)
	}
	if sources.File != "" {
		t.Fatalf("a file is in use before there is one: %q", sources.File)
	}
	var logs bytes.Buffer
	r, err := NewRuntime(cfg, sources, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, FileName)
	if r.File() != want || r.FileExists() {
		t.Errorf("before the first change the file is %q, exists %v", r.File(), r.FileExists())
	}
	if _, err := os.Stat(want); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the file is there before the first change: %v", err)
	}

	if _, _, err := r.Apply(map[string]any{"log.level": "debug", "link.ack_batch": 16}); err != nil {
		t.Fatalf("the first change: %v", err)
	}
	if !r.FileExists() || r.Sources().File != want {
		t.Errorf("after the first change the file in use is %q", r.Sources().File)
	}
	if r.Config().Log.Level != "debug" || r.Sources().Of("link.ack_batch") != SourceFile {
		t.Errorf("the change did not take effect: %+v", r.Config())
	}
	if n := strings.Count(logs.String(), `msg="configuration file created"`); n != 1 || !strings.Contains(logs.String(), FileName) {
		t.Errorf("the creation is logged %d times:\n%s", n, logs.String())
	}

	next, nextSources, err := LoadWithSources("", nil, flags)
	if err != nil {
		t.Fatal(err)
	}
	if nextSources.File != want || next.Log.Level != "debug" || next.Link.AckBatch != 16 || nextSources.Of("log.level") != SourceFile {
		t.Errorf("the next start reads %q: %+v", nextSources.File, next)
	}
	byEnv, envSources, err := LoadWithSources("", envFrom(map[string]string{"THESERVER_SERVER_DATADIR": dir}), nil)
	if err != nil || envSources.File != want || byEnv.Link.AckBatch != 16 {
		t.Errorf("a data directory from the environment finds %q, %v", envSources.File, err)
	}
	other := writeFile(t, t.TempDir(), "other.toml", "")
	_, named, err := LoadWithSources(other, nil, flags)
	if err != nil || named.File != other {
		t.Errorf("--config is not the file in use: %q, %v", named.File, err)
	}

	if _, _, err := r.Apply(map[string]any{"log.level": "warn"}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(logs.String(), `msg="configuration file created"`); n != 1 {
		t.Errorf("a later change logs the creation again, %d lines", n)
	}
	if cfg, _ := reload(t, want); cfg.Log.Level != "warn" || cfg.Link.AckBatch != 16 {
		t.Errorf("the file holds %+v", cfg)
	}
}

// A file that appears where the first change would create it holds settings
// the server never read, so the change is refused and the file kept.
func TestFileThatAppearedIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	cfg, sources, err := LoadWithSources("", nil, map[string]string{"data-dir": dir})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(cfg, sources, discard())
	if err != nil {
		t.Fatal(err)
	}
	content := `[link]
ack_batch = 8
`
	path := writeFile(t, dir, FileName, content)
	_, err = r.Change(map[string]any{"log.level": "debug"})
	if !errors.Is(err, ErrFileAppeared) || !strings.Contains(err.Error(), path) {
		t.Fatalf("the change gave %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != content {
		t.Errorf("the file changed:\n%s", data)
	}
	if r.FileExists() || r.Config().Log.Level != "info" {
		t.Error("the refused change left traces")
	}

	if _, err := NewRuntime(Config{}, Sources{}, nil); err == nil {
		t.Error("a runtime without a file and without a data directory was accepted")
	}
}

// A data directory that the registry refuses finds no file and fails as an
// override, and a directory in the place of the file is an error.
func TestDataDirectoryFileErrors(t *testing.T) {
	_, _, err := LoadWithSources("", envFrom(map[string]string{"THESERVER_SERVER_DATADIR": ""}), nil)
	if err == nil || !strings.Contains(err.Error(), "THESERVER_SERVER_DATADIR") {
		t.Errorf("an empty data directory gave %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadWithSources("", nil, map[string]string{"data-dir": dir}); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("a directory named %s gave %v", FileName, err)
	}
}

func TestCertificateNeedsItsKey(t *testing.T) {
	r, _ := startRuntime(t, "", nil, nil)
	if _, _, err := r.Apply(map[string]any{"tls.cert_file": "a.crt"}); !errors.Is(err, ErrCertKeyPair) {
		t.Errorf("a certificate alone gave %v", err)
	}
	needsRestart, _, err := r.Apply(map[string]any{"tls.cert_file": "a.crt", "tls.key_file": "a.key"})
	if err != nil || len(needsRestart) != 2 {
		t.Fatalf("both together gave %v, %v", needsRestart, err)
	}
	// With both pending, dropping one alone breaks the next start.
	if _, _, err := r.Reset([]string{"tls.key_file"}); !errors.Is(err, ErrCertKeyPair) {
		t.Errorf("dropping the key alone gave %v", err)
	}
}

func TestResetGoesBackToTheDefault(t *testing.T) {
	r, path := startRuntime(t, "[link]\nack_batch = 8\n\n[log]\nformat = \"json\"\n", nil, nil)
	changes, err := r.ResetKeys([]string{"link.ack_batch", "log.format", "log.level"})
	if err != nil {
		t.Fatalf("ResetKeys: %v", err)
	}
	want := []Change{
		{Key: "link.ack_batch", Old: int64(8), New: int64(32)},
		{Key: "log.format", Old: "json", New: "text", Restart: true},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes are %+v, want %+v", changes, want)
	}
	if r.Config().Link.AckBatch != 32 || r.Sources().Of("link.ack_batch") != SourceDefault {
		t.Errorf("ack batch after reset: %d from %s", r.Config().Link.AckBatch, r.Sources().Of("link.ack_batch"))
	}
	data, _ := os.ReadFile(path)
	for _, line := range []string{"# ack_batch = 32", `# format = "text"`, `# level = "info"`} {
		if !strings.Contains(string(data), line) {
			t.Errorf("the file lacks %q:\n%s", line, data)
		}
	}
	_, sources := reload(t, path)
	if sources.Of("link.ack_batch") != SourceDefault || sources.Of("log.format") != SourceDefault {
		t.Error("the reset settings are still in the file")
	}
	if strings.Join(r.RestartPending(), ",") != "log.format" {
		t.Errorf("restart pending is %v", r.RestartPending())
	}
}

func TestSameValueIsNoChangeButLandsInTheFile(t *testing.T) {
	r, path := startRuntime(t, "", nil, nil)
	changes, err := r.Change(map[string]any{"link.ack_batch": 32})
	if err != nil || len(changes) != 0 {
		t.Fatalf("setting the default gave %+v, %v", changes, err)
	}
	if r.Sources().Of("link.ack_batch") != SourceFile {
		t.Error("the value written to the file is not reported as coming from it")
	}
	cfg, sources := reload(t, path)
	if cfg.Link.AckBatch != 32 || sources.Of("link.ack_batch") != SourceFile {
		t.Errorf("the file sets %d from %s", cfg.Link.AckBatch, sources.Of("link.ack_batch"))
	}
}

func TestNewRuntimeReadsTheFileLayer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	if _, err := NewRuntime(Default(), Sources{File: path}, discard()); err == nil {
		t.Error("NewRuntime accepted a missing file")
	}
	r, path := startRuntime(t, "[link]\nack_batch = 8\n", nil, nil)
	if _, _, err := r.Apply(map[string]any{"log.level": "warn"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := reload(t, path)
	if cfg.Link.AckBatch != 8 {
		t.Error("a change dropped a value the file already set")
	}
}
