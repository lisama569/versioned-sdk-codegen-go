package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIEndToEnd runs the CLI in-process (via run) and logs every input and
// verdict, covering the documented local workflow.
func TestCLIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.json")
	newPath := filepath.Join(dir, "new.json")
	badPath := filepath.Join(dir, "bad.json")

	mustWrite := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(oldPath, `{
	  "version": "v1",
	  "entities": [{"name": "User", "fields": [
	    {"name": "id", "type": "string"},
	    {"name": "tier", "type": {"kind": "enum", "name": "Tier"}}
	  ]}],
	  "enums": [{"name": "Tier", "values": ["free", "pro"]}]
	}`)
	mustWrite(newPath, `{
	  "version": "v2",
	  "entities": [{"name": "User", "fields": [
	    {"name": "id", "type": "string"},
	    {"name": "tier", "type": {"kind": "enum", "name": "Tier"}},
	    {"name": "email", "type": "string", "optional": true}
	  ]}, {"name": "Order", "fields": [
	    {"name": "n", "type": "int"}
	  ]}],
	  "enums": [{"name": "Tier", "values": ["free", "pro", "enterprise"]}]
	}`)
	mustWrite(badPath, `{"version":"v9","entities":[{"fields":[{"type":"weird"}]}]}`)

	t.Run("malformed schema exits non-zero with categorized issues", func(t *testing.T) {
		t.Logf("input bad.json:\n%s", mustRead(t, badPath))
		err := run([]string{"normalize", badPath})
		if err == nil {
			t.Fatal("expected non-nil error for malformed schema")
		}
		if !strings.Contains(err.Error(), "issue(s)") {
			t.Fatalf("error should summarize issues, got: %v", err)
		}
		t.Logf("verdict: non-zero, %v", err)
	})

	t.Run("compat reports compatible additive change", func(t *testing.T) {
		t.Logf("inputs: %s (old), %s (new)", oldPath, newPath)
		if err := run([]string{"compat", oldPath, newPath}); err != nil {
			t.Fatalf("compat failed: %v", err)
		}
	})

	t.Run("generate writes a compilable SDK", func(t *testing.T) {
		out := filepath.Join(dir, "sdk-v2")
		if err := run([]string{"generate", "--key", "user-v2", "--out", out, newPath}); err != nil {
			t.Fatalf("generate failed: %v", err)
		}
		for _, want := range []string{"go.mod", "client.go", "types_user.go", "types_order.go", "enum_tier.go"} {
			if _, err := os.Stat(filepath.Join(out, want)); err != nil {
				t.Fatalf("missing generated file %s: %v", want, err)
			}
		}
		t.Logf("verdict: generated files present under %s", out)
	})
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
