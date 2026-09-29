// Package compat decides whether a "new" schema is backwards compatible with
// an "old" schema and lists every detected change in a stable order, so the
// same pair of schemas always yields the same verdict.
//
// Compatibility is judged from the point of view of an existing client
// consuming the "old" version: anything that can break that client (removed
// field, changed field type, removed enum value, newly required field,
// removed entity/enum) is marked breaking; additive or relaxing changes
// (new optional field, new enum value, required -> optional, new entity)
// are compatible.
package compat

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// Severity classifies a change as breaking or compatible.
type Severity string

const (
	SeverityBreaking   Severity = "breaking"
	SeverityCompatible Severity = "compatible"
)

// Change kind constants. They are part of the machine-readable verdict.
const (
	KindEntityAdded             = "entity_added"
	KindEntityRemoved           = "entity_removed"
	KindEnumAdded               = "enum_added"
	KindEnumRemoved             = "enum_removed"
	KindFieldAddedOptional      = "field_added_optional"
	KindFieldAddedRequired      = "field_added_required"
	KindFieldRemoved            = "field_removed"
	KindFieldTypeChanged        = "field_type_changed"
	KindFieldOptionalityTighter = "field_optionality_tightened"
	KindFieldOptionalityLooser  = "field_optionality_relaxed"
	KindEnumValueAdded          = "enum_value_added"
	KindEnumValueRemoved        = "enum_value_removed"
)

// Change describes one difference between two schema versions.
type Change struct {
	Kind     string // machine-readable change kind
	Severity Severity
	Path     string // stable location, e.g. entity.User.field.id
	Message  string // human-readable explanation
}

// Report is the compatibility verdict for one ordered schema pair.
type Report struct {
	Compatible bool
	Changes    []Change
}

// Check compares old against new and returns a deterministic report.
func Check(oldSchema, newSchema *model.Schema) *Report {
	r := &Report{Compatible: true}

	oldEntities := indexEntities(oldSchema)
	newEntities := indexEntities(newSchema)
	oldEnums := indexEnums(oldSchema)
	newEnums := indexEnums(newSchema)

	// Entities.
	for name, oldEnt := range oldEntities {
		newEnt, ok := newEntities[name]
		if !ok {
			r.add(KindEntityRemoved, SeverityBreaking, "entity."+name,
				"entity %q was removed", name)
			continue
		}
		compareFields(r, name, oldEnt, newEnt)
	}
	for name := range newEntities {
		if _, ok := oldEntities[name]; !ok {
			r.add(KindEntityAdded, SeverityCompatible, "entity."+name,
				"entity %q was added", name)
		}
	}

	// Named enums.
	for name, oldEnum := range oldEnums {
		newEnum, ok := newEnums[name]
		if !ok {
			r.add(KindEnumRemoved, SeverityBreaking, "enum."+name,
				"enum %q was removed", name)
			continue
		}
		compareEnumValues(r, "enum."+name, name, oldEnum.Values, newEnum.Values)
	}
	for name := range newEnums {
		if _, ok := oldEnums[name]; !ok {
			r.add(KindEnumAdded, SeverityCompatible, "enum."+name,
				"enum %q was added", name)
		}
	}

	r.sort()
	return r
}

func (r *Report) add(kind string, sev Severity, path, format string, args ...any) {
	if sev == SeverityBreaking {
		r.Compatible = false
	}
	r.Changes = append(r.Changes, Change{
		Kind:     kind,
		Severity: sev,
		Path:     path,
		Message:  fmt.Sprintf(format, args...),
	})
}

