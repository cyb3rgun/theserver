package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	full := writeFile(t, dir, "full.toml", `
[server]
listen_addr = "127.0.0.1:9000"
data_dir = "file-data"

[store]
busy_timeout_ms = 1234

[log]
level = "warn"
format = "json"
`)
	partial := writeFile(t, dir, "partial.toml", `
[log]
level = "debug"
`)

	fileConfig := Config{
		Server: Server{ListenAddr: "127.0.0.1:9000", DataDir: "file-data"},
		Store:  Store{BusyTimeoutMs: 1234},
		Log:    Log{Level: "warn", Format: "json"},
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
			want: Config{
				Server: Default().Server,
				Store:  Default().Store,
				Log:    Log{Level: "debug", Format: "text"},
			},
		},
		{
			name: "env over defaults",
			env: map[string]string{
				"THESERVER_SERVER_LISTENADDR":   "127.0.0.1:9100",
				"THESERVER_SERVER_DATADIR":      "env-data",
				"THESERVER_STORE_BUSYTIMEOUTMS": "250",
				"THESERVER_LOG_LEVEL":           "error",
				"THESERVER_LOG_FORMAT":          "json",
			},
			want: Config{
				Server: Server{ListenAddr: "127.0.0.1:9100", DataDir: "env-data"},
				Store:  Store{BusyTimeoutMs: 250},
				Log:    Log{Level: "error", Format: "json"},
			},
		},
		{
			name: "env over file",
			file: full,
			env: map[string]string{
				"THESERVER_SERVER_LISTENADDR": "127.0.0.1:9100",
				"THESERVER_LOG_LEVEL":         "error",
			},
			want: Config{
				Server: Server{ListenAddr: "127.0.0.1:9100", DataDir: "file-data"},
				Store:  Store{BusyTimeoutMs: 1234},
				Log:    Log{Level: "error", Format: "json"},
			},
		},
		{
			name: "flags over defaults",
			flags: map[string]string{
				"listen":    "127.0.0.1:9200",
				"data-dir":  "flag-data",
				"log-level": "debug",
			},
			want: Config{
				Server: Server{ListenAddr: "127.0.0.1:9200", DataDir: "flag-data"},
				Store:  Default().Store,
				Log:    Log{Level: "debug", Format: "text"},
			},
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
			want: Config{
				Server: Server{ListenAddr: "127.0.0.1:9200", DataDir: "env-data"},
				Store:  Default().Store,
				Log:    Log{Level: "error", Format: "text"},
			},
		},
		{
			name: "flags that are not settings are ignored",
			flags: map[string]string{
				"config":  full,
				"version": "true",
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
	wrongType := writeFile(t, dir, "wrongtype.toml", "[server]\nlisten_addr = 8443\n")
	badFormat := writeFile(t, dir, "badformat.toml", "[log]\nformat = \"xml\"\n")

	tests := []struct {
		name    string
		file    string
		env     map[string]string
		flags   map[string]string
		wantErr string
	}{
		{name: "named file is missing", file: filepath.Join(dir, "missing.toml"), wantErr: "missing.toml"},
		{name: "unknown key in file", file: unknown, wantErr: "unknown settings: server.listen"},
		{name: "wrong type in file", file: wrongType, wantErr: "wrongtype.toml"},
		{name: "invalid format in file", file: badFormat, wantErr: `log.format "xml"`},
		{name: "invalid level from env", env: map[string]string{"THESERVER_LOG_LEVEL": "loud"}, wantErr: `log.level "loud"`},
		{name: "listen address without port from flag", flags: map[string]string{"listen": "localhost"}, wantErr: "server.listen_addr"},
		{name: "empty data dir from env", env: map[string]string{"THESERVER_SERVER_DATADIR": ""}, wantErr: "server.data_dir"},
		{name: "busy timeout is not a number", env: map[string]string{"THESERVER_STORE_BUSYTIMEOUTMS": "soon"}, wantErr: "is not a whole number"},
		{name: "negative busy timeout", env: map[string]string{"THESERVER_STORE_BUSYTIMEOUTMS": "-1"}, wantErr: "store.busy_timeout_ms"},
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

func TestWriteDefaultRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "theserver.toml")
	if err := WriteDefault(path); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	got, err := Load(path, nil, nil)
	if err != nil {
		t.Fatalf("Load of the default file: %v", err)
	}
	if got != Default() {
		t.Errorf("default file loads as %+v, want %+v", got, Default())
	}
}

func TestWriteDefaultCommentsEverySetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theserver.toml")
	if err := WriteDefault(path); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")

	def := Default()
	for _, s := range settings {
		want := s.key + " = "
		found := false
		for i, line := range lines {
			if !strings.HasPrefix(line, want) {
				continue
			}
			found = true
			if i == 0 || !strings.HasPrefix(lines[i-1], "# ") {
				t.Errorf("%s.%s has no comment line above it", s.section, s.key)
			} else if !strings.Contains(lines[i-1], s.env) {
				t.Errorf("comment of %s.%s does not name %s", s.section, s.key, s.env)
			}
			want := fmt.Sprint(s.value(&def))
			if !strings.Contains(line, want) {
				t.Errorf("%s.%s line %q does not carry the default %q", s.section, s.key, line, want)
			}
		}
		if !found {
			t.Errorf("%s.%s is missing from the default file", s.section, s.key)
		}
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

// TestSettingsCoverEveryField guards the settings table: every leaf field of
// Config must have exactly one row, so that no setting is left out of the
// default file or the overrides.
func TestSettingsCoverEveryField(t *testing.T) {
	var cfg Config
	textRows := map[*string]int{}
	numberRows := map[*int]int{}
	for _, s := range settings {
		switch {
		case s.text != nil && s.number != nil:
			t.Errorf("%s.%s is both text and number", s.section, s.key)
		case s.text != nil:
			textRows[s.text(&cfg)]++
		case s.number != nil:
			numberRows[s.number(&cfg)]++
		default:
			t.Errorf("%s.%s points at no field", s.section, s.key)
		}
	}

	root := reflect.ValueOf(&cfg).Elem()
	leaves := 0
	for i := range root.NumField() {
		section := root.Field(i)
		for j := range section.NumField() {
			leaves++
			field := section.Field(j)
			name := root.Type().Field(i).Name + "." + section.Type().Field(j).Name
			var rows int
			switch ptr := field.Addr().Interface().(type) {
			case *string:
				rows = textRows[ptr]
			case *int:
				rows = numberRows[ptr]
			default:
				t.Errorf("%s is neither string nor int; extend the settings table for its type", name)
				continue
			}
			if rows != 1 {
				t.Errorf("%s has %d rows in the settings table, want 1", name, rows)
			}
		}
	}
	if leaves != len(settings) {
		t.Errorf("Config has %d settings, the table has %d rows", leaves, len(settings))
	}
}
