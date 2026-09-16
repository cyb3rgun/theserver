package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

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
	for _, want := range []string{"ID", "KIND", "CLASS", "STATUS", "LAST SEEN", "tgt-01", "target", "esp", "approved", "never"} {
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
