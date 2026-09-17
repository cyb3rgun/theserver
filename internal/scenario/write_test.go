package scenario_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/scenario"
	"github.com/cyb3rgun/theserver/internal/scenario/scenariotest"
)

// The manifest the server writes for a published draft (D-042) reads back as
// the same model, with the same hash, for every valid fixture.
func TestWriteManifestReadsBack(t *testing.T) {
	for _, name := range []string{scenariotest.Video, scenariotest.Interactive, scenariotest.Layered} {
		want := load(t, name).Manifest
		data, err := scenario.WriteManifest(want)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(string(data), "# manifest.toml") {
			t.Errorf("%s: the manifest starts with %q", name, strings.SplitN(string(data), "\n", 2)[0])
		}
		got, err := scenario.ParseManifest(data)
		if err != nil {
			t.Fatalf("%s: the written manifest does not parse: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s reads back as another manifest:\n%+v\n%+v", name, got, want)
		}
		if scenario.Hash(got) != scenario.Hash(want) {
			t.Errorf("%s: the hash changed by writing", name)
		}
		if problems := scenario.Validate(got, filesOf(t, name)); len(problems) != 0 {
			t.Errorf("%s: the written manifest has problems: %v", name, problems)
		}
	}

	// A manifest with nothing set is still written and read back.
	empty := scenario.Manifest{}
	data, err := scenario.WriteManifest(empty)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scenario.ParseManifest(data)
	if err != nil {
		t.Fatalf("an empty manifest does not parse: %v", err)
	}
	if !reflect.DeepEqual(got, empty) {
		t.Errorf("an empty manifest reads back as %+v", got)
	}
}

func filesOf(t *testing.T, name string) map[string]string {
	t.Helper()
	return load(t, name).Files
}
