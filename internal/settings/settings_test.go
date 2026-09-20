package settings

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var wantOrder = []string{
	"server.listen_addr",
	"server.data_dir",
	"tls.cert_file",
	"tls.key_file",
	"store.busy_timeout_ms",
	"content.max_upload_mb",
	"content.dir",
	"link.ack_interval_ms",
	"link.ack_batch",
	"link.ping_interval_s",
	"link.pong_timeout_s",
	"link.hello_timeout_s",
	"link.install_timeout_s",
	"log.level",
	"log.format",
	"admin.language",
	"admin.session_hours",
}

func keys(list []Setting) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.Key
	}
	return out
}

func TestOrderIsStable(t *testing.T) {
	first := All()
	if got := keys(first); !slices.Equal(got, wantOrder) {
		t.Errorf("All lists\n %v\nwant\n %v", got, wantOrder)
	}
	for range 5 {
		if again := All(); !reflect.DeepEqual(again, first) {
			t.Fatal("All changed between two calls")
		}
	}

	first[0].Key = "changed"
	if All()[0].Key == "changed" {
		t.Error("All hands out the registry itself")
	}

	seen := map[string]bool{}
	var sections []string
	for _, s := range All() {
		if seen[s.Key] {
			t.Errorf("%s is declared twice", s.Key)
		}
		seen[s.Key] = true
		if s.Key != s.Section+"."+s.Name() || s.Name() == "" || strings.Contains(s.Name(), ".") {
			t.Errorf("%s does not read section.name with section %q", s.Key, s.Section)
		}
		if len(sections) == 0 || sections[len(sections)-1] != s.Section {
			if slices.Contains(sections, s.Section) {
				t.Errorf("section %s is split; its settings must stand together", s.Section)
			}
			sections = append(sections, s.Section)
		}
	}
	if !slices.Equal(Sections(), sections) {
		t.Errorf("Sections is %v, want %v", Sections(), sections)
	}
	if got, ok := Get("link.ack_batch"); !ok || got.Kind != Int {
		t.Errorf("Get(link.ack_batch) = %+v, %v", got, ok)
	}
	if _, ok := Get("link.nothing"); ok {
		t.Error("Get found a setting that does not exist")
	}
}

// Every setting carries label, description and why in every language, and
// no other language. A missing translation fails here.
func TestEverySettingHasEveryText(t *testing.T) {
	dashes := string([]rune{0x2013, 0x2014})
	for _, s := range All() {
		var langs []string
		for lang := range s.Text {
			langs = append(langs, lang)
		}
		slices.Sort(langs)
		want := slices.Sorted(slices.Values(Languages()))
		if !slices.Equal(langs, want) {
			t.Errorf("%s has texts in %v, want %v", s.Key, langs, want)
		}
		for _, lang := range Languages() {
			text, ok := s.Text[lang]
			if !ok {
				t.Errorf("%s has no %s texts", s.Key, lang)
				continue
			}
			for field, value := range map[string]string{"label": text.Label, "description": text.Description, "why": text.Why} {
				switch {
				case strings.TrimSpace(value) == "":
					t.Errorf("%s has no %s %s", s.Key, lang, field)
				case strings.ContainsAny(value, dashes):
					t.Errorf("%s %s %s contains a dash", s.Key, lang, field)
				}
			}
			if strings.HasSuffix(text.Label, ".") {
				t.Errorf("%s %s label ends with a period", s.Key, lang)
			}
			for field, value := range map[string]string{"description": text.Description, "why": text.Why} {
				if !strings.HasSuffix(value, ".") {
					t.Errorf("%s %s %s does not end with a period", s.Key, lang, field)
				}
			}
		}
		if s.Text["de"] == s.Text["en"] {
			t.Errorf("%s has the English texts in German", s.Key)
		}
	}
}

func TestTextInFallsBackToEnglish(t *testing.T) {
	s, _ := Get("log.level")
	if s.TextIn("fr") != s.Text["en"] {
		t.Error("an unknown language does not fall back to English")
	}
	if s.TextIn("de").Label != "Protokollstufe" {
		t.Errorf("German label is %q", s.TextIn("de").Label)
	}
}

