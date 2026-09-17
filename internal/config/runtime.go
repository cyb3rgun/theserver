package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"slices"
	"sync"

	"github.com/cyb3rgun/theserver/internal/settings"
)

// Runtime is the configuration of a running server (D-032): the values in
// effect, where they came from, the file they are written back to, and the
// changes that wait for a restart. It is safe for concurrent use.
type Runtime struct {
	mu      sync.Mutex
	current Config
	sources Sources
	target  string         // the file changes are written to
	file    map[string]any // the settings the file sets, canonical
	pending map[string]any // restart settings whose next value differs
	hooks   []func(Config)
	log     *slog.Logger
}

// NewRuntime starts a Runtime from the configuration a server was started
// with. With a file in use, the file is read to know which settings it sets.
// Without one, the first change creates FileName in the data directory, and
// logger names it; nil logs to slog.Default().
func NewRuntime(cfg Config, sources Sources, logger *slog.Logger) (*Runtime, error) {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Runtime{
		current: cfg,
		sources: sources.clone(),
		target:  sources.File,
		file:    map[string]any{},
		pending: map[string]any{},
		log:     logger,
	}
	if r.sources.Keys == nil {
		r.sources.Keys = map[string]Source{}
	}
	if sources.File != "" {
		layer, _, err := readFile(sources.File)
		if err != nil {
			return nil, err
		}
		r.file = layer
		return r, nil
	}
	if cfg.Server.DataDir == "" {
		return nil, errors.New("config: a runtime without a configuration file needs a data directory")
	}
	r.target = DataDirFile(cfg.Server.DataDir)
	return r, nil
}

// Config returns the values in effect.
func (r *Runtime) Config() Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// Sources returns where the values in effect came from.
func (r *Runtime) Sources() Sources {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sources.clone()
}

// File is the configuration file changes are written to: the file in use,
// or for a server started without one, the file the first change creates.
func (r *Runtime) File() string {
	return r.target
}

// FileExists reports whether the server uses File, which is false until the
// first change creates it.
func (r *Runtime) FileExists() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sources.File != ""
}

// OnChange registers fn, which is called with the values in effect after a
// change that applies at once.
func (r *Runtime) OnChange(fn func(Config)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, fn)
}

// RestartPending lists, in registry order, the settings whose new value
// takes effect after the next start.
func (r *Runtime) RestartPending() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var keys []string
	for _, s := range settings.All() {
		if _, ok := r.pending[s.Key]; ok {
			keys = append(keys, s.Key)
		}
	}
	return keys
}

// Pending returns the value a restart will bring for key, if one waits.
func (r *Runtime) Pending(key string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.pending[key]
	return value, ok
}

// A Change is one setting an Apply or Reset changed.
type Change struct {
	Key string
	// Old is the value configured before, New the value configured now.
	Old any
	New any
	// Restart is set when the new value takes effect after a restart.
	Restart bool
}

// ErrFileAppeared refuses the change that would create the configuration
// file when a file of that name appeared after the start: writing it would
// drop the settings it holds, which the server never read.
var ErrFileAppeared = errors.New("a configuration file appeared after the start")

// ErrOverridden refuses a change to a setting that an environment variable
// or a flag sets; an *OverrideError wraps it.
var ErrOverridden = errors.New("set by an environment variable or a flag")

// OverrideError names what sets the setting instead of the file.
type OverrideError struct {
	Key    string
	Source Source // SourceEnv or SourceFlag
	Name   string // the variable, or the flag with its dashes
}

func (e *OverrideError) Error() string {
	return fmt.Sprintf("%s is set by %s %s and cannot be changed here", e.Key, sourceWord(e.Source), e.Name)
}

func (e *OverrideError) Unwrap() error {
	return ErrOverridden
}

func sourceWord(s Source) string {
	if s == SourceFlag {
		return "the flag"
	}
	return "the environment variable"
}

// Apply validates changes through the registry, writes them to the
// configuration file, applies the settings that allow it at once, and lists
// the ones that need a restart (D-032). Either every change is taken or none.
func (r *Runtime) Apply(changes map[string]any) (needsRestart, applied []string, err error) {
	done, err := r.Change(changes)
	if err != nil {
		return nil, nil, err
	}
	return r.split(done)
}

