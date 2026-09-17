// Package i18n translates the admin interface of theserver (D-031).
//
// The catalogues catalog/en.toml and catalog/de.toml are embedded. Their
// tables are read as flat keys: [admin.devices] with title = "Devices" is the
// key admin.devices.title. T looks a key up in a language, falls back to
// English, and renders a missing key as the key itself, logging a warning
// once. A test fails when a key exists in one catalogue and not the other.
package i18n

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"sync"

	"github.com/BurntSushi/toml"
)

// Fallback is the language every key exists in and the last resort of
// Resolve.
const Fallback = "en"

// CookieName is the cookie that holds the language an admin chose.
const CookieName = "theserver_lang"

var languages = []string{"en", "de"}

//go:embed catalog/*.toml
var catalogFS embed.FS

var catalogs = mustLoad(catalogFS)

// Languages lists the languages of the admin interface, English first.
func Languages() []string {
	return slices.Clone(languages)
}

// Supported reports whether lang is one of Languages.
func Supported(lang string) bool {
	return slices.Contains(languages, lang)
}

// Resolve picks the language of a request: the language in the cookie when
// it is supported, else def when it is supported, else English.
func Resolve(cookie, def string) string {
	switch {
	case Supported(cookie):
		return cookie
	case Supported(def):
		return def
	default:
		return Fallback
	}
}

func mustLoad(fsys fs.FS) map[string]map[string]string {
	loaded, err := load(fsys)
	if err != nil {
		panic(err)
	}
	return loaded
}

func load(fsys fs.FS) (map[string]map[string]string, error) {
	loaded := map[string]map[string]string{}
	for _, lang := range languages {
		data, err := fs.ReadFile(fsys, "catalog/"+lang+".toml")
		if err != nil {
			return nil, fmt.Errorf("i18n: %w", err)
		}
		var tree map[string]any
		if err := toml.Unmarshal(data, &tree); err != nil {
			return nil, fmt.Errorf("i18n: catalogue %s: %w", lang, err)
		}
		flat := map[string]string{}
		if err := flatten("", tree, flat); err != nil {
			return nil, fmt.Errorf("i18n: catalogue %s: %w", lang, err)
		}
		loaded[lang] = flat
	}
	return loaded, nil
}

func flatten(prefix string, tree map[string]any, into map[string]string) error {
	for key, value := range tree {
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		switch v := value.(type) {
		case string:
			into[name] = v
		case map[string]any:
			if err := flatten(name, v, into); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s is a %T, want text", name, value)
		}
	}
	return nil
}

// T returns the text of key in lang, formatted with args as fmt.Sprintf
// does when args are given. A key missing in lang comes from English; a key
// missing in English too renders as the key itself. Both are logged once.
func T(lang, key string, args ...any) string {
	text, ok := catalogs[lang][key]
	if !ok {
		reportMissing(lang, key)
		text, ok = catalogs[Fallback][key]
		if !ok {
			return key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(text, args...)
	}
	return text
}

// Has reports whether key exists in the catalogue of lang.
func Has(lang, key string) bool {
	_, ok := catalogs[lang][key]
	return ok
}

// Keys returns every key of the catalogue of lang, sorted.
func Keys(lang string) []string {
	keys := slices.Collect(maps.Keys(catalogs[lang]))
	sort.Strings(keys)
	return keys
}

var reported sync.Map

func reportMissing(lang, key string) {
	if _, seen := reported.LoadOrStore(lang+"|"+key, true); seen {
		return
	}
	slog.Default().Warn("i18n text missing", "component", "i18n", "lang", lang, "key", key)
}
