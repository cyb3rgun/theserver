// Package config loads the configuration of theserver from layered sources.
//
// Precedence, lowest first: built in defaults, the TOML file, environment
// variables prefixed THESERVER_, command line flags. Every setting is listed
// once in the settings table, which drives the default file, the environment
// and flag overrides and the log output. Adding a setting means adding a
// struct field, its default and one table row.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// EnvPrefix is the prefix of every environment variable that Load reads.
const EnvPrefix = "THESERVER_"

// Config is the complete configuration of theserver.
type Config struct {
	Server Server `toml:"server"`
	Store  Store  `toml:"store"`
	Log    Log    `toml:"log"`
}

// Server holds the network and storage settings.
type Server struct {
	ListenAddr string `toml:"listen_addr"`
	DataDir    string `toml:"data_dir"`
}

// Store holds the database settings.
type Store struct {
	BusyTimeoutMs int `toml:"busy_timeout_ms"`
}

// Log holds the logging settings.
type Log struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

var (
	logLevels  = []string{"debug", "info", "warn", "error"}
	logFormats = []string{"text", "json"}
)

// Default returns the built in defaults.
func Default() Config {
	return Config{
		Server: Server{
			ListenAddr: ":8443",
			DataDir:    "./data",
		},
		Store: Store{
			BusyTimeoutMs: 5000,
		},
		Log: Log{
			Level:  "info",
			Format: "text",
		},
	}
}

// setting describes one configuration value: its place in the TOML file, the
// environment variable and the flag that override it, and what it means.
// Exactly one of text and number points at the field it stands for.
type setting struct {
	section string
	key     string
	env     string
	flag    string // empty when the setting has no flag
	comment string
	text    func(*Config) *string
	number  func(*Config) *int
}

// value is the setting as it is written into the file and the log.
func (s setting) value(c *Config) any {
	if s.text != nil {
		return *s.text(c)
	}
	return *s.number(c)
}

// apply parses one override, which always arrives as text, and assigns it.
func (s setting) apply(c *Config, value string) error {
	if s.text != nil {
		*s.text(c) = value
		return nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s.%s: %q is not a whole number", s.section, s.key, value)
	}
	*s.number(c) = n
	return nil
}

// logValue keeps a number a number in the log.
func (s setting) logValue(c *Config) slog.Attr {
	if s.text != nil {
		return slog.String(s.key, *s.text(c))
	}
	return slog.Int(s.key, *s.number(c))
}

// settings lists every setting. Rows of the same section must be adjacent,
// because the default file writes one table per run of rows.
var settings = []setting{
	{
		section: "server", key: "listen_addr", env: EnvPrefix + "SERVER_LISTENADDR", flag: "listen",
		comment: "Address and port the HTTP server listens on.",
		text:    func(c *Config) *string { return &c.Server.ListenAddr },
	},
	{
		section: "server", key: "data_dir", env: EnvPrefix + "SERVER_DATADIR", flag: "data-dir",
		comment: "Directory for runtime data.",
		text:    func(c *Config) *string { return &c.Server.DataDir },
	},
	{
		section: "store", key: "busy_timeout_ms", env: EnvPrefix + "STORE_BUSYTIMEOUTMS",
		comment: "Milliseconds a statement waits for a locked database.",
		number:  func(c *Config) *int { return &c.Store.BusyTimeoutMs },
	},
	{
		section: "log", key: "level", env: EnvPrefix + "LOG_LEVEL", flag: "log-level",
		comment: "Lowest level that is logged: debug, info, warn or error.",
		text:    func(c *Config) *string { return &c.Log.Level },
	},
	{
		section: "log", key: "format", env: EnvPrefix + "LOG_FORMAT",
		comment: "Log output format: text or json.",
		text:    func(c *Config) *string { return &c.Log.Format },
	},
}

