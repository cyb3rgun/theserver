package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/cyb3rgun/theserver/internal/settings"
)

const fileHeader = `# theserver configuration
#
# Written by theserver from its settings registry (D-032). Every setting is
# listed with its label, description, default and range. A setting shown
# commented out is not set here and uses its default. Precedence, highest
# first: command line flags, environment variables, this file, defaults.
# The admin page writes this file; comments added by hand are not kept.
`

const foreignHeader = `
# The tables below are not theserver settings. theserver keeps their values
# as it found them.
`

// Write regenerates the configuration file at path from the registry (D-032):
// values holds the settings the file sets, keyed section.key, in any form
// settings.Parse accepts; every other setting is written commented out with
// its default. Tables of an existing file that are not theserver settings
// are kept. The file is replaced in one step, so a reader never sees half of
// it.
func Write(path string, values map[string]any) error {
	parsed := map[string]any{}
	var errs []error
	for key, value := range values {
		v, err := settings.Parse(key, value)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		parsed[key] = v
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	foreign := map[string]any{}
	if _, err := os.Stat(path); err == nil {
		existing, err := foreignTables(path)
		if err != nil {
			return err
		}
		foreign = existing
	}
	content, err := render(parsed, foreign)
	if err != nil {
		return err
	}
	return replaceFile(path, content)
}

// WriteDefault writes a configuration file at path in which every setting is
// set to its built in default. Missing parent directories are created. An
// existing file is never overwritten.
func WriteDefault(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config file %s already exists, not overwriting it", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	values := map[string]any{}
	for _, s := range settings.All() {
		values[s.Key] = s.Default
	}
	return Write(path, values)
}

// foreignTables returns the tables of the file at path that are not
// theserver settings. It does not check the settings themselves.
func foreignTables(path string) (map[string]any, error) {
	var tree map[string]any
	if _, err := toml.DecodeFile(path, &tree); err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	foreign := map[string]any{}
	for name, value := range tree {
		if table, ok := value.(map[string]any); ok && !slices.Contains(settings.Sections(), name) {
			foreign[name] = table
		}
	}
	return foreign, nil
}

func render(values, foreign map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(fileHeader)
	section := ""
	for _, s := range settings.All() {
		if s.Section != section {
			fmt.Fprintf(&buf, "\n[%s]\n", s.Section)
			section = s.Section
		}
		text := s.TextIn(settings.FallbackLanguage)
		fmt.Fprintf(&buf, "# %s. %s\n", text.Label, text.Description)
		defaultLine, err := encodeValue(s.Name(), s.Default)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "# %s\n", facts(s, defaultLine))
		value, set := values[s.Key]
		if !set {
			fmt.Fprintf(&buf, "# %s\n", defaultLine)
			continue
		}
		line, err := encodeValue(s.Name(), value)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "%s\n", line)
	}
	if len(foreign) > 0 {
		buf.WriteString(foreignHeader)
		names := make([]string, 0, len(foreign))
		for name := range foreign {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			buf.WriteString("\n")
			if err := toml.NewEncoder(&buf).Encode(map[string]any{name: foreign[name]}); err != nil {
				return nil, fmt.Errorf("config: table %s: %w", name, err)
			}
		}
	}
	return buf.Bytes(), nil
}

// facts is the second comment line of a setting: default, unit, range or
// values, the environment variable and flag, and the restart note.
func facts(s settings.Setting, defaultLine string) string {
	unit := ""
	if s.Unit != "" {
		unit = " " + s.Unit
	}
	parts := []string{"Default " + strings.TrimSpace(strings.SplitN(defaultLine, "=", 2)[1]) + unit}
	switch {
	case s.Min != nil && s.Max != nil:
		parts = append(parts, fmt.Sprintf("from %d to %d%s", *s.Min, *s.Max, unit))
	case len(s.Enum) > 0:
		parts = append(parts, "one of "+strings.Join(s.Enum, ", "))
	}
	override := "environment " + s.Env()
	if s.Flag != "" {
		override += ", flag --" + s.Flag
	}
	parts = append(parts, override)
	line := strings.Join(parts, "; ") + "."
	if s.Restart {
		line += " Changing it needs a restart."
	}
	return line
}

// encodeValue writes one TOML line, key = value.
func encodeValue(name string, value any) (string, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any{name: value}); err != nil {
		return "", fmt.Errorf("config: encode %s: %w", name, err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// replaceFile writes content to a temporary file beside path and renames it
// over path.
func replaceFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
