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
	TLS    TLS    `toml:"tls"`
	Store  Store  `toml:"store"`
	Link   Link   `toml:"link"`
	Log    Log    `toml:"log"`
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
		Link: Link{
			AckIntervalMs: 100,
			AckBatch:      32,
			PingIntervalS: 15,
			PongTimeoutS:  10,
			HelloTimeoutS: 5,
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
		section: "tls", key: "cert_file", env: EnvPrefix + "TLS_CERTFILE",
		comment: "PEM certificate to serve; empty with key_file empty uses the one created in <data_dir>/tls.",
		text:    func(c *Config) *string { return &c.TLS.CertFile },
	},
	{
		section: "tls", key: "key_file", env: EnvPrefix + "TLS_KEYFILE",
		comment: "PEM private key of cert_file; set both or neither.",
		text:    func(c *Config) *string { return &c.TLS.KeyFile },
	},
	{
		section: "store", key: "busy_timeout_ms", env: EnvPrefix + "STORE_BUSYTIMEOUTMS",
		comment: "Milliseconds a statement waits for a locked database.",
		number:  func(c *Config) *int { return &c.Store.BusyTimeoutMs },
	},
	{
		section: "link", key: "ack_interval_ms", env: EnvPrefix + "LINK_ACKINTERVALMS",
		comment: "Milliseconds after which received events are stored and acknowledged.",
		number:  func(c *Config) *int { return &c.Link.AckIntervalMs },
	},
	{
		section: "link", key: "ack_batch", env: EnvPrefix + "LINK_ACKBATCH",
		comment: "Number of received events that are stored and acknowledged at once.",
		number:  func(c *Config) *int { return &c.Link.AckBatch },
	},
	{
		section: "link", key: "ping_interval_s", env: EnvPrefix + "LINK_PINGINTERVALS",
		comment: "Seconds between two pings to a device.",
		number:  func(c *Config) *int { return &c.Link.PingIntervalS },
	},
	{
		section: "link", key: "pong_timeout_s", env: EnvPrefix + "LINK_PONGTIMEOUTS",
		comment: "Seconds a device has to answer a ping before it is dropped.",
		number:  func(c *Config) *int { return &c.Link.PongTimeoutS },
	},
	{
		section: "link", key: "hello_timeout_s", env: EnvPrefix + "LINK_HELLOTIMEOUTS",
		comment: "Seconds a new connection has to send its hello.",
		number:  func(c *Config) *int { return &c.Link.HelloTimeoutS },
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

// Sources maps a setting, written section.key, to the source of its value.
type Sources map[string]Source

// LoadWithSources is Load that also reports, for every setting, which layer
// its effective value came from.
func LoadWithSources(path string, lookupEnv func(string) (string, bool), flags map[string]string) (Config, Sources, error) {
	cfg := Default()
	sources := Sources{}
	for _, s := range settings {
		sources[s.name()] = SourceDefault
	}

	if path != "" {
		md, err := toml.DecodeFile(path, &cfg)
		if err != nil {
			return Config{}, nil, fmt.Errorf("config file %s: %w", path, err)
		}
		if undecoded := md.Undecoded(); len(undecoded) > 0 {
			keys := make([]string, len(undecoded))
			for i, key := range undecoded {
				keys[i] = key.String()
			}
			return Config{}, nil, fmt.Errorf("config file %s: unknown settings: %s", path, strings.Join(keys, ", "))
		}
		for _, s := range settings {
			if md.IsDefined(s.section, s.key) {
				sources[s.name()] = SourceFile
			}
		}
	}

	if lookupEnv != nil {
		for _, s := range settings {
			if value, ok := lookupEnv(s.env); ok {
				if err := s.apply(&cfg, value); err != nil {
					return Config{}, nil, fmt.Errorf("environment %s: %w", s.env, err)
				}
				sources[s.name()] = SourceEnv
			}
		}
	}

	for _, s := range settings {
		if s.flag == "" {
			continue
		}
		if value, ok := flags[s.flag]; ok {
			if err := s.apply(&cfg, value); err != nil {
				return Config{}, nil, fmt.Errorf("flag --%s: %w", s.flag, err)
			}
			sources[s.name()] = SourceFlag
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, nil, err
	}
	return cfg, sources, nil
}

// A Setting describes one setting with its effective value, for the API and
// the admin page.
type Setting struct {
	Key     string `json:"key"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	Source  Source `json:"source"`
	Env     string `json:"env"`
	Flag    string `json:"flag,omitempty"`
	Comment string `json:"comment"`
}

// Describe lists every setting of c in the order of the default file. A
// setting missing from sources is reported as coming from its default.
func Describe(c Config, sources Sources) []Setting {
	def := Default()
	described := make([]Setting, 0, len(settings))
	for _, s := range settings {
		source := sources[s.name()]
		if source == "" {
			source = SourceDefault
		}
		flag := ""
		if s.flag != "" {
			flag = "--" + s.flag
		}
		described = append(described, Setting{
			Key:     s.name(),
			Value:   s.value(&c),
			Default: s.value(&def),
			Source:  source,
			Env:     s.env,
			Flag:    flag,
			Comment: s.comment,
		})
	}
	return described
}

func (s setting) name() string {
	return s.section + "." + s.key
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
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		errs = append(errs, errors.New("tls.cert_file and tls.key_file must be set together or both left empty"))
	}
	if c.Store.BusyTimeoutMs < 0 {
		errs = append(errs, fmt.Errorf("store.busy_timeout_ms is %d, it must not be negative", c.Store.BusyTimeoutMs))
	}
	for _, positive := range []struct {
		key   string
		value int
	}{
		{"link.ack_interval_ms", c.Link.AckIntervalMs},
		{"link.ack_batch", c.Link.AckBatch},
		{"link.ping_interval_s", c.Link.PingIntervalS},
		{"link.pong_timeout_s", c.Link.PongTimeoutS},
		{"link.hello_timeout_s", c.Link.HelloTimeoutS},
	} {
		if positive.value <= 0 {
			errs = append(errs, fmt.Errorf("%s is %d, it must be at least 1", positive.key, positive.value))
		}
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
