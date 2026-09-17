// Package settings is the registry of every setting of theserver (D-030).
//
// Each setting is declared once, in registry.go, with its key, type, default,
// unit, range or allowed values, whether a change needs a restart, and its
// texts in English and German. The configuration loader, the configuration
// file, the API and the admin page all read this registry; nothing else
// describes a setting.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kind is the type of a setting.
type Kind int

// The kinds of settings. The canonical Go value of String, Enum, Path and
// Addr is a string, of Int and Duration an int64, of Bool a bool.
const (
	String Kind = iota
	Int
	Bool
	Duration // a whole number of Unit
	Enum
	Path
	Addr // host:port
)

var kindNames = []string{"string", "int", "bool", "duration", "enum", "path", "addr"}

// String returns the name of the kind as the API shows it.
func (k Kind) String() string {
	if int(k) < 0 || int(k) >= len(kindNames) {
		return fmt.Sprintf("kind(%d)", int(k))
	}
	return kindNames[k]
}

// MarshalText writes the kind by name.
func (k Kind) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}

// Text is what a person reads about a setting in one language: a short
// label, a one sentence description, and the longer why.
type Text struct {
	Label       string
	Description string
	Why         string
}

// Setting describes one setting.
type Setting struct {
	Key     string // "server.listen_addr"
	Section string // "server"
	Kind    Kind
	Default any    // canonical value, see Kind
	Unit    string // "ms", "s", "h" or empty
	// Min and Max bound Int and Duration settings, in Unit; nil is unbounded.
	Min, Max *int64
	Enum     []string // the allowed values of an Enum
	// Empty allows an empty String or Path.
	Empty bool
	// Restart means a change takes effect after the next start only.
	Restart bool
	// Sensitive values are never shown in logs or exports. None is yet.
	Sensitive bool
	// Flag is the command line flag of theserver serve, without dashes, or
	// empty when the setting has none.
	Flag string
	Text map[string]Text // by language: "en", "de"
}

// Name is the key without its section, as it stands in the TOML file.
func (s Setting) Name() string {
	return strings.TrimPrefix(s.Key, s.Section+".")
}

// EnvPrefix starts the environment variable of every setting.
const EnvPrefix = "THESERVER_"

// Env is the environment variable that overrides the setting, for example
// THESERVER_SERVER_LISTENADDR for server.listen_addr.
func (s Setting) Env() string {
	return EnvPrefix + strings.ToUpper(s.Section) + "_" + strings.ToUpper(strings.ReplaceAll(s.Name(), "_", ""))
}

// TextIn returns the texts in lang, or in English when lang has none.
func (s Setting) TextIn(lang string) Text {
	if t, ok := s.Text[lang]; ok {
		return t
	}
	return s.Text[FallbackLanguage]
}

// FallbackLanguage is the language every text exists in.
const FallbackLanguage = "en"

// Languages lists the languages every setting has texts in.
func Languages() []string {
	return []string{"en", "de"}
}

// All returns every setting in the stable order of the registry: sections in
// the order of the configuration file, settings in their order within.
func All() []Setting {
	return slices.Clone(registry)
}

// Get returns the setting with key.
func Get(key string) (Setting, bool) {
	i := slices.IndexFunc(registry, func(s Setting) bool { return s.Key == key })
	if i < 0 {
		return Setting{}, false
	}
	return registry[i], true
}

// Sections lists the sections in registry order.
func Sections() []string {
	var sections []string
	for _, s := range registry {
		if !slices.Contains(sections, s.Section) {
			sections = append(sections, s.Section)
		}
	}
	return sections
}

// The reasons a value is refused. A *ValueError wraps one of them.
var (
	ErrUnknownKey  = errors.New("unknown setting")
	ErrOutOfRange  = errors.New("out of range")
	ErrNotAllowed  = errors.New("not allowed")
	ErrBadAddress  = errors.New("bad address")
	ErrBadDuration = errors.New("bad duration")
	ErrBadNumber   = errors.New("bad number")
	ErrBadBool     = errors.New("bad bool")
	ErrEmpty       = errors.New("empty")
	ErrBadType     = errors.New("wrong type")
)

var codes = map[error]string{
	ErrUnknownKey:  "unknown_setting",
	ErrOutOfRange:  "out_of_range",
	ErrNotAllowed:  "not_allowed",
	ErrBadAddress:  "bad_address",
	ErrBadDuration: "bad_duration",
	ErrBadNumber:   "bad_number",
	ErrBadBool:     "bad_bool",
	ErrEmpty:       "empty",
	ErrBadType:     "bad_type",
}

// A ValueError says why a value was refused, with what a person needs to
// correct it. Code gives its machine readable reason.
type ValueError struct {
	Key     string
	Value   any
	Err     error
	Min     *int64
	Max     *int64
	Allowed []string
	Unit    string
}

// Code is the reason as the API names it, for example "out_of_range".
func (e *ValueError) Code() string {
	return codes[e.Err]
}

func (e *ValueError) Unwrap() error {
	return e.Err
}

