package httpapi

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestOpenAPICopyMatchesDocs keeps the embedded description equal to
// docs/openapi.yaml, the one people read and edit.
func TestOpenAPICopyMatchesDocs(t *testing.T) {
	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(docs, OpenAPI) {
		t.Fatal("internal/httpapi/openapi.yaml differs from docs/openapi.yaml; copy the docs file over")
	}
}

// TestOpenAPICoversEveryRoute checks both directions: every registered route is
// documented with its method, and every documented operation is registered.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	pathLine := regexp.MustCompile(`^  (/[^:]*):\s*$`)
	methodLine := regexp.MustCompile(`^    (get|put|post|delete|patch|head|options):\s*$`)

	documented := map[string]bool{}
	inPaths, current := false, ""
	for _, line := range strings.Split(string(OpenAPI), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "paths:":
			inPaths = true
		case inPaths && line != "" && !strings.HasPrefix(line, " "):
			inPaths = false
		case inPaths && pathLine.MatchString(line):
			current = pathLine.FindStringSubmatch(line)[1]
		case inPaths && current != "" && methodLine.MatchString(line):
			method := strings.ToUpper(methodLine.FindStringSubmatch(line)[1])
			documented[method+" "+current] = true
		}
	}
	if len(documented) == 0 {
		t.Fatal("no operation found in openapi.yaml")
	}

	var registered []string
	for _, r := range Routes() {
		key := r.Method + " " + r.Path
		registered = append(registered, key)
		if !documented[key] {
			t.Errorf("route %s is not documented in openapi.yaml", key)
		}
	}
	for key := range documented {
		if !slices.Contains(registered, key) {
			t.Errorf("openapi.yaml documents %s, which is not a route", key)
		}
	}
	if len(documented) != len(registered) {
		t.Errorf("openapi.yaml documents %d operations, the router has %d", len(documented), len(registered))
	}
}

// TestOpenAPIMarksTheOpenRoute checks that only the description itself is
// documented without security.
func TestOpenAPIMarksTheOpenRoute(t *testing.T) {
	if strings.Count(string(OpenAPI), "security: []") != 1 {
		t.Error("openapi.yaml should mark exactly one operation as open")
	}
	for _, r := range Routes() {
		if !r.Auth && r.Path != "/openapi.yaml" {
			t.Errorf("route %s %s is open but not documented as such", r.Method, r.Path)
		}
	}
}