func TestDefaultsAreValidAndCanonical(t *testing.T) {
	for _, s := range All() {
		got, err := s.Parse(s.Default)
		if err != nil {
			t.Errorf("%s: default %v is refused: %v", s.Key, s.Default, err)
			continue
		}
		if !reflect.DeepEqual(got, s.Default) {
			t.Errorf("%s: default %#v parses to %#v", s.Key, s.Default, got)
		}
		switch s.Kind {
		case String, Path, Addr, Enum:
			if _, ok := s.Default.(string); !ok {
				t.Errorf("%s: default %#v is not a string", s.Key, s.Default)
			}
		case Int, Duration:
			if _, ok := s.Default.(int64); !ok {
				t.Errorf("%s: default %#v is not an int64", s.Key, s.Default)
			}
			if s.Min == nil || s.Max == nil || *s.Min > *s.Max {
				t.Errorf("%s: range %v to %v is not a closed range", s.Key, s.Min, s.Max)
			}
		case Bool:
			if _, ok := s.Default.(bool); !ok {
				t.Errorf("%s: default %#v is not a bool", s.Key, s.Default)
			}
		}
		if s.Kind == Duration && unitDuration(s.Unit) == 0 {
			t.Errorf("%s: duration unit %q is unknown", s.Key, s.Unit)
		}
		if s.Kind == Enum && len(s.Enum) == 0 {
			t.Errorf("%s: enum without values", s.Key)
		}
		if s.Kind != Enum && s.Enum != nil {
			t.Errorf("%s: values listed for a %s", s.Key, s.Kind)
		}
	}
}

func TestEnvAndFlags(t *testing.T) {
	wantEnv := map[string]string{
		"server.listen_addr":     "THESERVER_SERVER_LISTENADDR",
		"server.data_dir":        "THESERVER_SERVER_DATADIR",
		"tls.cert_file":          "THESERVER_TLS_CERTFILE",
		"tls.key_file":           "THESERVER_TLS_KEYFILE",
		"store.busy_timeout_ms":  "THESERVER_STORE_BUSYTIMEOUTMS",
		"content.max_upload_mb":  "THESERVER_CONTENT_MAXUPLOADMB",
		"content.dir":            "THESERVER_CONTENT_DIR",
		"link.ack_interval_ms":   "THESERVER_LINK_ACKINTERVALMS",
		"link.ack_batch":         "THESERVER_LINK_ACKBATCH",
		"link.ping_interval_s":   "THESERVER_LINK_PINGINTERVALS",
		"link.pong_timeout_s":    "THESERVER_LINK_PONGTIMEOUTS",
		"link.hello_timeout_s":   "THESERVER_LINK_HELLOTIMEOUTS",
		"link.install_timeout_s": "THESERVER_LINK_INSTALLTIMEOUTS",
		"log.level":              "THESERVER_LOG_LEVEL",
		"log.format":             "THESERVER_LOG_FORMAT",
		"admin.language":         "THESERVER_ADMIN_LANGUAGE",
		"admin.session_hours":    "THESERVER_ADMIN_SESSIONHOURS",
	}
	flags := map[string]string{}
	for _, s := range All() {
		if want, ok := wantEnv[s.Key]; ok && s.Env() != want {
			t.Errorf("%s reads %s, want %s", s.Key, s.Env(), want)
		}
		if s.Flag != "" {
			flags[s.Key] = s.Flag
		}
	}
	want := map[string]string{"server.listen_addr": "listen", "server.data_dir": "data-dir", "log.level": "log-level"}
	if !reflect.DeepEqual(flags, want) {
		t.Errorf("flags are %v, want %v", flags, want)
	}
}