func (e *ValueError) Error() string {
	value := fmt.Sprintf("%v", e.Value)
	if text, ok := e.Value.(string); ok {
		value = strconv.Quote(text)
	}
	switch e.Err {
	case ErrUnknownKey:
		return fmt.Sprintf("unknown setting %q", e.Key)
	case ErrOutOfRange:
		return fmt.Sprintf("%s: %s is out of range, %s", e.Key, value, rangeText(e.Min, e.Max, e.Unit))
	case ErrNotAllowed:
		return fmt.Sprintf("%s: %s is not allowed, use one of %s", e.Key, value, strings.Join(e.Allowed, ", "))
	case ErrBadAddress:
		return fmt.Sprintf("%s: %s is not an address with a port, such as :8443 or 127.0.0.1:8443", e.Key, value)
	case ErrBadDuration:
		return fmt.Sprintf("%s: %s is not a whole number of %s or a duration such as 2%s", e.Key, value, e.Unit, e.Unit)
	case ErrBadNumber:
		return fmt.Sprintf("%s: %s is not a whole number", e.Key, value)
	case ErrBadBool:
		return fmt.Sprintf("%s: %s is not true or false", e.Key, value)
	case ErrEmpty:
		return fmt.Sprintf("%s: must not be empty", e.Key)
	case ErrBadType:
		return fmt.Sprintf("%s: %s has the wrong type", e.Key, value)
	default:
		return fmt.Sprintf("%s: %s: %v", e.Key, value, e.Err)
	}
}

func rangeText(min, max *int64, unit string) string {
	suffix := ""
	if unit != "" {
		suffix = " " + unit
	}
	switch {
	case min != nil && max != nil:
		return fmt.Sprintf("it must be between %d and %d%s", *min, *max, suffix)
	case min != nil:
		return fmt.Sprintf("it must be at least %d%s", *min, suffix)
	case max != nil:
		return fmt.Sprintf("it must be at most %d%s", *max, suffix)
	default:
		return "it has no bounds"
	}
}

// Validate reports whether value is acceptable for the setting key.
func Validate(key string, value any) error {
	_, err := Parse(key, value)
	return err
}

// Parse checks value for the setting key and returns it in its canonical
// form. Text from a form, the environment or a flag, numbers from JSON or
// TOML, and booleans are accepted where they make sense.
func Parse(key string, value any) (any, error) {
	s, ok := Get(key)
	if !ok {
		return nil, &ValueError{Key: key, Value: value, Err: ErrUnknownKey}
	}
	return s.Parse(value)
}

// Parse checks value for s and returns it in its canonical form.
func (s Setting) Parse(value any) (any, error) {
	fail := func(err error) (any, error) {
		return nil, &ValueError{Key: s.Key, Value: value, Err: err, Min: s.Min, Max: s.Max, Allowed: s.Enum, Unit: s.Unit}
	}
	switch s.Kind {
	case String, Path, Enum, Addr:
		text, ok := value.(string)
		if !ok {
			return fail(ErrBadType)
		}
		if s.Kind != String {
			text = strings.TrimSpace(text)
		}
		switch {
		case text == "" && s.Kind == Enum:
			return fail(ErrNotAllowed)
		case text == "" && !s.Empty:
			return fail(ErrEmpty)
		case s.Kind == Enum && !slices.Contains(s.Enum, text):
			return fail(ErrNotAllowed)
		case s.Kind == Addr && !validAddr(text):
			return fail(ErrBadAddress)
		}
		return text, nil

	case Int, Duration:
		n, err := s.number(value)
		if err != nil {
			return fail(err)
		}
		if (s.Min != nil && n < *s.Min) || (s.Max != nil && n > *s.Max) {
			return fail(ErrOutOfRange)
		}
		return n, nil

	case Bool:
		switch v := value.(type) {
		case bool:
			return v, nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return fail(ErrBadBool)
			}
			return b, nil
		default:
			return fail(ErrBadType)
		}
	}
	return fail(fmt.Errorf("kind %s is not handled", s.Kind))
}

// number reads a whole number, and for a Duration also a Go duration such as
// "2s", which must be a whole number of the unit.
func (s Setting) number(value any) (int64, error) {
	bad := ErrBadNumber
	if s.Kind == Duration {
		bad = ErrBadDuration
	}
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case float64:
		if v != math.Trunc(v) || math.Abs(v) > 1<<53 {
			return 0, bad
		}
		return int64(v), nil
	case json.Number:
		return s.number(v.String())
	case string:
		text := strings.TrimSpace(v)
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n, nil
		}
		if s.Kind != Duration {
			return 0, bad
		}
		d, err := time.ParseDuration(text)
		unit := unitDuration(s.Unit)
		if err != nil || unit == 0 || d%unit != 0 {
			return 0, bad
		}
		return int64(d / unit), nil
	case bool:
		return 0, ErrBadType
	default:
		return 0, ErrBadType
	}
}

func unitDuration(unit string) time.Duration {
	switch unit {
	case "ms":
		return time.Millisecond
	case "s":
		return time.Second
	case "min":
		return time.Minute
	case "h":
		return time.Hour
	}
	return 0
}

// DurationOf converts the canonical value of a Duration setting to a
// time.Duration.
func (s Setting) DurationOf(n int64) time.Duration {
	return time.Duration(n) * unitDuration(s.Unit)
}

func validAddr(text string) bool {
	_, port, err := net.SplitHostPort(text)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 0 && n <= 65535
}

// Code returns the reason of err as the API names it, or "" when err is not
// a *ValueError.
func Code(err error) string {
	var ve *ValueError
	if errors.As(err, &ve) {
		return ve.Code()
	}
	return ""
}
