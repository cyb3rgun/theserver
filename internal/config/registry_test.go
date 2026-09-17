package config

import (
	"reflect"
	"testing"

	registry "github.com/cyb3rgun/theserver/internal/settings"
)

// configFields maps every leaf of Config, written section.key as the toml
// tags name it, to the field.
func configFields(c *Config) map[string]reflect.Value {
	fields := map[string]reflect.Value{}
	root := reflect.ValueOf(c).Elem()
	for i := range root.NumField() {
		section := root.Type().Field(i).Tag.Get("toml")
		leaves := root.Field(i)
		for j := range leaves.NumField() {
			fields[section+"."+leaves.Type().Field(j).Tag.Get("toml")] = leaves.Field(j)
		}
	}
	return fields
}

// TestConfigMatchesRegistry keeps Config and the settings registry in step
// (D-030): every field has exactly one registry entry of a fitting kind,
// every entry has a field, and the defaults agree.
func TestConfigMatchesRegistry(t *testing.T) {
	def := Default()
	fields := configFields(&def)

	goKind := map[registry.Kind]reflect.Kind{
		registry.String:   reflect.String,
		registry.Path:     reflect.String,
		registry.Addr:     reflect.String,
		registry.Enum:     reflect.String,
		registry.Int:      reflect.Int,
		registry.Duration: reflect.Int,
		registry.Bool:     reflect.Bool,
	}
	registered := map[string]bool{}
	for _, s := range registry.All() {
		registered[s.Key] = true
		field, ok := fields[s.Key]
		if !ok {
			t.Errorf("registry setting %s has no field in Config", s.Key)
			continue
		}
		if field.Kind() != goKind[s.Kind] {
			t.Errorf("%s is a %s in the registry and a %s in Config", s.Key, s.Kind, field.Kind())
			continue
		}
		var got any
		switch field.Kind() {
		case reflect.String:
			got = field.String()
		case reflect.Int:
			got = field.Int()
		case reflect.Bool:
			got = field.Bool()
		}
		if !reflect.DeepEqual(got, s.Default) {
			t.Errorf("%s defaults to %#v in Config and %#v in the registry", s.Key, got, s.Default)
		}
	}
	for key := range fields {
		if !registered[key] {
			t.Errorf("Config field %s has no registry setting", key)
		}
	}
}