func TestValidatePerKind(t *testing.T) {
	text := Setting{Key: "test.text", Section: "test", Kind: String}
	optional := Setting{Key: "test.optional", Section: "test", Kind: String, Empty: true}
	flag := Setting{Key: "test.flag", Section: "test", Kind: Bool}
	hours := Setting{Key: "test.hours", Section: "test", Kind: Duration, Unit: "h", Min: bound(1), Max: bound(720)}

	type check struct {
		name  string
		s     Setting
		value any
		want  any   // canonical value when accepted
		err   error // sentinel when refused
	}
	get := func(key string) Setting {
		s, ok := Get(key)
		if !ok {
			t.Fatalf("no setting %s", key)
		}
		return s
	}
	checks := []check{
		// String
		{"string keeps spaces", text, " a b ", " a b ", nil},
		{"string empty refused", text, "", nil, ErrEmpty},
		{"string empty allowed", optional, "", "", nil},
		{"string from a number", text, 5, nil, ErrBadType},

		// Path
		{"path", get("server.data_dir"), " /srv/theserver ", "/srv/theserver", nil},
		{"path empty refused", get("server.data_dir"), "", nil, ErrEmpty},
		{"path empty allowed", get("tls.cert_file"), "", "", nil},
		{"path from a bool", get("tls.cert_file"), true, nil, ErrBadType},

		// Addr
		{"addr any interface", get("server.listen_addr"), ":8443", ":8443", nil},
		{"addr loopback", get("server.listen_addr"), "127.0.0.1:9000", "127.0.0.1:9000", nil},
		{"addr ipv6", get("server.listen_addr"), "[::1]:8443", "[::1]:8443", nil},
		{"addr without port", get("server.listen_addr"), "localhost", nil, ErrBadAddress},
		{"addr port too large", get("server.listen_addr"), ":70000", nil, ErrBadAddress},
		{"addr port not a number", get("server.listen_addr"), "host:https", nil, ErrBadAddress},
		{"addr empty", get("server.listen_addr"), "", nil, ErrEmpty},
		{"addr from a number", get("server.listen_addr"), 8443, nil, ErrBadType},

		// Enum
		{"enum", get("log.level"), "debug", "debug", nil},
		{"enum trims", get("log.level"), " warn ", "warn", nil},
		{"enum unknown", get("log.level"), "loud", nil, ErrNotAllowed},
		{"enum case matters", get("log.level"), "INFO", nil, ErrNotAllowed},
		{"enum empty", get("log.format"), "", nil, ErrNotAllowed},

		// Int
		{"int from text", get("link.ack_batch"), " 64 ", int64(64), nil},
		{"int from json", get("link.ack_batch"), float64(16), int64(16), nil},
		{"int from json number", get("link.ack_batch"), json.Number("8"), int64(8), nil},
		{"int from int", get("link.ack_batch"), 1024, int64(1024), nil},
		{"int below range", get("link.ack_batch"), "0", nil, ErrOutOfRange},
		{"int above range", get("link.ack_batch"), 1025, nil, ErrOutOfRange},
		{"int not a number", get("link.ack_batch"), "many", nil, ErrBadNumber},
		{"int with a fraction", get("link.ack_batch"), 1.5, nil, ErrBadNumber},
		{"int from a duration", get("link.ack_batch"), "2s", nil, ErrBadNumber},
		{"int from a bool", get("link.ack_batch"), true, nil, ErrBadType},

		// Duration
		{"duration plain number", get("store.busy_timeout_ms"), "250", int64(250), nil},
		{"duration zero allowed", get("store.busy_timeout_ms"), 0, int64(0), nil},
		{"duration with unit", get("store.busy_timeout_ms"), "2s", int64(2000), nil},
		{"duration in its unit", get("link.ping_interval_s"), "30s", int64(30), nil},
		{"duration in minutes", get("link.ping_interval_s"), "2m", int64(120), nil},
		{"duration not whole in unit", get("link.ping_interval_s"), "1500ms", nil, ErrBadDuration},
		{"duration not a duration", get("store.busy_timeout_ms"), "soon", nil, ErrBadDuration},
		{"duration fraction", get("link.ack_interval_ms"), 2.5, nil, ErrBadDuration},
		{"duration negative", get("store.busy_timeout_ms"), "-1", nil, ErrOutOfRange},
		{"duration below range", get("link.pong_timeout_s"), "0s", nil, ErrOutOfRange},
		{"duration above range", get("link.ack_interval_ms"), "11s", nil, ErrOutOfRange},
		{"duration hours", hours, "48h", int64(48), nil},
		{"duration hours from days", hours, "721h", nil, ErrOutOfRange},

		// Bool
		{"bool", flag, true, true, nil},
		{"bool from text", flag, "false", false, nil},
		{"bool from one", flag, "1", true, nil},
		{"bool from garbage", flag, "maybe", nil, ErrBadBool},
		{"bool from a number", flag, 1, nil, ErrBadType},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.s.Parse(c.value)
			if c.err == nil {
				if err != nil {
					t.Fatalf("Parse(%#v): %v", c.value, err)
				}
				if !reflect.DeepEqual(got, c.want) {
					t.Errorf("Parse(%#v) = %#v, want %#v", c.value, got, c.want)
				}
				return
			}
			if !errors.Is(err, c.err) {
				t.Fatalf("Parse(%#v) = %#v, %v; want %v", c.value, got, err, c.err)
			}
			var ve *ValueError
			if !errors.As(err, &ve) || ve.Key != c.s.Key || ve.Code() == "" || Code(err) != ve.Code() {
				t.Errorf("error %v is not a typed ValueError for %s", err, c.s.Key)
			}
			if !strings.Contains(err.Error(), c.s.Key) {
				t.Errorf("message %q does not name the setting", err)
			}
		})
	}
}

