package normalize

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// logInput prints the raw input and every categorized finding so test logs
// document both what was fed in and why it was accepted or rejected.
func logInput(t *testing.T, name string, data string, err error) {
	t.Helper()
	t.Logf("input %s:\n%s", name, data)
	if err == nil {
		t.Logf("verdict for %s: accepted", name)
		return
	}
	var issues Issues
	if errors.As(err, &issues) {
		t.Logf("verdict for %s: %d categorized issue(s):", name, len(issues))
		for _, is := range issues {
			t.Logf("  - [%s] at %q: %s", is.Code, is.Path, is.Message)
		}
	} else {
		t.Logf("verdict for %s: non-issue error: %v", name, err)
	}
}

func mustNormalize(t *testing.T, src Source) *model.Schema {
	t.Helper()
	s, err := Normalize(src)
	if err != nil {
		t.Fatalf("Normalize(%s) returned unexpected error: %v", src.Name, err)
	}
	return s
}

// expectIssue asserts that normalization fails with at least one issue of the
// given code, and no issue is silently dropped (the issue carries a path).
func expectIssue(t *testing.T, src Source, code IssueCode) {
	t.Helper()
	_, err := Normalize(src)
	logInput(t, src.Name, string(src.Data), err)
	if err == nil {
		t.Fatalf("Normalize(%s): expected error with %s, got nil", src.Name, code)
	}
	var issues Issues
	if !errors.As(err, &issues) {
		t.Fatalf("Normalize(%s): error %v is not categorized Issues", src.Name, err)
	}
	if !issues.Has(code) {
		t.Fatalf("Normalize(%s): wanted issue %s, got %v", src.Name, code, issues)
	}
}

func TestDialectsNormalizeToSameModel(t *testing.T) {
	simple := `{
	  "version": "v1",
	  "entities": [{"name": "User", "fields": [
	    {"name": "id", "type": "string"},
	    {"name": "age", "type": "integer", "optional": true},
	    {"name": "role", "type": {"kind": "enum", "name": "Role"}},
	    {"name": "tags", "type": "string[]"}
	  ]}],
	  "enums": [{"name": "Role", "values": ["admin", "member"]}]
	}`
	openapi := `{
	  "openapi": "3.0.3",
	  "info": {"version": "v1"},
	  "components": {"schemas": {
	    "Role": {"type": "string", "enum": ["admin", "member"]},
	    "User": {"type": "object", "required": ["id", "role", "tags"], "properties": {
	      "id": {"type": "string"},
	      "age": {"type": "integer"},
	      "role": {"$ref": "#/components/schemas/Role"},
	      "tags": {"type": "array", "items": {"type": "string"}}
	    }}
	  }}
	}`

	gotSimple := mustNormalize(t, Source{Name: "simple.json", Data: []byte(simple)})
	gotOpenAPI := mustNormalize(t, Source{Name: "openapi.json", Data: []byte(openapi)})
	logInput(t, "simple.json", simple, nil)
	logInput(t, "openapi.json", openapi, nil)

	want := &model.Schema{
		Version: "v1",
		Entities: []model.Entity{{
			Name: "User",
			Fields: []model.Field{
				{Name: "id", Type: model.Type{Kind: model.KindPrimitive, Primitive: "string"}},
				{Name: "age", Optional: true, Type: model.Type{Kind: model.KindPrimitive, Primitive: "int"}},
				{Name: "role", Type: model.Type{Kind: model.KindEnum, EnumName: "Role"}},
				{Name: "tags", Type: model.Type{Kind: model.KindArray,
					Elem: &model.Type{Kind: model.KindPrimitive, Primitive: "string"}}},
			},
		}},
		Enums: []model.Enum{{Name: "Role", Values: []string{"admin", "member"}}},
	}
	if !reflect.DeepEqual(gotSimple, want) {
		t.Fatalf("simple dialect mismatch:\n got %#v\nwant %#v", gotSimple, want)
	}
	// OpenAPI sorts properties alphabetically, so fields come out reordered.
	if gotOpenAPI.Version != want.Version {
		t.Fatalf("openapi version = %q, want %q", gotOpenAPI.Version, want.Version)
	}
	ou := gotOpenAPI.Entities[0]
	byName := map[string]model.Field{}
	for _, f := range ou.Fields {
		byName[f.Name] = f
	}
	if name := gotOpenAPI.Entities[0].Name; name != "User" {
		t.Fatalf("entity name = %q, want User", name)
	}
	if !reflect.DeepEqual(byName["id"], want.Entities[0].Fields[0]) {
		t.Fatalf("openapi id field mismatch: %#v", byName["id"])
	}
	if age := byName["age"]; !age.Optional || age.Type.Primitive != "int" {
		t.Fatalf("openapi age field mismatch: %#v", age)
	}
	if role := byName["role"]; role.Type.Kind != model.KindEnum || role.Type.EnumName != "Role" {
		t.Fatalf("openapi role field mismatch: %#v", role)
	}
	if tags := byName["tags"]; tags.Type.Kind != model.KindArray || tags.Type.Elem == nil || tags.Type.Elem.Primitive != "string" {
		t.Fatalf("openapi tags field mismatch: %#v", tags)
	}
}