// Reset sets keys back to their defaults, as Apply does.
func (r *Runtime) Reset(keys []string) (needsRestart, applied []string, err error) {
	done, err := r.ResetKeys(keys)
	if err != nil {
		return nil, nil, err
	}
	return r.split(done)
}

func (r *Runtime) split(done []Change) (needsRestart, applied []string, err error) {
	pending := r.RestartPending()
	for _, c := range done {
		switch {
		case !c.Restart:
			applied = append(applied, c.Key)
		case slices.Contains(pending, c.Key):
			needsRestart = append(needsRestart, c.Key)
		}
	}
	return needsRestart, applied, nil
}

// Change is Apply that returns every change with its old and new value, in
// registry order. A value equal to the one configured is no change.
func (r *Runtime) Change(values map[string]any) ([]Change, error) {
	return r.update(values, nil)
}

// ResetKeys is Reset that returns every change.
func (r *Runtime) ResetKeys(keys []string) ([]Change, error) {
	return r.update(nil, keys)
}

func (r *Runtime) update(values map[string]any, reset []string) ([]Change, error) {
	r.mu.Lock()
	next := map[string]any{}
	var errs []error
	check := func(key string) (settings.Setting, bool) {
		s, ok := settings.Get(key)
		if !ok {
			errs = append(errs, &settings.ValueError{Key: key, Err: settings.ErrUnknownKey})
			return s, false
		}
		switch source := r.sources.Of(key); source {
		case SourceEnv:
			errs = append(errs, &OverrideError{Key: key, Source: source, Name: s.Env()})
			return s, false
		case SourceFlag:
			errs = append(errs, &OverrideError{Key: key, Source: source, Name: "--" + s.Flag})
			return s, false
		}
		return s, true
	}
	for key, raw := range values {
		s, ok := check(key)
		if !ok {
			continue
		}
		value, err := s.Parse(raw)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		next[key] = value
	}
	for _, key := range reset {
		if s, ok := check(key); ok {
			next[key] = s.Default
		}
	}
	if len(errs) > 0 {
		r.mu.Unlock()
		return nil, errors.Join(errs...)
	}

	// The configuration the next start reads must hold together.
	upcoming := r.configuredLocked()
	for key, value := range next {
		upcoming.set(key, value)
	}
	if err := upcoming.Validate(); err != nil {
		r.mu.Unlock()
		return nil, err
	}

	file := maps.Clone(r.file)
	for key := range values {
		file[key] = next[key]
	}
	for _, key := range reset {
		delete(file, key)
	}
	created := r.sources.File == ""
	if created {
		if _, err := os.Stat(r.target); !errors.Is(err, fs.ErrNotExist) {
			r.mu.Unlock()
			if err == nil {
				return nil, fmt.Errorf("%w: %s; restart theserver to read it", ErrFileAppeared, r.target)
			}
			return nil, err
		}
	}
	if err := Write(r.target, file); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.file = file
	if created {
		r.sources.File = r.target
		r.log.Info("configuration file created", "path", r.target)
	}

	var done []Change
	live := false
	for _, s := range settings.All() {
		value, ok := next[s.Key]
		if !ok {
			continue
		}
		old := r.configuredValueLocked(s.Key)
		if _, inFile := file[s.Key]; inFile {
			if !s.Restart {
				r.sources.Keys[s.Key] = SourceFile
			}
		} else if !s.Restart {
			r.sources.Keys[s.Key] = SourceDefault
		}
		if reflect.DeepEqual(old, value) {
			continue
		}
		done = append(done, Change{Key: s.Key, Old: old, New: value, Restart: s.Restart})
		if s.Restart {
			if reflect.DeepEqual(value, r.current.Get(s.Key)) {
				delete(r.pending, s.Key)
			} else {
				r.pending[s.Key] = value
			}
			continue
		}
		r.current.set(s.Key, value)
		live = true
	}
	cfg := r.current
	hooks := append([]func(Config){}, r.hooks...)
	r.mu.Unlock()

	if live {
		for _, hook := range hooks {
			hook(cfg)
		}
	}
	return done, nil
}

// configuredLocked is the configuration with every pending value applied.
func (r *Runtime) configuredLocked() Config {
	c := r.current
	for key, value := range r.pending {
		c.set(key, value)
	}
	return c
}

// configuredValueLocked is the value key has now or after the next start.
func (r *Runtime) configuredValueLocked(key string) any {
	if value, ok := r.pending[key]; ok {
		return value
	}
	return r.current.Get(key)
}
