package editor_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/editor"
	"github.com/cyb3rgun/theserver/internal/i18n"
	"github.com/cyb3rgun/theserver/pkg/scenario"
)

// Every field of the editor names a place in the scenario model, so the
// registry cannot drift away from the manifest it edits (D-046).
func TestEveryFieldLivesInTheManifest(t *testing.T) {
	manifest := reflect.TypeFor[scenario.Manifest]()
	for _, f := range editor.Fields() {
		if !resolves(manifest, f.Key) {
			t.Errorf("the field %s is not a field of the scenario model", f.Key)
		}
	}
}

// resolves walks a json path of the manifest, where [#] means one of a list.
func resolves(t reflect.Type, path string) bool {
	for _, part := range strings.Split(path, ".") {
		name, isList := strings.CutSuffix(part, "[#]")
		t = deref(t)
		if t.Kind() != reflect.Struct {
			return false
		}
		field, ok := fieldByJSON(t, name)
		if !ok {
			return false
		}
		t = deref(field.Type)
		if isList {
			if t.Kind() != reflect.Slice {
				return false
			}
			t = deref(t.Elem())
		}
	}
	return true
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func fieldByJSON(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); tag == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// Every field carries its three texts in every language of the catalogues,
// and says how it is shown (D-046).
func TestEveryFieldHasTextsAndAControl(t *testing.T) {
	controls := []string{editor.ControlText, editor.ControlTexts, editor.ControlNumber,
		editor.ControlSwitch, editor.ControlSelect, editor.ControlZones, editor.ControlStates,
		editor.ControlFixed}
	froms := []string{editor.FromStates, editor.FromClips, editor.FromSounds, editor.FromOverlays,
		editor.FromAppearances, editor.FromThen}
	seen := map[string]bool{}
	for _, f := range editor.Fields() {
		if seen[f.Key] {
			t.Errorf("the field %s is listed twice", f.Key)
		}
		seen[f.Key] = true
		if !slices.Contains(editor.Groups(), f.Group) {
			t.Errorf("%s is in the unknown group %q", f.Key, f.Group)
		}
		if !slices.Contains(controls, f.Control) {
			t.Errorf("%s has the unknown control %q", f.Key, f.Control)
		}
		if f.Control == editor.ControlSelect && (len(f.Enum) == 0) == (f.From == "") {
			t.Errorf("%s is a select with %d fixed values and the list %q; it takes one of the two", f.Key, len(f.Enum), f.From)
		}
		if f.From != "" && !slices.Contains(froms, f.From) {
			t.Errorf("%s takes its values from the unknown list %q", f.Key, f.From)
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			t.Errorf("%s has a lowest value above its highest", f.Key)
		}
		if f.ID() == "" || strings.ContainsAny(f.ID(), ".[]# ") {
			t.Errorf("%s has the id %q", f.Key, f.ID())
		}
		for _, lang := range i18n.Languages() {
			texts := f.Text[lang]
			switch {
			case texts.Label == "" || texts.Description == "" || texts.Why == "":
				t.Errorf("%s has no complete texts in %s: %+v", f.Key, lang, texts)
			case len(texts.Label) > 48:
				t.Errorf("%s has a label of %d characters in %s", f.Key, len(texts.Label), lang)
			case texts.Description == texts.Why:
				t.Errorf("%s says the same twice in %s", f.Key, lang)
			case strings.TrimSpace(texts.Label) != texts.Label:
				t.Errorf("%s has a label with spaces around it in %s", f.Key, lang)
			}
		}
		if f.In("fr").Label != f.In("en").Label {
			t.Errorf("%s falls back to something else than English", f.Key)
		}
	}
}

// The groups the panel shows hold every field, and each holds something.
func TestGroupsHoldEveryField(t *testing.T) {
	count := 0
	for _, group := range editor.Groups() {
		fields := editor.Of(group)
		if len(fields) == 0 {
			t.Errorf("the group %s has no field", group)
		}
		count += len(fields)
	}
	if count != len(editor.Fields()) {
		t.Errorf("the groups hold %d of %d fields", count, len(editor.Fields()))
	}

	f, ok := editor.Get("zone[#].zone_class")
	if !ok || f.Group != editor.GroupZone || !slices.Equal(f.Enum, scenario.ZoneClasses()) {
		t.Errorf("the zone class field reads %+v (%v)", f, ok)
	}
	if f.Name() != "zone_class" || f.ID() != "zone-zone_class" {
		t.Errorf("the zone class field is named %q with the id %q", f.Name(), f.ID())
	}
	if _, ok := editor.Get("zone[#].nothing"); ok {
		t.Error("a field that is not there was found")
	}
}