func (r *Report) sort() {
	sort.SliceStable(r.Changes, func(i, j int) bool {
		a, b := r.Changes[i], r.Changes[j]
		if a.Path != b.Path {
			return comparePaths(a.Path, b.Path) < 0
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Message < b.Message
	})
}

// comparePaths orders change paths with numeric bracket indexes compared by
// value, so e.g. value[2] precedes value[10].
func comparePaths(a, b string) int {
	pa, pb := pathSegments(a), pathSegments(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, y := pa[i], pb[i]
		if x.num && y.num {
			if x.n != y.n {
				if x.n < y.n {
					return -1
				}
				return 1
			}
			continue
		}
		if x.text != y.text {
			if x.text < y.text {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
		return 1
	}
	return 0
}

type pathSeg struct {
	text string
	num  bool
	n    int
}

func pathSegments(p string) []pathSeg {
	var segs []pathSeg
	for i := 0; i < len(p); {
		switch p[i] {
		case '[':
			j := i + 1
			n := 0
			for j < len(p) && p[j] >= '0' && p[j] <= '9' {
				n = n*10 + int(p[j]-'0')
				j++
			}
			if j < len(p) && p[j] == ']' && j > i+1 {
				segs = append(segs, pathSeg{num: true, n: n})
				i = j + 1
				continue
			}
		case '.':
			i++
			continue
		}
		j := i
		for j < len(p) && p[j] != '.' && p[j] != '[' {
			j++
		}
		segs = append(segs, pathSeg{text: p[i:j]})
		i = j
	}
	return segs
}

func compareFields(r *Report, entityName string, oldEnt, newEnt model.Entity) {
	oldFields := map[string]model.Field{}
	for _, f := range oldEnt.Fields {
		oldFields[f.Name] = f
	}
	newFields := map[string]model.Field{}
	for _, f := range newEnt.Fields {
		newFields[f.Name] = f
	}

	for _, of := range oldEnt.Fields { // declared, deterministic order
		nf, ok := newFields[of.Name]
		path := fmt.Sprintf("entity.%s.field.%s", entityName, of.Name)
		if !ok {
			r.add(KindFieldRemoved, SeverityBreaking, path,
				"field %q was removed from entity %q", of.Name, entityName)
			continue
		}
		if diff := typeDiff(of.Type, nf.Type); diff != "" {
			r.add(KindFieldTypeChanged, SeverityBreaking, path,
				"field %s.%s changed type: %s", entityName, of.Name, diff)
		}
		switch {
		case of.Optional && !nf.Optional:
			r.add(KindFieldOptionalityTighter, SeverityBreaking, path,
				"field %s.%s changed from optional to required", entityName, of.Name)
		case !of.Optional && nf.Optional:
			r.add(KindFieldOptionalityLooser, SeverityCompatible, path,
				"field %s.%s changed from required to optional", entityName, of.Name)
		}
	}
	for _, nf := range newEnt.Fields {
		if _, ok := oldFields[nf.Name]; ok {
			continue
		}
		path := fmt.Sprintf("entity.%s.field.%s", entityName, nf.Name)
		if nf.Optional {
			r.add(KindFieldAddedOptional, SeverityCompatible, path,
				"optional field %q was added to entity %q", nf.Name, entityName)
		} else {
			r.add(KindFieldAddedRequired, SeverityBreaking, path,
				"required field %q was added to entity %q", nf.Name, entityName)
		}
	}
}

func compareEnumValues(r *Report, path, enumName string, oldVals, newVals []string) {
	oldSet := map[string]bool{}
	for _, v := range oldVals {
		oldSet[v] = true
	}
	newSet := map[string]bool{}
	for _, v := range newVals {
		newSet[v] = true
	}
	// Iterate declared order for determinism without another sort.
	for _, v := range oldVals {
		if !newSet[v] {
			r.add(KindEnumValueRemoved, SeverityBreaking, path+".value."+v,
				"enum %q no longer allows value %q", enumName, v)
		}
	}
	for _, v := range newVals {
		if !oldSet[v] {
			r.add(KindEnumValueAdded, SeverityCompatible, path+".value."+v,
				"enum %q added allowed value %q", enumName, v)
		}
	}
}

// typeDiff returns "" when the types are equal, otherwise a description of
// the difference. The model is normalized, so different primitive spellings
// (e.g. "integer" vs "int") compare as equal.
func typeDiff(a, b model.Type) string {
	return typeDiffAt(a, b, "")
}

func typeDiffAt(a, b model.Type, at string) string {
	where := at
	if where == "" {
		where = "type"
	}
	if a.Kind != b.Kind {
		return fmt.Sprintf("%s changed from %s to %s", where, a.Kind, b.Kind)
	}
	switch a.Kind {
	case model.KindPrimitive:
		if a.Primitive != b.Primitive {
			return fmt.Sprintf("%s changed primitive from %q to %q", where, a.Primitive, b.Primitive)
		}
	case model.KindRef:
		if a.RefName != b.RefName {
			return fmt.Sprintf("%s changed reference from %q to %q", where, a.RefName, b.RefName)
		}
	case model.KindEnum:
		if a.EnumName != b.EnumName {
			return fmt.Sprintf("%s changed enum reference from %q to %q", where, a.EnumName, b.EnumName)
		}
		if d := stringSetDiff(a.Values, b.Values); d != "" {
			return fmt.Sprintf("%s inline enum values differ: %s", where, d)
		}
	case model.KindArray:
		if a.Elem == nil || b.Elem == nil {
			if a.Elem != b.Elem {
				return where + " element type changed"
			}
			return ""
		}
		return typeDiffAt(*a.Elem, *b.Elem, where+" element")
	}
	return ""
}

func stringSetDiff(a, b []string) string {
	setB := map[string]bool{}
	for _, v := range b {
		setB[v] = true
	}
	setA := map[string]bool{}
	for _, v := range a {
		setA[v] = true
	}
	var removed, added []string
	for _, v := range a {
		if !setB[v] {
			removed = append(removed, v)
		}
	}
	for _, v := range b {
		if !setA[v] {
			added = append(added, v)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	var parts []string
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(removed, ", "))
	}
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(added, ", "))
	}
	return strings.Join(parts, "; ")
}

func indexEntities(s *model.Schema) map[string]model.Entity {
	m := map[string]model.Entity{}
	if s == nil {
		return m
	}
	for _, e := range s.Entities {
		m[e.Name] = e
	}
	return m
}

func indexEnums(s *model.Schema) map[string]model.Enum {
	m := map[string]model.Enum{}
	if s == nil {
		return m
	}
	for _, e := range s.Enums {
		m[e.Name] = e
	}
	return m
}
