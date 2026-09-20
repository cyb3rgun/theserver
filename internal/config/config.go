// Package config loads the configuration of theserver from layered sources.
//
// Precedence, lowest first: built in defaults, the TOML file, environment
// variables prefixed THESERVER_, command line flags. Every setting, with its
// default, range, flag and texts, is declared once in internal/settings; this
// package maps the registry onto the Config struct by its toml tags, writes
// the configuration file back (D-032), and applies changes while the server
// runs (Runtime).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/cyb3rgun/theserver/internal/settings"
)

// EnvPrefix is the prefix of every environment variable that Load reads.
const EnvPrefix = settings.EnvPrefix

// FileName is the configuration file that a server started without --config
// keeps in its data directory: the first saved change creates it, and every
// later start without --config reads it.
const FileName = "theserver.toml"

// DataDirFile is the configuration file of a server started without
// --config whose data directory is dataDir.
func DataDirFile(dataDir string) string {
	return filepath.Join(dataDir, FileName)
}

// Config is the complete configuration of theserver. Every field is a
// setting of the registry, found by its section and key tags.
type Config struct {
	Server  Server  `toml:"server"`
	TLS     TLS     `toml:"tls"`
	Store   Store   `toml:"store"`
	Content Content `toml:"content"`
	Link    Link    `toml:"link"`
	Log     Log     `toml:"log"`
	Admin   Admin   `toml:"admin"`
}

// Server holds the network and storage settings.
type Server struct {
	ListenAddr string `toml:"listen_addr"`
	DataDir    string `toml:"data_dir"`
}

// TLS names the certificate the server presents. Both empty means the
// certificate that theserver creates in the tls folder of the data directory.
type TLS struct {
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
}

// Link holds the timing of the device link.
type Link struct {
	AckIntervalMs int `toml:"ack_interval_ms"`
	AckBatch      int `toml:"ack_batch"`
	PingIntervalS int `toml:"ping_interval_s"`
	PongTimeoutS  int `toml:"pong_timeout_s"`
	HelloTimeoutS int `toml:"hello_timeout_s"`
	// InstallTimeoutS is how long the start of a session waits for a device
	// to install the scenario before the pages call it not ready (D-059).
	InstallTimeoutS int `toml:"install_timeout_s"`
}

// Store holds the database settings.
type Store struct {
	BusyTimeoutMs int `toml:"busy_timeout_ms"`
}

// Content holds the settings of the scenario packages (D-036).
type Content struct {
	MaxUploadMB int    `toml:"max_upload_mb"`
	Dir         string `toml:"dir"`
}

// ContentDir is the directory of the scenario packages: Content.Dir, or the
// folder content in the data directory when it is empty.
func (c Config) ContentDir() string {
	if c.Content.Dir != "" {
		return c.Content.Dir
	}
	return filepath.Join(c.Server.DataDir, "content")
}

// Log holds the logging settings.
type Log struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

// Admin holds the settings of the admin pages.
type Admin struct {
	Language     string `toml:"language"`
	SessionHours int    `toml:"session_hours"`
}

// fields maps every registry key to the index path of its Config field.
var fields = func() map[string][]int {
	index := map[string][]int{}
	root := reflect.TypeOf(Config{})
	for i := range root.NumField() {
		section := root.Field(i)
		for j := range section.Type.NumField() {
			key := section.Tag.Get("toml") + "." + section.Type.Field(j).Tag.Get("toml")
			index[key] = []int{i, j}
		}
	}
	for _, s := range settings.All() {
		if _, ok := index[s.Key]; !ok {
			panic("config: setting " + s.Key + " has no field in Config")
		}
	}
	return index
}()

// Get returns the value of the setting key in its canonical form: a string,
// an int64 or a bool. An unknown key gives nil.
func (c Config) Get(key string) any {
	path, ok := fields[key]
	if !ok {
		return nil
	}
	field := reflect.ValueOf(c).FieldByIndex(path)
	switch field.Kind() {
	case reflect.String:
		return field.String()
	case reflect.Int:
		return field.Int()
	case reflect.Bool:
		return field.Bool()
	}
	return nil
}

