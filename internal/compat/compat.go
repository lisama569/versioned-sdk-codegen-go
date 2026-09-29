// Package compat 负责两版内部模型 schema 之间的兼容性判定。
//
// 判定口径（旧 schema -> 新 schema，面向"旧版本客户端能否安全消费新版本"）：
//
// 破坏性变更（breaking）：
//   - 删除实体、删除枚举；
//   - 删除字段；
//   - 字段类型变更；
//   - 字段由可选变为必填（收紧）；
//   - 字段的枚举引用目标变更；
//   - 删除枚举取值（收紧枚举，旧客户端可能发送新版本不认识的值）。
//
// 兼容性变更（compatible）：
//   - 新增实体、新增枚举；
//   - 新增字段；
//   - 字段由必填变为可选（放宽）；
//   - 枚举新增取值。
//
// Compare 不依赖任何 map 遍历顺序：输入模型在归一化阶段已排序，
// 差异清单也会按 (Path, Kind) 排序，因此同一对 schema 反复判定
// 得到的结论逐字节一致。
package compat

import (
	"sort"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// ChangeKind 是兼容性变更的分类。
type ChangeKind string

const (
	EntityAdded        ChangeKind = "entity_added"
	EntityRemoved      ChangeKind = "entity_removed"
	EnumAdded          ChangeKind = "enum_added"
	EnumRemoved        ChangeKind = "enum_removed"
	FieldAdded         ChangeKind = "field_added"
	FieldRemoved       ChangeKind = "field_removed"
	FieldTypeChanged   ChangeKind = "field_type_changed"
	FieldOptionalness  ChangeKind = "field_optionalness_changed"
	FieldEnumRefChange ChangeKind = "field_enum_ref_changed"
	EnumValueAdded     ChangeKind = "enum_value_added"
	EnumValueRemoved   ChangeKind = "enum_value_removed"
)

// Severity 表示变更的兼容级别。
type Severity string

const (
	SeverityCompatible Severity = "compatible"
	SeverityBreaking   Severity = "breaking"
)

// Change 描述新旧 schema 之间的一处差异及其兼容性结论。
type Change struct {
	Kind     ChangeKind
	Severity Severity
	// Path 是变更在模型中的定位，如 "entities.User.fields.age"。
	Path string
	// Message 给出判定依据（旧值 -> 新值）。
	Message string
}

// Report 是一次兼容性判定的完整结论。
type Report struct {
	OldVersion string
	NewVersion string
	Changes    []Change
}

// BreakingCount 返回破坏性变更的数量。
func (r *Report) BreakingCount() int {
	n := 0
	for _, c := range r.Changes {
		if c.Severity == SeverityBreaking {
			n++
		}
	}
	return n
}

// IsCompatible 为 true 表示新 schema 相对旧 schema 向后兼容。
func (r *Report) IsCompatible() bool { return r.BreakingCount() == 0 }

// Compare 判定 new 相对 old 是否向后兼容。
// 同一对 schema 多次判定的结论必须完全一致（确定性）。
func Compare(old, new *model.Schema) *Report {
	r := &Report{}
	if old != nil {
		r.OldVersion = old.Version
	}
	if new != nil {
		r.NewVersion = new.Version
	}

	oldEntities := indexEntities(old)
	newEntities := indexEntities(new)
	oldEnums := indexEnums(old)
	newEnums := indexEnums(new)

	// 实体：删除、新增。
	for _, name := range sortedKeysOf(oldEntities) {
		path := "entities." + name
		if _, ok := newEntities[name]; !ok {
			r.Changes = append(r.Changes, Change{
				Kind: EntityRemoved, Severity: SeverityBreaking, Path: path,
				Message: "实体 " + name + " 被删除，旧客户端无法继续使用",
			})
			continue
		}
		compareFields(&r.Changes, name, oldEntities[name], newEntities[name])
	}
	for _, name := range sortedKeysOf(newEntities) {
		if _, ok := oldEntities[name]; !ok {
			r.Changes = append(r.Changes, Change{
				Kind: EntityAdded, Severity: SeverityCompatible, Path: "entities." + name,
				Message: "新增实体 " + name,
			})
		}
	}

	// 枚举：删除、新增，以及取值集合差异。
	for _, name := range sortedKeysOf(oldEnums) {
		path := "enums." + name
		nw, ok := newEnums[name]
		if !ok {
			r.Changes = append(r.Changes, Change{
				Kind: EnumRemoved, Severity: SeverityBreaking, Path: path,
				Message: "枚举 " + name + " 被删除",
			})
			continue
		}
		compareEnumValues(&r.Changes, path, oldEnums[name], nw)
	}
	for _, name := range sortedKeysOf(newEnums) {
		if _, ok := oldEnums[name]; !ok {
			r.Changes = append(r.Changes, Change{
				Kind: EnumAdded, Severity: SeverityCompatible, Path: "enums." + name,
				Message: "新增枚举 " + name,
			})
		}
	}

	sort.SliceStable(r.Changes, func(i, j int) bool {
		if r.Changes[i].Path != r.Changes[j].Path {
			return r.Changes[i].Path < r.Changes[j].Path
		}
		return r.Changes[i].Kind < r.Changes[j].Kind
	})
	return r
}

func compareFields(out *[]Change, entityName string, old, new model.Entity) {
	oldFields := indexFields(old)
	newFields := indexFields(new)
	base := "entities." + entityName + ".fields."

	for _, name := range sortedKeysOf(oldFields) {
		path := base + name
		nf, ok := newFields[name]
		if !ok {
			*out = append(*out, Change{
				Kind: FieldRemoved, Severity: SeverityBreaking, Path: path,
				Message: "字段 " + entityName + "." + name + " 被删除",
			})
			continue
		}
		of := oldFields[name]
		if of.Type != nf.Type {
			*out = append(*out, Change{
				Kind: FieldTypeChanged, Severity: SeverityBreaking, Path: path,
				Message: "字段类型变更: " + string(of.Type) + " -> " + string(nf.Type),
			})
		}
		if of.Optional != nf.Optional {
			switch {
			case !of.Optional && nf.Optional:
				*out = append(*out, Change{
					Kind: FieldOptionalness, Severity: SeverityCompatible, Path: path,
					Message: "字段由必填变为可选（放宽）",
				})
			case of.Optional && !nf.Optional:
				*out = append(*out, Change{
					Kind: FieldOptionalness, Severity: SeverityBreaking, Path: path,
					Message: "字段由可选变为必填（收紧），未提供该字段的旧请求将被拒绝",
				})
			}
		}
		if of.EnumRef != nf.EnumRef {
			*out = append(*out, Change{
				Kind: FieldEnumRefChange, Severity: SeverityBreaking, Path: path,
				Message: "字段枚举引用变更: " + refDesc(of.EnumRef) + " -> " + refDesc(nf.EnumRef),
			})
		}
	}
	for _, name := range sortedKeysOf(newFields) {
		if _, ok := oldFields[name]; !ok {
			*out = append(*out, Change{
				Kind: FieldAdded, Severity: SeverityCompatible, Path: base + name,
				Message: "新增可选/必填字段 " + entityName + "." + name,
			})
		}
	}
}

func compareEnumValues(out *[]Change, path string, old, new model.Enum) {
	oldSet := map[string]bool{}
	newSet := map[string]bool{}
	for _, v := range old.Values {
		oldSet[v] = true
	}
	for _, v := range new.Values {
		newSet[v] = true
	}
	var values []string
	values = append(values, old.Values...)
	values = append(values, new.Values...)
	sort.Strings(values)
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		switch {
		case oldSet[v] && !newSet[v]:
			*out = append(*out, Change{
				Kind: EnumValueRemoved, Severity: SeverityBreaking, Path: path + ".values." + v,
				Message: "枚举取值 " + v + " 被删除（收紧枚举），持有该值的旧数据将不再合法",
			})
		case !oldSet[v] && newSet[v]:
			*out = append(*out, Change{
				Kind: EnumValueAdded, Severity: SeverityCompatible, Path: path + ".values." + v,
				Message: "枚举新增取值 " + v,
			})
		}
	}
}

func refDesc(ref string) string {
	if ref == "" {
		return "<无枚举引用>"
	}
	return ref
}

func indexEntities(s *model.Schema) map[string]model.Entity {
	out := map[string]model.Entity{}
	if s == nil {
		return out
	}
	for _, e := range s.Entities {
		out[e.Name] = e
	}
	return out
}

func indexEnums(s *model.Schema) map[string]model.Enum {
	out := map[string]model.Enum{}
	if s == nil {
		return out
	}
	for _, e := range s.Enums {
		out[e.Name] = e
	}
	return out
}

func indexFields(e model.Entity) map[string]model.Field {
	out := map[string]model.Field{}
	for _, f := range e.Fields {
		out[f.Name] = f
	}
	return out
}

func sortedKeysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
