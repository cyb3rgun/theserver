package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/settings"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func envFrom(vars map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := vars[key]
		return value, ok
	}
}

// defaultsWith returns the defaults with change applied, so a case names only
// what it expects to differ.
func defaultsWith(change func(c *Config)) Config {
	c := Default()
	change(&c)
	return c
}

func TestDefaultsComeFromTheRegistry(t *testing.T) {
	want := Config{
		Server:  Server{ListenAddr: ":8443", DataDir: "./data"},
		Store:   Store{BusyTimeoutMs: 5000},
		Content: Content{MaxUploadMB: 2048},
		Link:    Link{AckIntervalMs: 100, AckBatch: 32, PingIntervalS: 15, PongTimeoutS: 10, HelloTimeoutS: 5, InstallTimeoutS: 120},
		Log:     Log{Level: "info", Format: "text"},
		Admin:   Admin{Language: "en", SessionHours: 12},
	}
	if got := Default(); got != want {
		t.Errorf("Default is %+v, want %+v", got, want)
	}
	if got := want.ContentDir(); got != filepath.Join("data", "content") {
		t.Errorf("the default content directory is %s", got)
	}
	want.Content.Dir = "/srv/packages"
	if got := want.ContentDir(); got != "/srv/packages" {
		t.Errorf("a set content directory reads %s", got)
	}
	if got := want.Get("link.ack_batch"); got != int64(32) {
		t.Errorf("Get(link.ack_batch) = %#v", got)
	}
	if got := want.Get("no.such"); got != nil {
		t.Errorf("Get of an unknown key = %#v", got)
	}
}

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	full := writeFile(t, dir, "full.toml", `
[server]
listen_addr = "127.0.0.1:9000"
data_dir = "file-data"

[tls]
cert_file = "file.crt"
key_file = "file.key"

[store]
busy_timeout_ms = 1234

[content]
max_upload_mb = 512
dir = "file-content"

[link]
ack_interval_ms = 250
ack_batch = 8
ping_interval_s = 30
pong_timeout_s = 20
hello_timeout_s = 3

[log]
level = "warn"
format = "json"

[admin]
language = "de"
session_hours = 8
`)
	partial := writeFile(t, dir, "partial.toml", `
[log]
level = "debug"
`)

	fileConfig := Config{
		Server:  Server{ListenAddr: "127.0.0.1:9000", DataDir: "file-data"},
		TLS:     TLS{CertFile: "file.crt", KeyFile: "file.key"},
		Store:   Store{BusyTimeoutMs: 1234},
		Content: Content{MaxUploadMB: 512, Dir: "file-content"},
		Link:    Link{AckIntervalMs: 250, AckBatch: 8, PingIntervalS: 30, PongTimeoutS: 20, HelloTimeoutS: 3, InstallTimeoutS: 120},
		Log:     Log{Level: "warn", Format: "json"},
		Admin:   Admin{Language: "de", SessionHours: 8},
	}

	tests := []struct {
		name  string
		file  string
		env   map[string]string
		flags map[string]string
		want  Config
	}{
		{
			name: "defaults only",
			want: Default(),
		},
		{
			name: "file over defaults",
			file: full,
			want: fileConfig,
		},
		{
			name: "partial file keeps the other defaults",
			file: partial,
			want: defaultsWith(func(c *Config) { c.Log.Level = "debug" }),
		},
		{
			name: "env over defaults",
			env: map[string]string{
				"THESERVER_SERVER_LISTENADDR":   "127.0.0.1:9100",
				"THESERVER_SERVER_DATADIR":      "env-data",
				"THESERVER_TLS_CERTFILE":        "env.crt",
				"THESERVER_TLS_KEYFILE":         "env.key",
				"THESERVER_STORE_BUSYTIMEOUTMS": "250",
				"THESERVER_CONTENT_MAXUPLOADMB": "100",
				"THESERVER_CONTENT_DIR":         "env-content",
				"THESERVER_LINK_ACKINTERVALMS":  "50",
				"THESERVER_LINK_ACKBATCH":       "16",
				"THESERVER_LINK_PINGINTERVALS":  "5",
				"THESERVER_LINK_PONGTIMEOUTS":   "4",
				"THESERVER_LINK_HELLOTIMEOUTS":  "2s",
				"THESERVER_LOG_LEVEL":           "error",
				"THESERVER_LOG_FORMAT":          "json",
				"THESERVER_ADMIN_LANGUAGE":      "de",
				"THESERVER_ADMIN_SESSIONHOURS":  "2h",
			},
			want: Config{
				Server:  Server{ListenAddr: "127.0.0.1:9100", DataDir: "env-data"},
				TLS:     TLS{CertFile: "env.crt", KeyFile: "env.key"},
				Store:   Store{BusyTimeoutMs: 250},
				Content: Content{MaxUploadMB: 100, Dir: "env-content"},
				Link:    Link{AckIntervalMs: 50, AckBatch: 16, PingIntervalS: 5, PongTimeoutS: 4, HelloTimeoutS: 2, InstallTimeoutS: 120},
				Log:     Log{Level: "error", Format: "json"},
				Admin:   Admin{Language: "de", SessionHours: 2},
			},
		},
		{
			name: "env over file",
			file: full,
			env: map[string]string{
				"THESERVER_SERVER_LISTENADDR": "127.0.0.1:9100",
				"THESERVER_LINK_ACKBATCH":     "64",
				"THESERVER_LOG_LEVEL":         "error",
			},
			want: func() Config {
				c := fileConfig
				c.Server.ListenAddr = "127.0.0.1:9100"
				c.Link.AckBatch = 64
				c.Log.Level = "error"
				return c
			}(),
		},
		{
			name: "flags over defaults",
			flags: map[string]string{
				"listen":    "127.0.0.1:9200",
				"data-dir":  "flag-data",
				"log-level": "debug",
			},
			want: defaultsWith(func(c *Config) {
				c.Server = Server{ListenAddr: "127.0.0.1:9200", DataDir: "flag-data"}
				c.Log.Level = "debug"
			}),
		},
		{
			name: "flags over env over file over defaults",
			file: partial,
			env: map[string]string{
				"THESERVER_SERVER_LISTENADDR": "127.0.0.1:9100",
				"THESERVER_SERVER_DATADIR":    "env-data",
				"THESERVER_LOG_LEVEL":         "error",
			},
			flags: map[string]string{
				"listen": "127.0.0.1:9200",
			},
			want: defaultsWith(func(c *Config) {
				c.Server = Server{ListenAddr: "127.0.0.1:9200", DataDir: "env-data"}
				c.Log.Level = "error"
			}),
		},
		{
			name: "flags that are not settings are ignored",
			flags: map[string]string{
				"config":  full,
				"version": "true",
				"id":      "tgt-01",
			},
			want: Default(),
		},
		{
			name: "unrelated environment variables are ignored",
			env: map[string]string{
				"THESERVER_UNKNOWN": "x",
				"LISTENADDR":        "127.0.0.1:1",
			},
			want: Default(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(tt.file, envFrom(tt.env), tt.flags)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got != tt.want {
				t.Errorf("Load:\n got  %+v\n want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	unknown := writeFile(t, dir, "unknown.toml", "[server]\nlisten = \":1\"\n")
	bare := writeFile(t, dir, "bare.toml", "listen_addr = \":1\"\n")
	notTable := writeFile(t, dir, "nottable.toml", "server = 5\n")
	wrongType := writeFile(t, dir, "wrongtype.toml", "[server]\nlisten_addr = 8443\n")
	badFormat := writeFile(t, dir, "badformat.toml", "[log]\nformat = \"xml\"\n")
	broken := writeFile(t, dir, "broken.toml", "[log\n")

	tests := []struct {
		name    string
		file    string
		env     map[string]string
		flags   map[string]string
		wantErr string
	}{
		{name: "named file is missing", file: filepath.Join(dir, "missing.toml"), wantErr: "missing.toml"},
		{name: "file is not toml", file: broken, wantErr: "broken.toml"},
		{name: "unknown key in file", file: unknown, wantErr: "unknown settings: server.listen"},
		{name: "bare key at the top of the file", file: bare, wantErr: "unknown settings: listen_addr"},
		{name: "section that is not a table", file: notTable, wantErr: "unknown settings: server"},
		{name: "wrong type in file", file: wrongType, wantErr: "wrongtype.toml: server.listen_addr: 8443 has the wrong type"},
		{name: "invalid format in file", file: badFormat, wantErr: `log.format: "xml" is not allowed`},
		{name: "invalid level from env", env: map[string]string{"THESERVER_LOG_LEVEL": "loud"}, wantErr: `environment THESERVER_LOG_LEVEL: log.level: "loud"`},
		{name: "listen address without port from flag", flags: map[string]string{"listen": "localhost"}, wantErr: "flag --listen: server.listen_addr"},
		{name: "empty data dir from env", env: map[string]string{"THESERVER_SERVER_DATADIR": ""}, wantErr: "server.data_dir: must not be empty"},
		{name: "busy timeout is not a number", env: map[string]string{"THESERVER_STORE_BUSYTIMEOUTMS": "soon"}, wantErr: "is not a whole number"},
		{name: "negative busy timeout", env: map[string]string{"THESERVER_STORE_BUSYTIMEOUTMS": "-1"}, wantErr: "store.busy_timeout_ms"},
		{name: "only a certificate", env: map[string]string{"THESERVER_TLS_CERTFILE": "a.crt"}, wantErr: "tls.cert_file and tls.key_file"},
		{name: "only a key", env: map[string]string{"THESERVER_TLS_KEYFILE": "a.key"}, wantErr: "tls.cert_file and tls.key_file"},
		{name: "zero ack batch", env: map[string]string{"THESERVER_LINK_ACKBATCH": "0"}, wantErr: "link.ack_batch"},
		{name: "negative pong timeout", env: map[string]string{"THESERVER_LINK_PONGTIMEOUTS": "-5"}, wantErr: "link.pong_timeout_s"},
		{name: "hello timeout is not a duration", env: map[string]string{"THESERVER_LINK_HELLOTIMEOUTS": "soon"}, wantErr: "link.hello_timeout_s"},
		{name: "unknown admin language", env: map[string]string{"THESERVER_ADMIN_LANGUAGE": "fr"}, wantErr: "admin.language"},
		{name: "login of a year", env: map[string]string{"THESERVER_ADMIN_SESSIONHOURS": "8760"}, wantErr: "admin.session_hours"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.file, envFrom(tt.env), tt.flags)
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

// A flag replaces an environment variable entirely; what the variable held
// does not matter then.
func TestOnlyTheWinningValueIsChecked(t *testing.T) {
	cfg, sources, err := LoadWithSources("",
		envFrom(map[string]string{"THESERVER_SERVER_DATADIR": "", "THESERVER_LOG_LEVEL": "loud"}),
		map[string]string{"data-dir": "flag-data", "log-level": "warn"})
	if err != nil {
		t.Fatalf("LoadWithSources: %v", err)
	}
	if cfg.Server.DataDir != "flag-data" || cfg.Log.Level != "warn" || sources.Of("log.level") != SourceFlag {
		t.Errorf("loaded %+v %+v from %s", cfg.Server, cfg.Log, sources.Of("log.level"))
	}
}

func TestForeignTablesAreKnownButNotErrors(t *testing.T) {
	path := writeFile(t, t.TempDir(), "theserver.toml", `
[log]
level = "warn"

[scenario]
catalogue = "/srv/scenarios"

[zeta.inner]
x = 1
`)
	cfg, sources, err := LoadWithSources(path, nil, nil)
	if err != nil {
		t.Fatalf("LoadWithSources: %v", err)
	}
	if cfg.Log.Level != "warn" || sources.File != path {
		t.Errorf("loaded %+v from %q", cfg.Log, sources.File)
	}
	if strings.Join(sources.Foreign, ",") != "scenario,zeta" {
		t.Errorf("foreign tables are %v", sources.Foreign)
	}
}

func TestWriteDefaultRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "theserver.toml")
	if err := WriteDefault(path); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	got, sources, err := LoadWithSources(path, nil, nil)
	if err != nil {
		t.Fatalf("Load of the default file: %v", err)
	}
	if got != Default() {
		t.Errorf("default file loads as %+v, want %+v", got, Default())
	}
	for _, s := range settings.All() {
		if sources.Of(s.Key) != SourceFile {
			t.Errorf("%s comes from %s, want the file that sets every default", s.Key, sources.Of(s.Key))
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(info.Name(), ".tmp") {
		t.Errorf("the file is named %s", info.Name())
	}
}

func TestWrittenFileExplainsEverySetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theserver.toml")
	if err := Write(path, map[string]any{"link.ack_batch": 64, "server.listen_addr": "127.0.0.1:9000"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")

	for _, s := range settings.All() {
		text := s.TextIn("en")
		active := s.Name() + " = "
		commented := "# " + active
		found := false
		for i, line := range lines {
			if !strings.HasPrefix(line, active) && !strings.HasPrefix(line, commented) {
				continue
			}
			found = true
			if i < 2 || lines[i-2] != fmt.Sprintf("# %s. %s", text.Label, text.Description) {
				t.Errorf("%s is not introduced by its label and description", s.Key)
			}
			if !strings.Contains(lines[i-1], s.Env()) || !strings.HasPrefix(lines[i-1], "# Default ") {
				t.Errorf("the facts of %s read %q", s.Key, lines[i-1])
			}
			if s.Restart != strings.Contains(lines[i-1], "Changing it needs a restart.") {
				t.Errorf("the restart note of %s is wrong: %q", s.Key, lines[i-1])
			}
			if s.Flag != "" && !strings.Contains(lines[i-1], "flag --"+s.Flag) {
				t.Errorf("the facts of %s do not name its flag", s.Key)
			}
			set := s.Key == "link.ack_batch" || s.Key == "server.listen_addr"
			if set != strings.HasPrefix(line, active) {
				t.Errorf("%s line is %q", s.Key, line)
			}
			if !set && !strings.Contains(line, fmt.Sprint(s.Default)) {
				t.Errorf("%s line %q does not show the default %v", s.Key, line, s.Default)
			}
		}
		if !found {
			t.Errorf("%s is missing from the file", s.Key)
		}
	}
	for _, want := range []string{"ack_batch = 64", `listen_addr = "127.0.0.1:9000"`, "from 1 to 1024", "one of debug, info, warn, error", "Default 5000 ms"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the file lacks %q", want)
		}
	}
}

// Writing a set of values and loading the file gives the same values back,
// and writing them again gives the same bytes.
func TestWriteThenLoadIsIdentical(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "theserver.toml", `
# a comment theserver does not keep
[scenario]
catalogue = "/srv/scenarios"
ratings = ["16", "18"]

[log]
level = "info"
`)
	values := map[string]any{
		"server.listen_addr":    "127.0.0.1:9443",
		"server.data_dir":       `C:\theserver\data`,
		"tls.cert_file":         "a b.crt",
		"tls.key_file":          `quote "key".pem`,
		"store.busy_timeout_ms": "2s",
		"link.ack_batch":        int64(7),
		"log.level":             "debug",
		"admin.language":        "de",
		"admin.session_hours":   3,
	}
	if err := Write(path, values); err != nil {
		t.Fatalf("Write: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, sources, err := LoadWithSources(path, nil, nil)
	if err != nil {
		t.Fatalf("Load: %v\n%s", err, first)
	}
	want := defaultsWith(func(c *Config) {
		c.Server = Server{ListenAddr: "127.0.0.1:9443", DataDir: `C:\theserver\data`}
		c.TLS = TLS{CertFile: "a b.crt", KeyFile: `quote "key".pem`}
		c.Store.BusyTimeoutMs = 2000
		c.Link.AckBatch = 7
		c.Log.Level = "debug"
		c.Admin = Admin{Language: "de", SessionHours: 3}
	})
	if cfg != want {
		t.Errorf("loaded\n %+v\nwant\n %+v", cfg, want)
	}
	for _, s := range settings.All() {
		_, set := values[s.Key]
		if want := map[bool]Source{true: SourceFile, false: SourceDefault}[set]; sources.Of(s.Key) != want {
			t.Errorf("%s comes from %s, want %s", s.Key, sources.Of(s.Key), want)
		}
	}
	if strings.Join(sources.Foreign, ",") != "scenario" {
		t.Errorf("the foreign table is lost: %v\n%s", sources.Foreign, first)
	}
	if !strings.Contains(string(first), `ratings = ["16", "18"]`) || strings.Contains(string(first), "does not keep") {
		t.Errorf("the foreign table was not kept as found, or a hand comment survived:\n%s", first)
	}

	again := map[string]any{}
	for key := range values {
		again[key] = cfg.Get(key)
	}
	if err := Write(path, again); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("writing the loaded values again changed the file:\n%s\n---\n%s", first, second)
	}
}

func TestWriteRefusesBadValuesAndKeepsTheFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), "theserver.toml", "[log]\nlevel = \"warn\"\n")
	err := Write(path, map[string]any{"log.level": "loud", "no.such": 1})
	if err == nil || !strings.Contains(err.Error(), "log.level") || !strings.Contains(err.Error(), "no.such") {
		t.Fatalf("Write returned %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "[log]\nlevel = \"warn\"\n" {
		t.Errorf("a refused write changed the file to %q", data)
	}
	broken := writeFile(t, t.TempDir(), "theserver.toml", "[log\n")
	if err := Write(broken, map[string]any{"log.level": "warn"}); err == nil {
		t.Error("Write replaced a file it could not read")
	}
}

func TestWriteDefaultKeepsExistingFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), "theserver.toml", "# edited by hand\n")
	if err := WriteDefault(path); err == nil {
		t.Fatal("WriteDefault overwrote an existing file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# edited by hand\n" {
		t.Errorf("existing file changed to %q", data)
	}
}

func TestLoadWithSources(t *testing.T) {
	file := writeFile(t, t.TempDir(), "partial.toml", `
[server]
listen_addr = "127.0.0.1:9000"

[link]
ack_batch = 8
`)
	cfg, sources, err := LoadWithSources(file,
		envFrom(map[string]string{"THESERVER_LINK_ACKBATCH": "16", "THESERVER_LOG_FORMAT": "json"}),
		map[string]string{"log-level": "debug", "config": file})
	if err != nil {
		t.Fatalf("LoadWithSources: %v", err)
	}
	if sources.File != file {
		t.Errorf("the file in use is %q, want %q", sources.File, file)
	}
	want := map[string]Source{
		"server.listen_addr":    SourceFile,
		"server.data_dir":       SourceDefault,
		"link.ack_batch":        SourceEnv,
		"log.format":            SourceEnv,
		"log.level":             SourceFlag,
		"store.busy_timeout_ms": SourceDefault,
	}
	for key, source := range want {
		if sources.Of(key) != source {
			t.Errorf("%s comes from %q, want %q", key, sources.Of(key), source)
		}
	}
	if len(sources.Keys) != len(settings.All()) {
		t.Errorf("%d sources for %d settings", len(sources.Keys), len(settings.All()))
	}
	if cfg.Link.AckBatch != 16 || cfg.Server.ListenAddr != "127.0.0.1:9000" || cfg.Log.Level != "debug" {
		t.Errorf("loaded %+v", cfg)
	}
	if (Sources{}).Of("server.listen_addr") != SourceDefault {
		t.Error("without sources a setting does not read as default")
	}
}

func TestLogValueListsEverySection(t *testing.T) {
	value := Default().LogValue()
	var sections []string
	for _, attr := range value.Group() {
		sections = append(sections, attr.Key)
	}
	if strings.Join(sections, ",") != strings.Join(settings.Sections(), ",") {
		t.Errorf("logged sections are %v", sections)
	}
}
