package compat

import (
	"reflect"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

func prim(p string) model.Type    { return model.Type{Kind: model.KindPrimitive, Primitive: p} }
func arr(e model.Type) model.Type { return model.Type{Kind: model.KindArray, Elem: &e} }

// logReport prints both input versions and every change with the evidence
// behind it, fulfilling the "log inputs and verdict basis" requirement.
func logReport(t *testing.T, label string, oldS, newS *model.Schema, r *Report) {
	t.Helper()
	t.Logf("compat input old=%q new=%q (%s)", oldS.Version, newS.Version, label)
	t.Logf("verdict: compatible=%v, %d change(s)", r.Compatible, len(r.Changes))
	for _, c := range r.Changes {
		t.Logf("  [%s] %s @ %s -- %s", c.Severity, c.Kind, c.Path, c.Message)
	}
}

func kindCount(r *Report) map[string]Severity {
	m := map[string]Severity{}
	for _, c := range r.Changes {
		m[c.Kind] = c.Severity
	}
	return m
}

func TestBreakingChangesAreMarked(t *testing.T) {
	oldS := &model.Schema{Version: "v1",
		Entities: []model.Entity{{Name: "User", Fields: []model.Field{
			{Name: "id", Type: prim("string")},
			{Name: "age", Type: prim("int")},
			{Name: "role", Type: model.Type{Kind: model.KindEnum, EnumName: "Role"}},
			{Name: "oldOpt", Optional: true, Type: prim("string")},
			{Name: "gone", Type: prim("string")}, // removed field
		}}, {Name: "Dropped", Fields: []model.Field{{Name: "x", Type: prim("string")}}}},
		Enums: []model.Enum{{Name: "Role", Values: []string{"admin", "member"}}},
	}
	newS := &model.Schema{Version: "v2",
		Entities: []model.Entity{{Name: "User", Fields: []model.Field{
			{Name: "id", Type: prim("int")},       // type changed
			{Name: "age", Type: arr(prim("int"))}, // primitive -> array
			{Name: "role", Type: model.Type{Kind: model.KindEnum, EnumName: "Role"}},
			{Name: "oldOpt", Optional: false, Type: prim("string")}, // optional -> required
			{Name: "newReq", Optional: false, Type: prim("string")}, // newly required
		}}},
		Enums: []model.Enum{{Name: "Role", Values: []string{"member"}}}, // admin removed
	}

	r := Check(oldS, newS)
	logReport(t, "all breaking kinds", oldS, newS, r)
	if r.Compatible {
		t.Fatal("expected incompatible verdict")
	}
	want := map[string]Severity{
		KindFieldRemoved:            SeverityBreaking,
		KindEntityRemoved:           SeverityBreaking,
		KindFieldTypeChanged:        SeverityBreaking,
		KindFieldOptionalityTighter: SeverityBreaking,
		KindFieldAddedRequired:      SeverityBreaking,
		KindEnumValueRemoved:        SeverityBreaking,
	}
	got := kindCount(r)
	for k, sev := range want {
		gs, ok := got[k]
		if !ok {
			t.Errorf("missing change kind %s", k)
			continue
		}
		if gs != sev {
			t.Errorf("kind %s severity = %s, want %s", k, gs, sev)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected extra changes: %#v", got)
	}
}

func TestCompatibleChangesPass(t *testing.T) {
	oldS := &model.Schema{Version: "v1",
		Entities: []model.Entity{{Name: "User", Fields: []model.Field{
			{Name: "id", Type: prim("string")},
			{Name: "oldReq", Type: prim("string")},
		}}},
		Enums: []model.Enum{{Name: "Role", Values: []string{"admin"}}},
	}
	newS := &model.Schema{Version: "v2",
		Entities: []model.Entity{{Name: "User", Fields: []model.Field{
			{Name: "id", Type: prim("string")},
			{Name: "oldReq", Optional: true, Type: prim("string")}, // required -> optional
			{Name: "nick", Optional: true, Type: prim("string")},   // new optional
		}}, {Name: "Added", Fields: []model.Field{{Name: "x", Type: prim("string")}}}},
		Enums: []model.Enum{
			{Name: "Role", Values: []string{"admin", "member"}}, // value added
			{Name: "Color", Values: []string{"red"}},            // new enum
		},
	}

	r := Check(oldS, newS)
	logReport(t, "all compatible kinds", oldS, newS, r)
	if !r.Compatible {
		t.Fatalf("expected compatible verdict, got breaking: %#v", r.Changes)
	}
	want := map[string]Severity{
		KindFieldAddedOptional:     SeverityCompatible,
		KindFieldOptionalityLooser: SeverityCompatible,
		KindEntityAdded:            SeverityCompatible,
		KindEnumValueAdded:         SeverityCompatible,
		KindEnumAdded:              SeverityCompatible,
	}
	got := kindCount(r)
	if len(got) != len(want) {
		t.Fatalf("change kinds = %#v, want %#v", got, want)
	}
	for k, sev := range want {
		if got[k] != sev {
			t.Errorf("kind %s severity = %s, want %s", k, got[k], sev)
		}
	}
}

func TestIdenticalSchemasAreCompatible(t *testing.T) {
	s := &model.Schema{Version: "v1",
		Entities: []model.Entity{{Name: "U", Fields: []model.Field{
			{Name: "f", Type: model.Type{Kind: model.KindEnum, Values: []string{"A", "B"}}},
		}}},
	}
	r := Check(s, s)
	logReport(t, "identical", s, s, r)
	if !r.Compatible || len(r.Changes) != 0 {
		t.Fatalf("identical schemas produced changes: %#v", r)
	}
}

func TestInlineEnumTighteningIsBreaking(t *testing.T) {
	oldS := &model.Schema{Version: "v1", Entities: []model.Entity{{Name: "U", Fields: []model.Field{
		{Name: "kind", Type: model.Type{Kind: model.KindEnum, Values: []string{"A", "B", "C"}}},
	}}}}
	newS := &model.Schema{Version: "v2", Entities: []model.Entity{{Name: "U", Fields: []model.Field{
		{Name: "kind", Type: model.Type{Kind: model.KindEnum, Values: []string{"B", "C"}}},
	}}}}
	r := Check(oldS, newS)
	logReport(t, "inline enum tighten", oldS, newS, r)
	if r.Compatible {
		t.Fatal("removing an inline enum value must be breaking")
	}
	if c := findChange(r, KindFieldTypeChanged); c == nil {
		t.Fatalf("expected a field_type_changed change, got %#v", r.Changes)
	}
}

func TestVerdictIsReproducible(t *testing.T) {
	oldS := &model.Schema{Version: "v1",
		Entities: []model.Entity{{Name: "Zeta", Fields: []model.Field{
			{Name: "z", Type: prim("string")},
		}}, {Name: "Alpha", Fields: []model.Field{
			{Name: "a", Type: prim("string")},
			{Name: "b", Type: prim("int")},
		}}},
	}
	newS := &model.Schema{Version: "v2",
		Entities: []model.Entity{{Name: "Zeta", Fields: []model.Field{
			{Name: "z", Type: prim("int")},
			{Name: "extra", Optional: true, Type: prim("string")},
		}}, {Name: "Alpha", Fields: []model.Field{
			{Name: "a", Type: prim("string")},
		}}},
	}
	first := Check(oldS, newS)
	for i := 0; i < 10; i++ {
		again := Check(oldS, newS)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("verdict differs across runs (iteration %d):\n%#v\n%#v", i, first.Changes, again.Changes)
		}
	}
	logReport(t, "reproducibility x10", oldS, newS, first)
	// Verify explicit deterministic ordering by path.
	for i := 1; i < len(first.Changes); i++ {
		if comparePaths(first.Changes[i-1].Path, first.Changes[i].Path) > 0 {
			t.Fatalf("changes not path-sorted: %s after %s", first.Changes[i].Path, first.Changes[i-1].Path)
		}
	}
}

func TestNilSchemaHandling(t *testing.T) {
	// nil -> populated: everything is additive, therefore compatible.
	added := Check(nil, &model.Schema{Version: "v1", Entities: []model.Entity{{Name: "U"}}})
	if !added.Compatible {
		t.Fatalf("nil -> populated should be compatible: %#v", added.Changes)
	}
	// populated -> nil: everything existing is removed, therefore breaking.
	removed := Check(&model.Schema{Version: "v1", Entities: []model.Entity{{Name: "U"}}}, nil)
	if removed.Compatible {
		t.Fatal("populated -> nil should be breaking")
	}
	if findChange(removed, KindEntityRemoved) == nil {
		t.Fatalf("expected entity_removed, got %#v", removed.Changes)
	}
}

func findChange(r *Report, kind string) *Change {
	for i := range r.Changes {
		if r.Changes[i].Kind == kind {
			return &r.Changes[i]
		}
	}
	return nil
}