// set assigns a canonical value, as settings.Parse returns it.
func (c *Config) set(key string, value any) {
	field := reflect.ValueOf(c).Elem().FieldByIndex(fields[key])
	switch v := value.(type) {
	case string:
		field.SetString(v)
	case int64:
		field.SetInt(v)
	case bool:
		field.SetBool(v)
	default:
		panic(fmt.Sprintf("config: %s cannot hold %T", key, value))
	}
}

// Default returns the built in defaults of the registry.
func Default() Config {
	var c Config
	for _, s := range settings.All() {
		c.set(s.Key, s.Default)
	}
	return c
}

// Load builds the effective configuration. It starts from the built in
// defaults, applies the TOML file at path, then the environment variables
// found by lookupEnv, then flags. flags holds the flags that were set on the
// command line, keyed by flag name without dashes; flags that are not
// settings are ignored. With an empty path the file is FileName in the data
// directory that the defaults, the environment and the flags give, when it
// exists. A file that is named but missing, an unknown key in a section of
// theserver and an invalid value are errors.
func Load(path string, lookupEnv func(string) (string, bool), flags map[string]string) (Config, error) {
	cfg, _, err := LoadWithSources(path, lookupEnv, flags)
	return cfg, err
}

// A Source says where the effective value of a setting came from.
type Source string

// The sources of a setting, lowest precedence first.
const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
	SourceFlag    Source = "flag"
)

// Sources tells where the configuration came from: the file in use and, per
// setting written section.key, the layer of its value.
type Sources struct {
	// File is the TOML file in use, empty when the server runs without one:
	// the file named by --config, or FileName found in the data directory.
	File string
	// Keys maps a setting to the source of its value.
	Keys map[string]Source
	// Foreign lists the tables of the file that are not theserver
	// settings; they are kept when the file is written again.
	Foreign []string
}

// Of returns the source of key, SourceDefault when none is recorded.
func (s Sources) Of(key string) Source {
	if source, ok := s.Keys[key]; ok {
		return source
	}
	return SourceDefault
}

func (s Sources) clone() Sources {
	out := Sources{File: s.File, Keys: map[string]Source{}, Foreign: slices.Clone(s.Foreign)}
	for key, source := range s.Keys {
		out.Keys[key] = source
	}
	return out
}

// LoadWithSources is Load that also reports where the configuration came
// from.
func LoadWithSources(path string, lookupEnv func(string) (string, bool), flags map[string]string) (Config, Sources, error) {
	cfg := Default()
	sources := Sources{Keys: map[string]Source{}}
	for _, s := range settings.All() {
		sources.Keys[s.Key] = SourceDefault
	}

	// An environment variable replaces the file, a flag replaces both. Only
	// the value that wins is checked, so a flag can stand in for a variable
	// that holds something unusable.
	overrides := map[string]override{}
	for _, s := range settings.All() {
		if lookupEnv != nil {
			if raw, ok := lookupEnv(s.Env()); ok {
				overrides[s.Key] = override{raw, "environment " + s.Env(), SourceEnv}
			}
		}
		if s.Flag != "" {
			if raw, ok := flags[s.Flag]; ok {
				overrides[s.Key] = override{raw, "flag --" + s.Flag, SourceFlag}
			}
		}
	}

	if path == "" {
		found, err := dataDirFileOf(cfg, overrides)
		if err != nil {
			return Config{}, Sources{}, err
		}
		path = found
	}
	sources.File = path
	if path != "" {
		layer, foreign, err := readFile(path)
		if err != nil {
			return Config{}, Sources{}, err
		}
		for key, value := range layer {
			cfg.set(key, value)
			sources.Keys[key] = SourceFile
		}
		for name := range foreign {
			sources.Foreign = append(sources.Foreign, name)
		}
		slices.Sort(sources.Foreign)
	}

	for _, s := range settings.All() {
		o, ok := overrides[s.Key]
		if !ok {
			continue
		}
		value, err := s.Parse(o.raw)
		if err != nil {
			return Config{}, Sources{}, fmt.Errorf("%s: %w", o.origin, err)
		}
		cfg.set(s.Key, value)
		sources.Keys[s.Key] = o.source
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, Sources{}, err
	}
	return cfg, sources, nil
}

