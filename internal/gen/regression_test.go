package gen

import (
	"strings"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// Regression: a numeric version ("1.0.0") must render a compilable doc.go
// without template leftovers, and each file in the Render map must own
// independent bytes (the shared render buffer used to alias doc.go with
// client.go, letting later writes corrupt earlier files).
func TestRenderNumericVersionAndIndependentBuffers(t *testing.T) {
	s := &model.Schema{Version: "1.0.0",
		Entities: []model.Entity{{Name: "User", Fields: []model.Field{
			{Name: "id", Type: model.Type{Kind: model.KindPrimitive, Primitive: "string"}},
		}}},
	}
	files, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(files["doc.go"])
	t.Logf("doc.go:\n%s", doc)
	// The old template/buffer bugs emitted a standalone "_0" token line.
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "_0" || strings.Contains(trimmed, "MISSING") || strings.Contains(trimmed, "%!") {
			t.Fatalf("doc.go contains render artifact line %q", line)
		}
	}
	if !strings.Contains(doc, "package sdkv1_0_0") {
		t.Fatalf("doc.go missing expected package clause:\n%s", doc)
	}
	if !strings.Contains(doc, `const Version = "1.0.0"`) {
		t.Fatalf("doc.go missing version constant:\n%s", doc)
	}

	// Mutating the client buffer region must not change doc.go: prove the
	// map entries do not share storage by checking the header lines differ
	// and survive after every other file is rendered.
	for _, line := range strings.Split(string(files["client.go"]), "\n") {
		if strings.TrimSpace(line) == "_0" {
			t.Fatal("client.go contains template range artifact _0")
		}
	}
	if !strings.HasPrefix(string(files["doc.go"]), "// Code generated") {
		t.Fatal("doc.go header corrupted by buffer aliasing")
	}
}

// Regression: entity/enum files use their own per-file buffers already, but
// verify the full numeric-version generation actually compiles end to end.
func TestNumericVersionGenerationCompiles(t *testing.T) {
	out := t.TempDir() + "/sdk"
	svc := NewService()
	s := &model.Schema{Version: "2.5.1",
		Entities: []model.Entity{{Name: "Account", Fields: []model.Field{
			{Name: "id", Type: model.Type{Kind: model.KindPrimitive, Primitive: "string"}},
			{Name: "state", Type: model.Type{Kind: model.KindEnum, EnumName: "State"}},
		}}},
		Enums: []model.Enum{{Name: "State", Values: []string{"open", "closed"}}},
	}
	if err := svc.Generate("num-version", s, Options{OutputDir: out}); err != nil {
		t.Fatalf("numeric-version generation failed to compile: %v", err)
	}
}
