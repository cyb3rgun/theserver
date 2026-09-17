package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cyb3rgun/theserver/internal/settings"
	"github.com/cyb3rgun/theserver/internal/store"
)

// cli runs theserver in-process with its own data directory.
type cli struct {
	t   *testing.T
	dir string
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	// Keep the environment from pointing the commands elsewhere.
	for _, key := range []string{"THESERVER_SERVER_DATADIR", "THESERVER_TLS_CERTFILE", "THESERVER_TLS_KEYFILE"} {
		t.Setenv(key, "")
	}
	return &cli{t: t, dir: filepath.Join(t.TempDir(), "data")}
}

func (c *cli) run(args ...string) (code int, stdout, stderr string) {
	c.t.Helper()
	var out, errOut bytes.Buffer
	withDir := append(append([]string{}, args...), "--data-dir", c.dir)
	code = run(withDir, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (c *cli) store() *store.Store {
	c.t.Helper()
	st, err := store.Open(context.Background(), c.dir)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { st.Close() })
	return st
}

func tokenFrom(t *testing.T, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if token, ok := strings.CutPrefix(line, "token: "); ok {
			return strings.TrimSpace(token)
		}
	}
	t.Fatalf("no token line in %q", stdout)
	return ""
}

func TestDeviceAddListRevoke(t *testing.T) {
	c := newCLI(t)

	code, out, errOut := c.run("device", "add", "--id", "tgt-01", "--kind", "target", "--class", "esp")
	if code != 0 {
		t.Fatalf("device add exited %d: %s", code, errOut)
	}
	token := tokenFrom(t, out)
	if !strings.Contains(out, "shown once") {
		t.Errorf("device add printed no warning about the token: %q", out)
	}

	st := c.store()
	device, err := st.DeviceByToken(context.Background(), token)
	if err != nil {
		t.Fatalf("the printed token does not find the device: %v", err)
	}
	if device.ID != "tgt-01" || device.Status != store.StatusApproved || device.Kind != store.KindTarget {
		t.Errorf("stored %+v", device)
	}
	if strings.Contains(string(device.TokenHash), token) {
		t.Error("the token itself is stored")
	}

	if code, _, errOut := c.run("device", "add", "--id", "tgt-01", "--kind", "target"); code != 1 || !strings.Contains(errOut, "already exists") {
		t.Errorf("adding tgt-01 again exited %d: %s", code, errOut)
	}

	code, out, _ = c.run("device", "list")
	if code != 0 {
		t.Fatalf("device list exited %d", code)
	}
	for _, want := range []string{"ID", "KIND", "CLASS", "STATUS", "MIN AGE", "LAST SEEN", "tgt-01", "target", "esp", "approved", "never"} {
		if !strings.Contains(out, want) {
			t.Errorf("device list lacks %q:\n%s", want, out)
		}
	}

	code, out, errOut = c.run("device", "revoke", "--id", "tgt-01")
	if code != 0 {
		t.Fatalf("device revoke exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "revoked") {
		t.Errorf("device revoke printed %q", out)
	}
	device, err = st.GetDevice(context.Background(), "tgt-01")
	if err != nil {
		t.Fatal(err)
	}
	if device.Status != store.StatusBlocked {
		t.Errorf("status after revoke is %s, want blocked", device.Status)
	}

	if code, _, _ := c.run("device", "revoke", "--id", "nobody"); code != 1 {
		t.Errorf("revoking an unknown device exited %d, want 1", code)
	}
}

func TestDeviceAddValidates(t *testing.T) {
	c := newCLI(t)
	tests := [][]string{
		{"device", "add", "--kind", "target"},
		{"device", "add", "--id", "x", "--kind", "drone"},
		{"device", "add", "--id", "x", "--kind", "target", "--class", "quantum"},
		{"device", "add", "--id", "x", "--kind", "target", "--min-age", "14"},
		{"device", "add", "--id", "x", "--kind", "target", "extra"},
		{"device", "revoke"},
		{"device"},
		{"device", "rename"},
		{"db"},
		{"db", "drop"},
		{"launch"},
	}
	for _, args := range tests {
		if code, _, _ := c.run(args...); code != 2 {
			t.Errorf("%v exited %d, want 2", args, code)
		}
	}
}

func TestDBInfoAndDeprecatedFlag(t *testing.T) {
	c := newCLI(t)

	code, out, errOut := c.run("db", "info")
	if code != 0 {
		t.Fatalf("db info exited %d: %s", code, errOut)
	}
	for _, want := range []string{"schema version: ", "journal mode:   wal", "foreign keys:   on", "devices", "events"} {
		if !strings.Contains(out, want) {
			t.Errorf("db info lacks %q:\n%s", want, out)
		}
	}

	code, legacy, errOut := c.run("--db-info")
	if code != 0 {
		t.Fatalf("--db-info exited %d: %s", code, errOut)
	}
	if legacy != out {
		t.Errorf("--db-info printed\n%s\nwant the same as db info\n%s", legacy, out)
	}
	if !strings.Contains(errOut, "deprecated") {
		t.Errorf("--db-info gave no deprecation note: %q", errOut)
	}
}

func TestVersionAndHelp(t *testing.T) {
	c := newCLI(t)
	for _, args := range [][]string{{"--version"}, {"serve", "--version"}} {
		code, out, _ := c.run(args...)
		if code != 0 || !strings.HasPrefix(out, "theserver ") || !strings.Contains(out, "go:") {
			t.Errorf("%v exited %d with %q", args, code, out)
		}
	}
	var out bytes.Buffer
	if code := run([]string{"help"}, &out, &out); code != 0 || !strings.Contains(out.String(), "device add") {
		t.Errorf("help exited %d with %q", code, out.String())
	}
}

func TestAdminTokenCommands(t *testing.T) {
	c := newCLI(t)

	code, out, errOut := c.run("admin", "token", "add", "--name", "founder")
	if code != 0 {
		t.Fatalf("admin token add exited %d: %s", code, errOut)
	}
	token := tokenFrom(t, out)
	if !strings.Contains(out, "shown once") || !strings.Contains(out, "/admin/login") {
		t.Errorf("admin token add printed %q", out)
	}
	st := c.store()
	row, err := st.VerifyAdminToken(context.Background(), token)
	if err != nil || row.Name != "founder" {
		t.Fatalf("the printed token verifies as %+v, %v", row, err)
	}
	if !strings.Contains(out, row.ID) {
		t.Errorf("admin token add did not print the id %s", row.ID)
	}

	code, out, _ = c.run("admin", "token", "list")
	if code != 0 {
		t.Fatalf("admin token list exited %d", code)
	}
	for _, want := range []string{"ID", "NAME", "LAST USED", "REVOKED", row.ID, "founder", "no"} {
		if !strings.Contains(out, want) {
			t.Errorf("admin token list lacks %q:%s", want, out)
		}
	}
	if strings.Contains(out, token) {
		t.Error("admin token list shows the token")
	}

	code, out, errOut = c.run("admin", "token", "revoke", row.ID)
	if code != 0 || !strings.Contains(out, "revoked") {
		t.Fatalf("admin token revoke exited %d: %s %s", code, out, errOut)
	}
	if _, err := st.VerifyAdminToken(context.Background(), token); err == nil {
		t.Error("the revoked token still verifies")
	}
	if code, _, _ := c.run("admin", "token", "revoke", "adm-nothing"); code != 1 {
		t.Errorf("revoking an unknown token exited %d, want 1", code)
	}

	for _, args := range [][]string{
		{"admin"},
		{"admin", "token"},
		{"admin", "token", "burn"},
		{"admin", "user", "add"},
		{"admin", "token", "add"},
		{"admin", "token", "revoke"},
		{"admin", "token", "revoke", "a", "b"},
		{"admin", "token", "revoke", "a", "--id", "b"},
	} {
		if code, _, _ := c.run(args...); code != 2 {
			t.Errorf("%v exited %d, want 2", args, code)
		}
	}
}

func TestDeviceReset(t *testing.T) {
	c := newCLI(t)
	if code, _, errOut := c.run("device", "add", "--id", "tgt-01", "--kind", "target"); code != 0 {
		t.Fatalf("device add: %s", errOut)
	}

	// The id may come before or after the flags, or as --id.
	code, out, errOut := c.run("device", "reset", "tgt-01")
	if code != 0 || !strings.Contains(out, "epoch 2") {
		t.Fatalf("device reset exited %d: %s %s", code, out, errOut)
	}
	code, out, _ = c.run("device", "reset", "--id", "tgt-01")
	if code != 0 || !strings.Contains(out, "epoch 3") {
		t.Errorf("device reset --id exited %d: %s", code, out)
	}
	code, out, _ = c.run("device", "reset", "tgt-01", "--id", "tgt-01")
	if code != 0 || !strings.Contains(out, "epoch 4") {
		t.Errorf("device reset with the same id twice exited %d: %s", code, out)
	}

	device, err := c.store().GetDevice(context.Background(), "tgt-01")
	if err != nil || device.SeqEpoch != 4 {
		t.Errorf("the device is in epoch %d, %v; want 4", device.SeqEpoch, err)
	}

	_, list, _ := c.run("device", "list")
	if !strings.Contains(list, "EPOCH") || !strings.Contains(list, "  4  ") {
		t.Errorf("device list does not show the epoch:%s", list)
	}

	if code, _, _ := c.run("device", "reset", "nobody"); code != 1 {
		t.Errorf("resetting an unknown device exited %d, want 1", code)
	}
	for _, args := range [][]string{
		{"device", "reset"},
		{"device", "reset", "a", "b"},
		{"device", "reset", "a", "--id", "b"},
	} {
		if code, _, _ := c.run(args...); code != 2 {
			t.Errorf("%v exited %d, want 2", args, code)
		}
	}
}

// Without --config every command reads theserver.toml in the data directory,
// the file the first saved change of a server without --config creates.
func TestCommandsReadTheFileInTheDataDirectory(t *testing.T) {
	c := newCLI(t)
	if code, _, errOut := c.run("db", "info"); code != 0 {
		t.Fatalf("db info exited %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(c.dir, "theserver.toml")); err == nil {
		t.Fatal("a command created the configuration file")
	}
	content := `[store]
busy_timeout_ms = 1234
`
	if err := os.WriteFile(filepath.Join(c.dir, "theserver.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := c.run("db", "info")
	if code != 0 || !strings.Contains(out, "busy timeout:   1234 ms") {
		t.Errorf("db info exited %d without the file's busy timeout:\n%s%s", code, out, errOut)
	}
	named := filepath.Join(t.TempDir(), "named.toml")
	if err := os.WriteFile(named, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = c.run("db", "info", "--config", named)
	if code != 0 || !strings.Contains(out, "busy timeout:   5000 ms") {
		t.Errorf("with --config the file in the data directory still counts: %d\n%s%s", code, out, errOut)
	}
}

// docs/settings.md is what theserver settings doc writes; a changed registry
// without a new reference fails here. Refresh it with:
// go run ./cmd/theserver settings doc --out docs/settings.md
func TestSettingsReferenceIsCurrent(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"settings", "doc"}, &out, &errOut); code != 0 {
		t.Fatalf("settings doc exited %d: %s", code, errOut.String())
	}
	committed, err := os.ReadFile(filepath.Join("..", "..", "docs", "settings.md"))
	if err != nil {
		t.Fatalf("read docs/settings.md: %v", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n")), out.Bytes()) {
		t.Error("docs/settings.md is stale; run: go run ./cmd/theserver settings doc --out docs/settings.md")
	}
	for _, s := range settings.All() {
		if !strings.Contains(out.String(), "| `"+s.Key+"` | "+s.TextIn("en").Label+" |") {
			t.Errorf("the reference has no row for %s", s.Key)
		}
	}

	path := filepath.Join(t.TempDir(), "settings.md")
	out.Reset()
	if code := run([]string{"settings", "doc", "--out", path}, &out, &errOut); code != 0 {
		t.Fatalf("settings doc --out exited %d: %s", code, errOut.String())
	}
	written, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(written, settings.Markdown()) {
		t.Errorf("--out wrote %d bytes, %v", len(written), err)
	}
	for _, args := range [][]string{{"settings"}, {"settings", "list"}, {"settings", "doc", "extra"}} {
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("%v exited %d, want 2", args, code)
		}
	}
}

// A device is set for 18 unless --min-age says otherwise (D-039).
func TestDeviceAddSetsTheAge(t *testing.T) {
	c := newCLI(t)
	code, out, errOut := c.run("device", "add", "--id", "tgt-kids", "--kind", "target", "--min-age", "12")
	if code != 0 || !strings.Contains(out, "min age 12") {
		t.Fatalf("device add exited %d: %s%s", code, out, errOut)
	}
	if code, out, _ := c.run("device", "add", "--id", "tgt-adults", "--kind", "target"); code != 0 || !strings.Contains(out, "min age 18") {
		t.Errorf("a device without --min-age: %s", out)
	}
	st := c.store()
	for id, want := range map[string]int{"tgt-kids": 12, "tgt-adults": 18} {
		if d, err := st.GetDevice(context.Background(), id); err != nil || d.MinAge != want {
			t.Errorf("%s is set for %d, want %d (%v)", id, d.MinAge, want, err)
		}
	}
}