// An override is the value an environment variable or a flag gives a
// setting.
type override struct {
	raw    string
	origin string
	source Source
}

// dataDirFileOf returns FileName in the data directory that cfg and the
// overrides give, or "" when there is no such file. A data directory that an
// override gives but the registry refuses finds no file; LoadWithSources
// reports it with the other overrides.
func dataDirFileOf(cfg Config, overrides map[string]override) (string, error) {
	dir := cfg.Server.DataDir
	if o, ok := overrides["server.data_dir"]; ok {
		value, err := settings.Parse("server.data_dir", o.raw)
		if err != nil {
			return "", nil
		}
		dir = value.(string)
	}
	path := DataDirFile(dir)
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("config file %s: %w", path, err)
	case info.IsDir():
		return "", fmt.Errorf("config file %s is a directory", path)
	}
	return path, nil
}

// readFile reads the settings a configuration file sets, checked through
// the registry, and the tables of the file that are not theserver settings.
// An unknown key in a section of theserver is an error, so a typo does not
// fall back to a default silently.
func readFile(path string) (layer, foreign map[string]any, err error) {
	var tree map[string]any
	if _, err := toml.DecodeFile(path, &tree); err != nil {
		return nil, nil, fmt.Errorf("config file %s: %w", path, err)
	}
	sections := settings.Sections()
	layer, foreign = map[string]any{}, map[string]any{}
	var unknown []string
	var errs []error
	for name, value := range tree {
		table, isTable := value.(map[string]any)
		switch {
		case !slices.Contains(sections, name) && isTable:
			foreign[name] = table
			continue
		case !isTable:
			unknown = append(unknown, name)
			continue
		}
		for key, raw := range table {
			s, ok := settings.Get(name + "." + key)
			if !ok {
				unknown = append(unknown, name+"."+key)
				continue
			}
			parsed, err := s.Parse(raw)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			layer[s.Key] = parsed
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, nil, fmt.Errorf("config file %s: unknown settings: %s", path, strings.Join(unknown, ", "))
	}
	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("config file %s: %w", path, errors.Join(errs...))
	}
	return layer, foreign, nil
}

// ErrCertKeyPair refuses a certificate without its key or a key without its
// certificate.
var ErrCertKeyPair = errors.New("tls.cert_file and tls.key_file must be set together or both left empty")

// Validate reports every setting that holds a value the registry refuses,
// and a certificate without its key.
func (c Config) Validate() error {
	var errs []error
	for _, s := range settings.All() {
		if _, err := s.Parse(c.Get(s.Key)); err != nil {
			errs = append(errs, err)
		}
	}
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		errs = append(errs, ErrCertKeyPair)
	}
	return errors.Join(errs...)
}

// SlogLevel returns the configured level as a slog.Level. An invalid level,
// which Validate refuses, maps to info.
func (l Log) SlogLevel() slog.Level {
	switch l.Level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogValue reports every setting grouped by section, so the effective
// configuration can be logged as a single attribute. Sensitive settings are
// left out.
func (c Config) LogValue() slog.Value {
	var groups []slog.Attr
	for _, section := range settings.Sections() {
		var attrs []slog.Attr
		for _, s := range settings.All() {
			if s.Section != section || s.Sensitive {
				continue
			}
			attrs = append(attrs, slog.Any(s.Name(), c.Get(s.Key)))
		}
		groups = append(groups, slog.Attr{Key: section, Value: slog.GroupValue(attrs...)})
	}
	return slog.GroupValue(groups...)
}