// Load builds the effective configuration. It starts from the built in
// defaults, applies the TOML file at path unless path is empty, then the
// environment variables found by lookupEnv, then flags. flags holds the flags
// that were set on the command line, keyed by flag name without dashes; flags
// that are not settings are ignored. A file that is named but missing, an
// unknown key in the file and an invalid value are errors.
func Load(path string, lookupEnv func(string) (string, bool), flags map[string]string) (Config, error) {
	cfg := Default()

	if path != "" {
		md, err := toml.DecodeFile(path, &cfg)
		if err != nil {
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		}
		if undecoded := md.Undecoded(); len(undecoded) > 0 {
			keys := make([]string, len(undecoded))
			for i, key := range undecoded {
				keys[i] = key.String()
			}
			return Config{}, fmt.Errorf("config file %s: unknown settings: %s", path, strings.Join(keys, ", "))
		}
	}

	if lookupEnv != nil {
		for _, s := range settings {
			if value, ok := lookupEnv(s.env); ok {
				if err := s.apply(&cfg, value); err != nil {
					return Config{}, fmt.Errorf("environment %s: %w", s.env, err)
				}
			}
		}
	}

	for _, s := range settings {
		if s.flag == "" {
			continue
		}
		if value, ok := flags[s.flag]; ok {
			if err := s.apply(&cfg, value); err != nil {
				return Config{}, fmt.Errorf("flag --%s: %w", s.flag, err)
			}
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate reports every setting that holds an invalid value.
func (c Config) Validate() error {
	var errs []error
	if _, _, err := net.SplitHostPort(c.Server.ListenAddr); err != nil {
		errs = append(errs, fmt.Errorf("server.listen_addr %q is not host:port: %w", c.Server.ListenAddr, err))
	}
	if c.Server.DataDir == "" {
		errs = append(errs, errors.New("server.data_dir must not be empty"))
	}
	if c.Store.BusyTimeoutMs < 0 {
		errs = append(errs, fmt.Errorf("store.busy_timeout_ms is %d, it must not be negative", c.Store.BusyTimeoutMs))
	}
	if !slices.Contains(logLevels, c.Log.Level) {
		errs = append(errs, fmt.Errorf("log.level %q is not one of %s", c.Log.Level, strings.Join(logLevels, ", ")))
	}
	if !slices.Contains(logFormats, c.Log.Format) {
		errs = append(errs, fmt.Errorf("log.format %q is not one of %s", c.Log.Format, strings.Join(logFormats, ", ")))
	}
	return errors.Join(errs...)
}

// SlogLevel returns the configured level as a slog.Level. An invalid level,
// which Validate rejects, maps to info.
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
// configuration can be logged as a single attribute.
func (c Config) LogValue() slog.Value {
	var sections []string
	bySection := map[string][]slog.Attr{}
	for _, s := range settings {
		if _, ok := bySection[s.section]; !ok {
			sections = append(sections, s.section)
		}
		bySection[s.section] = append(bySection[s.section], s.logValue(&c))
	}
	attrs := make([]slog.Attr, 0, len(sections))
	for _, name := range sections {
		attrs = append(attrs, slog.Attr{Key: name, Value: slog.GroupValue(bySection[name]...)})
	}
	return slog.GroupValue(attrs...)
}

const defaultHeader = `# theserver configuration
#
# Every setting is listed with its built in default and a one line comment.
# Precedence, highest first: command line flags, environment variables,
# this file, built in defaults.
`

// defaultTOML returns the content that WriteDefault writes.
func defaultTOML() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(defaultHeader)
	def := Default()
	section := ""
	for _, s := range settings {
		if s.section != section {
			fmt.Fprintf(&buf, "\n[%s]\n", s.section)
			section = s.section
		}
		fmt.Fprintf(&buf, "# %s %s\n", s.comment, s.overrides())
		if err := toml.NewEncoder(&buf).Encode(map[string]any{s.key: s.value(&def)}); err != nil {
			return nil, fmt.Errorf("encode %s.%s: %w", s.section, s.key, err)
		}
	}
	return buf.Bytes(), nil
}

func (s setting) overrides() string {
	if s.flag == "" {
		return fmt.Sprintf("Environment %s.", s.env)
	}
	return fmt.Sprintf("Environment %s, flag --%s.", s.env, s.flag)
}

// WriteDefault writes a TOML file at path in which every setting appears with
// its built in default and a one line comment. Missing parent directories are
// created. An existing file is never overwritten.
func WriteDefault(path string) error {
	data, err := defaultTOML()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("config file %s already exists, not overwriting it", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}
