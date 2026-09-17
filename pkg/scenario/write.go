package scenario

import (
	"bytes"
	"fmt"

	"github.com/BurntSushi/toml"
)

// writtenHeader is the first line of a manifest the server writes.
const writtenHeader = "# manifest.toml, the scenario model version %d (docs/scenario.md).\n" +
	"# Written by theserver from an editor draft; every file is listed with its SHA-256.\n\n"

// WriteManifest writes m as the manifest.toml of a package (D-042).
// ParseManifest reads back what it writes: the same model, with the same
// hash, which a test keeps true for every fixture.
func WriteManifest(m Manifest) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, writtenHeader, ModelVersion)
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("write the manifest of %q: %w", m.Scenario.ID, err)
	}
	return buf.Bytes(), nil
}