func TestValidateByKey(t *testing.T) {
	if err := Validate("link.ack_batch", "8"); err != nil {
		t.Errorf("Validate: %v", err)
	}
	err := Validate("link.nothing", "8")
	if !errors.Is(err, ErrUnknownKey) || Code(err) != "unknown_setting" {
		t.Errorf("an unknown key gives %v", err)
	}
	if Code(errors.New("plain")) != "" {
		t.Error("Code names a plain error")
	}
}

func TestValueErrorMessages(t *testing.T) {
	cases := []struct {
		key   string
		value any
		code  string
		parts []string
	}{
		{"link.ack_batch", "0", "out_of_range", []string{"link.ack_batch", `"0"`, "between 1 and 1024"}},
		{"store.busy_timeout_ms", "-5", "out_of_range", []string{"between 0 and 600000 ms"}},
		{"log.level", "loud", "not_allowed", []string{`"loud"`, "debug, info, warn, error"}},
		{"server.listen_addr", "localhost", "bad_address", []string{`"localhost"`, ":8443"}},
		{"store.busy_timeout_ms", "soon", "bad_duration", []string{"is not a whole number of ms", "2ms"}},
		{"link.ack_batch", "many", "bad_number", []string{"is not a whole number"}},
		{"server.data_dir", "", "empty", []string{"must not be empty"}},
		{"server.listen_addr", 8443, "bad_type", []string{"8443", "wrong type"}},
	}
	for _, c := range cases {
		err := Validate(c.key, c.value)
		if Code(err) != c.code {
			t.Errorf("%s = %#v gives code %q, want %q (%v)", c.key, c.value, Code(err), c.code, err)
			continue
		}
		for _, part := range c.parts {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("%q lacks %q", err, part)
			}
		}
	}
}

func TestKindNames(t *testing.T) {
	want := map[Kind]string{String: "string", Int: "int", Bool: "bool", Duration: "duration", Enum: "enum", Path: "path", Addr: "addr"}
	for kind, name := range want {
		if kind.String() != name {
			t.Errorf("%d is named %q, want %q", kind, kind.String(), name)
		}
		text, _ := kind.MarshalText()
		if string(text) != name {
			t.Errorf("%d marshals as %q", kind, text)
		}
	}
	if Kind(99).String() != "kind(99)" {
		t.Errorf("an unknown kind is named %q", Kind(99).String())
	}
}

func TestDurationOf(t *testing.T) {
	s, _ := Get("link.ping_interval_s")
	if got := s.DurationOf(15); got.Seconds() != 15 {
		t.Errorf("15 s is %v", got)
	}
}
