// Package model defines the unified internal representation of a schema.
//
// Every supported source dialect (simple JSON, OpenAPI-like JSON, ...) is
// normalized into this model before compatibility checks or code generation
// run against it.
package model

// Schema is the normalized description of one service version.
type Schema struct {
	Version  string
	Entities []Entity
	Enums    []Enum
}

// Entity is a named object/struct type.
type Entity struct {
	Name   string
	Fields []Field
}

// Field describes a single property of an entity.
type Field struct {
	// Name is the canonical field name exactly as written in the source
	// (no case rewriting).
	Name string
	// Type is the normalized field type.
	Type Type
	// Optional reports whether the field may be omitted.
	Optional bool
	// Note carries an optional free-form description.
	Note string
}

// TypeKind enumerates the supported normalized type constructors.
type TypeKind string

const (
	KindPrimitive TypeKind = "primitive" // one of string/int/bool/float
	KindEnum      TypeKind = "enum"      // named or inline enum
	KindArray     TypeKind = "array"
	KindRef       TypeKind = "ref" // reference to another entity
)

// Type is a normalized field type.
type Type struct {
	Kind TypeKind
	// Primitive is the normalized primitive name ("string", "int",
	// "bool", "float") when Kind == KindPrimitive.
	Primitive string
	// RefName is the referenced entity name when Kind == KindRef.
	RefName string
	// EnumName is the referenced named enum when Kind == KindEnum.
	// Empty for inline enums.
	EnumName string
	// Values holds the allowed values for an inline enum.
	Values []string
	// Elem is the element type when Kind == KindArray.
	Elem *Type
}

// Enum is a named, reusable enum.
type Enum struct {
	Name   string
	Values []string
}
