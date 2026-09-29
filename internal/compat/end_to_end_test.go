package compat_test

import (
	"errors"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/compat"
	"github.com/lisama569/versioned-sdk-codegen-go/internal/normalize"
)

// TestCrossDialectEndToEnd normalizes two different dialects and checks
// compatibility between them, proving the pipeline treats dialect as an
// implementation detail of the unified model.
func TestCrossDialectEndToEnd(t *testing.T) {
	oldDoc := []byte(`{
	  "version": "v1",
	  "entities": [{"name": "Account", "fields": [
	    {"name": "id", "type": "string"},
	    {"name": "tier", "type": {"kind": "enum", "name": "Tier"}},
	    {"name": "balance", "type": "float"}
	  ]}],
	  "enums": [{"name": "Tier", "values": ["free", "pro"]}]
	}`)
	newDoc := []byte(`{
	  "openapi": "3.0.3",
	  "info": {"version": "v2"},
	  "components": {"schemas": {
	    "Tier": {"type": "string", "enum": ["free", "pro", "enterprise"]},
	    "Account": {"type": "object", "required": ["id", "tier", "credit"], "properties": {
	      "id": {"type": "string"},
	      "tier": {"$ref": "#/components/schemas/Tier"},
	      "balance": {"type": "integer"},
	      "credit": {"type": "integer"}
	    }}
	  }}
	}`)

	oldS, err := normalize.Normalize(normalize.Source{Name: "v1.simple.json", Data: oldDoc})
	if err != nil {
		t.Fatalf("old schema invalid: %v", err)
	}
	newS, err := normalize.Normalize(normalize.Source{Name: "v2.openapi.json", Data: newDoc})
	if err != nil {
		t.Fatalf("new schema invalid: %v", err)
	}
	t.Logf("normalized old (simple) version=%q entities=%d enums=%d", oldS.Version, len(oldS.Entities), len(oldS.Enums))
	t.Logf("normalized new (openapi) version=%q entities=%d enums=%d", newS.Version, len(newS.Entities), len(newS.Enums))

	r := compat.Check(oldS, newS)
	t.Logf("cross-dialect verdict compatible=%v:", r.Compatible)
	for _, c := range r.Changes {
		t.Logf("  [%s] %s @ %s: %s", c.Severity, c.Kind, c.Path, c.Message)
	}
	if r.Compatible {
		t.Fatal("expected breaking: balance changed type float->int and required field credit was added")
	}

	byKind := map[string]compat.Change{}
	for _, c := range r.Changes {
		byKind[c.Kind] = c
	}
	if c, ok := byKind[compat.KindFieldTypeChanged]; !ok || c.Severity != compat.SeverityBreaking {
		t.Fatalf("balance type change not marked breaking: %#v", byKind)
	}
	if c, ok := byKind[compat.KindFieldAddedRequired]; !ok || c.Severity != compat.SeverityBreaking {
		t.Fatalf("required credit addition not marked breaking: %#v", byKind)
	}
	if c, ok := byKind[compat.KindEnumValueAdded]; !ok || c.Severity != compat.SeverityCompatible {
		t.Fatalf("enum value addition should be compatible: %#v", byKind)
	}
}

func TestMalformedSchemaIsRejectedBeforeCompat(t *testing.T) {
	_, err := normalize.Normalize(normalize.Source{Name: "bad.json", Data: []byte(`{"version":"v1","entities":[{"fields":[]}]}`)})
	if err == nil {
		t.Fatal("expected normalization error")
	}
	var issues normalize.Issues
	if !errors.As(err, &issues) || !issues.Has(normalize.IssueMissingName) {
		t.Fatalf("error not categorized as missing_name: %v", err)
	}
	t.Logf("malformed schema rejected before compat with categorized issues: %v", err)
}
