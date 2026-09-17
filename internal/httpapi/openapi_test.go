package httpapi

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
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

// The OpenAPI description as this repository writes it. The decoder refuses
// fields that are not here (D-047), so text that runs into the next key of a
// flow mapping, an unquoted comma in a description for instance, fails the
// test instead of disappearing from the file unnoticed.
type openAPIDoc struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title       string `yaml:"title"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	} `yaml:"info"`
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Security   []map[string][]string                  `yaml:"security"`
	Paths      map[string]map[string]openAPIOperation `yaml:"paths"`
	Components struct {
		SecuritySchemes map[string]struct {
			Type        string `yaml:"type"`
			Scheme      string `yaml:"scheme"`
			Description string `yaml:"description"`
		} `yaml:"securitySchemes"`
		Parameters map[string]openAPIParameter `yaml:"parameters"`
		Responses  map[string]openAPIResponse  `yaml:"responses"`
		Schemas    map[string]openAPISchema    `yaml:"schemas"`
	} `yaml:"components"`
}

type openAPIOperation struct {
	Summary     string `yaml:"summary"`
	Description string `yaml:"description"`
	OperationID string `yaml:"operationId"`
	// Security is a pointer so that an operation without it can be told
	// from one that opens itself with an empty list.
	Security    *[]map[string][]string     `yaml:"security"`
	Parameters  []openAPIParameter         `yaml:"parameters"`
	RequestBody *openAPIBody               `yaml:"requestBody"`
	Responses   map[string]openAPIResponse `yaml:"responses"`
}

type openAPIParameter struct {
	Ref         string         `yaml:"$ref"`
	Name        string         `yaml:"name"`
	In          string         `yaml:"in"`
	Required    bool           `yaml:"required"`
	Description string         `yaml:"description"`
	Schema      *openAPISchema `yaml:"schema"`
}

type openAPIBody struct {
	Description string                  `yaml:"description"`
	Required    bool                    `yaml:"required"`
	Content     map[string]openAPIMedia `yaml:"content"`
}

type openAPIResponse struct {
	Ref         string                      `yaml:"$ref"`
	Description string                      `yaml:"description"`
	Headers     map[string]openAPIParameter `yaml:"headers"`
	Content     map[string]openAPIMedia     `yaml:"content"`
}

type openAPIMedia struct {
	Schema *openAPISchema `yaml:"schema"`
}

type openAPISchema struct {
	Ref                  string                   `yaml:"$ref"`
	Type                 any                      `yaml:"type"`
	Format               string                   `yaml:"format"`
	Title                string                   `yaml:"title"`
	Description          string                   `yaml:"description"`
	Enum                 []any                    `yaml:"enum"`
	Const                any                      `yaml:"const"`
	Default              any                      `yaml:"default"`
	Example              any                      `yaml:"example"`
	Examples             []any                    `yaml:"examples"`
	Pattern              string                   `yaml:"pattern"`
	Minimum              *float64                 `yaml:"minimum"`
	Maximum              *float64                 `yaml:"maximum"`
	MinLength            *int                     `yaml:"minLength"`
	MaxLength            *int                     `yaml:"maxLength"`
	MinItems             *int                     `yaml:"minItems"`
	Nullable             bool                     `yaml:"nullable"`
	ReadOnly             bool                     `yaml:"readOnly"`
	Required             []string                 `yaml:"required"`
	Properties           map[string]openAPISchema `yaml:"properties"`
	Items                *openAPISchema           `yaml:"items"`
	AdditionalProperties *openAPIAdditional       `yaml:"additionalProperties"`
	AllOf                []openAPISchema          `yaml:"allOf"`
	AnyOf                []openAPISchema          `yaml:"anyOf"`
	OneOf                []openAPISchema          `yaml:"oneOf"`
}

// openAPIAdditional is additionalProperties: false for an object that takes
// nothing else, else the schema of the other properties.
type openAPIAdditional struct {
	Open   bool
	Schema *openAPISchema
}

// UnmarshalYAML takes both forms. A node read here does not inherit
// KnownFields from the decoder, so the keys of the schema are checked
// against the fields of openAPISchema by hand; a mapping nested deeper
// inside this one is not.
func (a *openAPIAdditional) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return value.Decode(&a.Open)
	}
	var keys map[string]yaml.Node
	if err := value.Decode(&keys); err != nil {
		return err
	}
	fields := reflect.TypeFor[openAPISchema]()
	for name := range keys {
		known := false
		for i := range fields.NumField() {
			tag, _, _ := strings.Cut(fields.Field(i).Tag.Get("yaml"), ",")
			known = known || tag == name
		}
		if !known {
			return fmt.Errorf("line %d: field %s not found in type httpapi.openAPISchema", value.Line, name)
		}
	}
	a.Schema = &openAPISchema{}
	return value.Decode(a.Schema)
}

// refs collects every reference below a schema, so the test can check that
// each one points at a component that is there.
func (s *openAPISchema) refs(out *[]string) {
	if s == nil {
		return
	}
	if s.Ref != "" {
		*out = append(*out, s.Ref)
	}
	for _, key := range slices.Sorted(maps.Keys(s.Properties)) {
		p := s.Properties[key]
		p.refs(out)
	}
	s.Items.refs(out)
	if s.AdditionalProperties != nil {
		s.AdditionalProperties.Schema.refs(out)
	}
	for _, group := range [][]openAPISchema{s.AllOf, s.AnyOf, s.OneOf} {
		for _, sub := range group {
			sub.refs(out)
		}
	}
}

func parseOpenAPI(t *testing.T) *openAPIDoc {
	t.Helper()
	var doc openAPIDoc
	dec := yaml.NewDecoder(bytes.NewReader(OpenAPI))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("openapi.yaml does not read as the OpenAPI shape:\n%v", err)
	}
	return &doc
}

// TestOpenAPIParses reads the description with a YAML parser (D-047): it is
// well formed YAML, it holds nothing the shape above does not know, every
// reference points at a component that exists, and every component is used.
func TestOpenAPIParses(t *testing.T) {
	doc := parseOpenAPI(t)
	if !strings.HasPrefix(doc.OpenAPI, "3.1") || doc.Info.Title == "" || doc.Info.Version == "" ||
		len(doc.Info.Description) < 100 || len(doc.Servers) != 1 || len(doc.Security) != 1 {
		t.Errorf("the head of openapi.yaml reads %+v", doc.Info)
	}

	var refs []string
	seenIDs := map[string]bool{}
	methods := []string{"get", "put", "post", "delete", "patch", "head", "options"}
	for _, path := range slices.Sorted(maps.Keys(doc.Paths)) {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("the path %q does not start with a slash", path)
		}
		for _, method := range slices.Sorted(maps.Keys(doc.Paths[path])) {
			op := doc.Paths[path][method]
			where := strings.ToUpper(method) + " " + path
			if !slices.Contains(methods, method) {
				t.Errorf("%s: %q is not an HTTP method", path, method)
			}
			if op.Summary == "" || op.OperationID == "" {
				t.Errorf("%s has no summary or no operationId", where)
			}
			if seenIDs[op.OperationID] {
				t.Errorf("%s: the operationId %s is used twice", where, op.OperationID)
			}
			seenIDs[op.OperationID] = true
			if len(op.Responses) == 0 {
				t.Errorf("%s documents no response", where)
			}
			for _, p := range op.Parameters {
				collectParameter(p, &refs)
			}
			if b := op.RequestBody; b != nil {
				collectContent(b.Content, &refs)
			}
			for _, status := range slices.Sorted(maps.Keys(op.Responses)) {
				r := op.Responses[status]
				if r.Ref == "" && r.Description == "" {
					t.Errorf("%s: the response %s has no description", where, status)
				}
				collectResponse(r, &refs)
			}
		}
	}

	for _, name := range slices.Sorted(maps.Keys(doc.Components.Parameters)) {
		collectParameter(doc.Components.Parameters[name], &refs)
	}
	for _, name := range slices.Sorted(maps.Keys(doc.Components.Responses)) {
		collectResponse(doc.Components.Responses[name], &refs)
	}
	for _, name := range slices.Sorted(maps.Keys(doc.Components.Schemas)) {
		s := doc.Components.Schemas[name]
		s.refs(&refs)
	}

	used := map[string]bool{}
	for _, ref := range refs {
		used[ref] = true
		rest, isComponent := strings.CutPrefix(ref, "#/components/")
		part, name, ok := strings.Cut(rest, "/")
		if !isComponent || !ok {
			t.Errorf("the reference %q does not name a component", ref)
			continue
		}
		var there bool
		switch part {
		case "parameters":
			_, there = doc.Components.Parameters[name]
		case "responses":
			_, there = doc.Components.Responses[name]
		case "schemas":
			_, there = doc.Components.Schemas[name]
		default:
			t.Errorf("the reference %q names no part of components", ref)
			continue
		}
		if !there {
			t.Errorf("the reference %q points at a component that is not there", ref)
		}
	}
	for part, names := range map[string][]string{
		"parameters": slices.Sorted(maps.Keys(doc.Components.Parameters)),
		"responses":  slices.Sorted(maps.Keys(doc.Components.Responses)),
		"schemas":    slices.Sorted(maps.Keys(doc.Components.Schemas)),
	} {
		for _, name := range names {
			if !used["#/components/"+part+"/"+name] {
				t.Errorf("the component %s/%s is in the file but nothing points at it", part, name)
			}
		}
	}
	for name := range doc.Components.SecuritySchemes {
		if name != "adminToken" && name != "deviceToken" {
			t.Errorf("the security scheme %s is not one of the two the server has", name)
		}
	}
}

func collectParameter(p openAPIParameter, out *[]string) {
	if p.Ref != "" {
		*out = append(*out, p.Ref)
	}
	p.Schema.refs(out)
}

func collectResponse(r openAPIResponse, out *[]string) {
	if r.Ref != "" {
		*out = append(*out, r.Ref)
	}
	for _, name := range slices.Sorted(maps.Keys(r.Headers)) {
		collectParameter(r.Headers[name], out)
	}
	collectContent(r.Content, out)
}

func collectContent(content map[string]openAPIMedia, out *[]string) {
	for _, kind := range slices.Sorted(maps.Keys(content)) {
		content[kind].Schema.refs(out)
	}
}

// TestOpenAPICoversEveryRoute checks both directions: every registered route is
// documented with its method, and every documented operation is registered.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	doc := parseOpenAPI(t)
	documented := map[string]bool{}
	for path, item := range doc.Paths {
		for method := range item {
			documented[strings.ToUpper(method)+" "+path] = true
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
	doc := parseOpenAPI(t)
	var open []string
	for path, item := range doc.Paths {
		for method, op := range item {
			if op.Security != nil && len(*op.Security) == 0 {
				open = append(open, strings.ToUpper(method)+" "+path)
			}
		}
	}
	if len(open) != 1 || open[0] != "GET /openapi.yaml" {
		t.Errorf("the operations without security are %v", open)
	}
	for _, r := range Routes() {
		if !r.Auth && r.Path != "/openapi.yaml" {
			t.Errorf("route %s %s is open but not documented as such", r.Method, r.Path)
		}
	}
}
