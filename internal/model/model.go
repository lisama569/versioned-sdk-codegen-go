// Package model 定义 schema 归一化后的统一内部模型。
//
// 不同来源（自研简化写法、OpenAPI 子集等）的 schema 经过归一化处理后，
// 都会转换成本包中的 Schema 表示，供兼容性判定与代码生成使用。
package model

import "sort"

// PrimitiveType 表示字段可使用的原始类型。
type PrimitiveType string

const (
	TypeString  PrimitiveType = "string"
	TypeInteger PrimitiveType = "integer"
	TypeNumber  PrimitiveType = "number"
	TypeBoolean PrimitiveType = "boolean"
)

// IsValidPrimitive 判断原始类型是否在支持范围内。
func IsValidPrimitive(t PrimitiveType) bool {
	switch t {
	case TypeString, TypeInteger, TypeNumber, TypeBoolean:
		return true
	default:
		return false
	}
}

// Field 表示结构体（实体）中的一个字段。
type Field struct {
	// Name 是归一化后的字段名（去空白，保留来源大小写）。
	Name string
	// Type 是字段的原始类型；引用枚举时仍记录声明的原始类型，
	// 是否为枚举引用由 EnumRef 表达。
	Type PrimitiveType
	// Optional 为 true 时表示该字段可以缺省。
	Optional bool
	// EnumRef 非空时表示该字段引用 Enum 中同名的枚举定义。
	// 按约定枚举引用的底层类型必须为 string。
	EnumRef string
}

// Entity 表示一个具名结构体（对象）。
type Entity struct {
	// Name 是实体名（归一化后，必须为合法 Go 导出标识符）。
	Name string
	// Fields 按字段名排序，保证多次归一化结果稳定。
	Fields []Field
}

// Field 按名称查找字段；不存在时返回 nil。
func (e *Entity) Field(name string) *Field {
	for i := range e.Fields {
		if e.Fields[i].Name == name {
			return &e.Fields[i]
		}
	}
	return nil
}

// Enum 表示一个具名枚举（字符串枚举）。
type Enum struct {
	// Name 是枚举名（归一化后，必须为合法 Go 导出标识符）。
	Name string
	// Values 为枚举取值，已去重并按声明顺序保留，
	// 但对外输出（代码生成）时统一按字典序排序。
	Values []string
}

// Schema 是一份 schema 归一化后的完整表示。
type Schema struct {
	// Version 是 schema 自描述的版本号（归一化后，去除首尾空白）。
	Version string
	// Entities 为实体列表，按名称排序。
	Entities []Entity
	// Enums 为枚举列表，按名称排序。
	Enums []Enum
}

// Entity 按名称查找实体；不存在时返回 nil。
func (s *Schema) Entity(name string) *Entity {
	for i := range s.Entities {
		if s.Entities[i].Name == name {
			return &s.Entities[i]
		}
	}
	return nil
}

// Enum 按名称查找枚举；不存在时返回 nil。
func (s *Schema) Enum(name string) *Enum {
	for i := range s.Enums {
		if s.Enums[i].Name == name {
			return &s.Enums[i]
		}
	}
	return nil
}

// Sort 原地对 schema 内的实体、枚举及字段排序，
// 使归一化结果与来源书写顺序无关。
func (s *Schema) Sort() {
	sort.Slice(s.Enums, func(i, j int) bool { return s.Enums[i].Name < s.Enums[j].Name })
	sort.Slice(s.Entities, func(i, j int) bool { return s.Entities[i].Name < s.Entities[j].Name })
	for i := range s.Enums {
		sort.Strings(s.Enums[i].Values)
	}
	for i := range s.Entities {
		sort.Slice(s.Entities[i].Fields, func(a, b int) bool {
			return s.Entities[i].Fields[a].Name < s.Entities[i].Fields[b].Name
		})
	}
}