func TestMalformedSchemasAreCategorized(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		code IssueCode
	}{
		{"not json", "{oops", IssueInvalidSyntax},
		{"root is array", `[1,2]`, IssueInvalidSyntax},
		{"unknown dialect", `{"hello": 1}`, IssueUnsupported},
		{"missing version", `{"entities": []}`, IssueMissingVersion},
		{"entities wrong shape", `{"version":"v1","entities": {}}`, IssueInvalidSyntax},
		{"entity missing name", `{"version":"v1","entities":[{"fields":[]}]}`, IssueMissingName},
		{"entity invalid name", `{"version":"v1","entities":[{"name":"1bad","fields":[]}]}`, IssueInvalidName},
		{"duplicate entity", `{"version":"v1","entities":[{"name":"U","fields":[]},{"name":"U","fields":[]}]}`, IssueNameConflict},
		{"enum entity clash", `{"version":"v1","entities":[{"name":"U","fields":[]}],"enums":[{"name":"U","values":["A"]}]}`, IssueNameConflict},
		{"field missing name", `{"version":"v1","entities":[{"name":"U","fields":[{}]}]}`, IssueMissingName},
		{"field missing type", `{"version":"v1","entities":[{"name":"U","fields":[{"name":"f"}]}]}`, IssueMissingType},
		{"field invalid type", `{"version":"v1","entities":[{"name":"U","fields":[{"name":"f","type":"datetime"}]}]}`, IssueInvalidType},
		{"unknown ref", `{"version":"v1","entities":[{"name":"U","fields":[{"name":"f","type":"Ghost"}]}]}`, IssueUnknownRef},
		{"array missing items", `{"version":"v1","entities":[{"name":"U","fields":[{"name":"f","type":{"kind":"array"}}]}]}`, IssueMissingType},
		{"enum duplicate value", `{"version":"v1","enums":[{"name":"E","values":["A","A"]}]}`, IssueNameConflict},
		{"enum value not identifier", `{"version":"v1","enums":[{"name":"E","values":["a b"]}]}`, IssueInvalidName},
		{"openapi unsupported component", `{"info":{"version":"v1"},"components":{"schemas":{"X":{"type":"file"}}}}`, IssueUnsupported},
		{"openapi bad ref", `{"info":{"version":"v1"},"components":{"schemas":{
		   "U":{"type":"object","properties":{"f":{"$ref":"#/elsewhere/X"}}}}}}`, IssueInvalidType},
		{"openapi dangling ref", `{"info":{"version":"v1"},"components":{"schemas":{
		   "U":{"type":"object","properties":{"f":{"$ref":"#/components/schemas/X"}}}}}}`, IssueUnknownRef},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectIssue(t, Source{Name: tc.name + ".json", Data: []byte(tc.doc)}, tc.code)
		})
	}
}

func TestIssuesAreSortedAndDedupedAndNothingDropped(t *testing.T) {
	// Two distinct malformed fields: both must show up, ordered by path.
	doc := `{"version":"v1","entities":[{"name":"U","fields":[
	  {"name":"b","type":"nope"},
	  {"name":"a","type":"also-nope"}
	]}]}`
	_, err := Normalize(Source{Name: "two-bad-fields.json", Data: []byte(doc)})
	logInput(t, "two-bad-fields.json", doc, err)
	var issues Issues
	if !errors.As(err, &issues) {
		t.Fatalf("expected Issues error, got %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d: %v", len(issues), issues)
	}
	if !strings.Contains(issues[0].Path, "fields[0]") || !strings.Contains(issues[1].Path, "fields[1]") {
		t.Fatalf("issues not ordered by path index: %v", issues)
	}
	if !strings.Contains(issues[0].Message, `"nope"`) || !strings.Contains(issues[1].Message, `"also-nope"`) {
		t.Fatalf("issues not aligned with their paths: %v", issues)
	}
	for _, is := range issues {
		if is.Path == "" || is.Message == "" {
			t.Fatalf("issue missing path/message: %#v", is)
		}
	}
}

func TestFormatDetectionAndExplicitFormat(t *testing.T) {
	doc := `{"version":"v1","entities":[]}`
	if s := mustNormalize(t, Source{Name: "auto", Data: []byte(doc)}); s.Version != "v1" {
		t.Fatalf("auto detect simple failed: %#v", s)
	}
	if s := mustNormalize(t, Source{Format: FormatSimple, Name: "explicit", Data: []byte(doc)}); s.Version != "v1" {
		t.Fatalf("explicit simple failed: %#v", s)
	}
	expectIssue(t, Source{Format: FormatOpenAPI, Name: "wrong-hint", Data: []byte(doc)}, IssueMissingVersion)
}
