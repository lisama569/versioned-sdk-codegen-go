package normalize

import (
	"encoding/json"
	"fmt"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// parseSimple handles the concise "simple" dialect:
//
//	{
//	  "version": "v2",
//	  "entities": [
//	    {
//	      "name": "User",
//	      "fields": [
//	        {"name": "id", "type": "string"},
//	        {"name": "age", "type": "int", "optional": true},
//	        {"name": "tags", "type": {"kind": "array", "items": "string"}},
//	        {"name": "role", "type": {"kind": "enum", "name": "Role"}},
//	        {"name": "addr", "type": {"kind": "ref", "name": "Address"}}
//	      ]
//	    }
//	  ],
//	  "enums": [{"name": "Role", "values": ["admin", "user"]}]
//	}
//
// A bare string type ("string", "Address[]") is accepted as shorthand.
func parseSimple(c *collector, root map[string]any) *model.Schema {
	schema := &model.Schema{}

	if v, ok := root["version"]; ok {
		if s, ok := asString(v); ok && s != "" {
			schema.Version = s
		} else {
			c.add(IssueMissingVersion, "version", "version must be a non-empty string, got %s", jsonKind(v))
		}
	} else {
		c.add(IssueMissingVersion, "version", "schema is missing the required version field")
	}

	// First pass: collect names so refs and named enums resolve regardless
	// of declaration order. Entities and enums share one namespace.
	entitiesRaw, entitiesOK := asSlice(root["entities"])
	enumsRaw, enumsOK := asSlice(root["enums"])
	if root["entities"] != nil && !entitiesOK {
		c.add(IssueInvalidSyntax, "entities", "entities must be an array, got %s", jsonKind(root["entities"]))
	}
	if root["enums"] != nil && !enumsOK {
		c.add(IssueInvalidSyntax, "enums", "enums must be an array, got %s", jsonKind(root["enums"]))
	}
	knownEntities := map[string]bool{}
	knownEnums := map[string]bool{}

	for i, raw := range entitiesRaw {
		p := fmt.Sprintf("entities[%d]", i)
		obj, ok := asObject(raw)
		if !ok {
			c.add(IssueInvalidSyntax, p, "entity must be an object, got %s", jsonKind(raw))
			continue
		}
		name, ok := nonEmptyStringField(obj, "name")
		if !ok {
			c.add(IssueMissingName, p+".name", "entity is missing a non-empty name")
			continue
		}
		if !isGoIdentifier(name) {
			c.add(IssueInvalidName, p+".name", "entity name %q must be a valid Go identifier", name)
			continue
		}
		switch {
		case knownEntities[name]:
			c.add(IssueNameConflict, p, "duplicate entity name %q", name)
		case knownEnums[name]:
			c.add(IssueNameConflict, p, "entity %q conflicts with an enum of the same name", name)
		default:
			knownEntities[name] = true
		}
	}

	for i, raw := range enumsRaw {
		p := fmt.Sprintf("enums[%d]", i)
		obj, ok := asObject(raw)
		if !ok {
			c.add(IssueInvalidSyntax, p, "enum must be an object, got %s", jsonKind(raw))
			continue
		}
		name, ok := nonEmptyStringField(obj, "name")
		if !ok {
			c.add(IssueMissingName, p+".name", "enum is missing a non-empty name")
			continue
		}
		if !isGoIdentifier(name) {
			c.add(IssueInvalidName, p+".name", "enum name %q must be a valid Go identifier", name)
			continue
		}
		switch {
		case knownEnums[name]:
			c.add(IssueNameConflict, p, "duplicate enum name %q", name)
		case knownEntities[name]:
			c.add(IssueNameConflict, p, "enum %q conflicts with an entity of the same name", name)
		default:
			knownEnums[name] = true
		}
	}

	// Second pass: build enums.
	for i, raw := range enumsRaw {
		p := fmt.Sprintf("enums[%d]", i)
		obj, ok := asObject(raw)
		if !ok {
			continue
		}
		name, _ := nonEmptyStringField(obj, "name")
		if name == "" {
			continue
		}
		enum := model.Enum{Name: name}
		valuesRaw, ok := asSlice(obj["values"])
		if !ok {
			c.add(IssueInvalidSyntax, p+".values", "enum %q values must be an array", name)
			continue
		}
		seen := map[string]bool{}
		for j, vr := range valuesRaw {
			vp := fmt.Sprintf("%s.values[%d]", p, j)
			val, ok := asString(vr)
			if !ok || val == "" {
				c.add(IssueInvalidSyntax, vp, "enum value must be a non-empty string, got %s", jsonKind(vr))
				continue
			}
			if !isGoEnumValue(val) {
				c.add(IssueInvalidName, vp, "enum value %q in enum %q must be a valid Go identifier", val, name)
				continue
			}
			if seen[val] {
				c.add(IssueNameConflict, vp, "enum %q lists value %q more than once", name, val)
				continue
			}
			seen[val] = true
			enum.Values = append(enum.Values, val)
		}
		schema.Enums = append(schema.Enums, enum)
	}

	// Third pass: build entities and fields.
	for i, raw := range entitiesRaw {
		p := fmt.Sprintf("entities[%d]", i)
		obj, ok := asObject(raw)
		if !ok {
			continue
		}
		name, _ := nonEmptyStringField(obj, "name")
		if name == "" {
			continue
		}
		entity := model.Entity{Name: name}
		fieldsRaw, isSlice := asSlice(obj["fields"])
		if obj["fields"] == nil {
			fieldsRaw, isSlice = nil, true
		}
		if !isSlice {
			c.add(IssueInvalidSyntax, p+".fields", "entity %q fields must be an array, got %s", name, jsonKind(obj["fields"]))
			continue
		}
		fieldNames := map[string]bool{}
		for j, fr := range fieldsRaw {
			fp := fmt.Sprintf("%s.fields[%d]", p, j)
			fobj, ok := asObject(fr)
			if !ok {
				c.add(IssueInvalidSyntax, fp, "field must be an object, got %s", jsonKind(fr))
				continue
			}
			fname, ok := nonEmptyStringField(fobj, "name")
			if !ok {
				c.add(IssueMissingName, fp+".name", "field in entity %q is missing a non-empty name", name)
				continue
			}
			if !isGoIdentifier(fname) {
				c.add(IssueInvalidName, fp+".name", "field name %q in entity %q must be a valid Go identifier", fname, name)
				continue
			}
			if fieldNames[fname] {
				c.add(IssueNameConflict, fp, "entity %q declares field %q more than once", name, fname)
				continue
			}
			fieldNames[fname] = true
			field := model.Field{Name: fname}
			if note, ok := asString(fobj["note"]); ok {
				field.Note = note
			}
			if opt, ok := fobj["optional"].(bool); ok {
				field.Optional = opt
			} else if fobj["optional"] != nil {
				c.add(IssueInvalidSyntax, fp+".optional", "field %s.%s optional must be a boolean, got %s", name, fname, jsonKind(fobj["optional"]))
			}
			if fobj["type"] == nil {
				c.add(IssueMissingType, fp+".type", "field %s.%s is missing a type", name, fname)
				continue
			}
			ft, ok := parseSimpleType(c, fobj["type"], fp+".type", knownEntities, knownEnums)
			if !ok {
				continue
			}
			field.Type = *ft
			entity.Fields = append(entity.Fields, field)
		}
		schema.Entities = append(schema.Entities, entity)
	}

	return schema
}

// parseSimpleType parses one simple-dialect type expression:
//
//	"string"
//	"Address"          (ref)
//	"string[]"         (array shorthand)
//	{"kind": "array", "items": "string"}
//	{"kind": "enum", "name": "Role"}
//	{"kind": "enum", "values": ["a", "b"]}
//	{"kind": "ref", "name": "Address"}
func parseSimpleType(c *collector, raw any, path string, entities, enums map[string]bool) (*model.Type, bool) {
	if s, ok := asString(raw); ok {
		if s == "" {
			c.add(IssueInvalidType, path, "type string must not be empty")
			return nil, false
		}
		if len(s) >= 2 && s[len(s)-2:] == "[]" {
			elem, ok := parseSimpleType(c, s[:len(s)-2], path, entities, enums)
			if !ok {
				return nil, false
			}
			return &model.Type{Kind: model.KindArray, Elem: elem}, true
		}
		if prim, ok := canonicalPrimitive(s); ok {
			return &model.Type{Kind: model.KindPrimitive, Primitive: prim}, true
		}
		if enums[s] {
			return &model.Type{Kind: model.KindEnum, EnumName: s}, true
		}
		if entities[s] {
			return &model.Type{Kind: model.KindRef, RefName: s}, true
		}
		// A capitalized spelling reads like a reference to a missing
		// entity/enum; a lower-case spelling is an invalid primitive.
		if s[0] >= 'A' && s[0] <= 'Z' {
			c.add(IssueUnknownRef, path, "type %q looks like a reference but no entity/enum with that name exists", s)
		} else {
			c.add(IssueInvalidType, path, "unknown primitive type %q (want string, int, bool, float or array/enum/ref)", s)
		}
		return nil, false
	}

	obj, ok := asObject(raw)
	if !ok {
		c.add(IssueInvalidType, path, "type must be a string or object, got %s", jsonKind(raw))
		return nil, false
	}
	kind, _ := nonEmptyStringField(obj, "kind")
	switch kind {
	case "primitive":
		name, ok := nonEmptyStringField(obj, "name")
		if !ok {
			c.add(IssueMissingType, path+".name", "primitive type requires a name")
			return nil, false
		}
		prim, ok := canonicalPrimitive(name)
		if !ok {
			c.add(IssueInvalidType, path+".name", "unknown primitive %q", name)
			return nil, false
		}
		return &model.Type{Kind: model.KindPrimitive, Primitive: prim}, true
	case "array":
		if obj["items"] == nil {
			c.add(IssueMissingType, path+".items", "array type requires an items type")
			return nil, false
		}
		elem, ok := parseSimpleType(c, obj["items"], path+".items", entities, enums)
		if !ok {
			return nil, false
		}
		return &model.Type{Kind: model.KindArray, Elem: elem}, true
	case "enum":
		if name, ok := nonEmptyStringField(obj, "name"); ok {
			if !enums[name] {
				c.add(IssueUnknownRef, path+".name", "unknown enum %q", name)
				return nil, false
			}
			return &model.Type{Kind: model.KindEnum, EnumName: name}, true
		}
		valuesRaw, ok := asSlice(obj["values"])
		if !ok || len(valuesRaw) == 0 {
			c.add(IssueInvalidType, path, "inline enum requires a non-empty values array")
			return nil, false
		}
		t := &model.Type{Kind: model.KindEnum}
		seen := map[string]bool{}
		for i, vr := range valuesRaw {
			vp := fmt.Sprintf("%s.values[%d]", path, i)
			val, ok := asString(vr)
			if !ok || val == "" {
				c.add(IssueInvalidType, vp, "enum value must be a non-empty string, got %s", jsonKind(vr))
				return nil, false
			}
			if !isGoEnumValue(val) {
				c.add(IssueInvalidName, vp, "inline enum value %q must be a valid Go identifier", val)
				return nil, false
			}
			if seen[val] {
				c.add(IssueNameConflict, vp, "inline enum lists value %q more than once", val)
				return nil, false
			}
			seen[val] = true
			t.Values = append(t.Values, val)
		}
		return t, true
	case "ref":
		name, ok := nonEmptyStringField(obj, "name")
		if !ok {
			c.add(IssueMissingType, path+".name", "ref type requires a name")
			return nil, false
		}
		if !entities[name] {
			c.add(IssueUnknownRef, path+".name", "unknown entity %q", name)
			return nil, false
		}
		return &model.Type{Kind: model.KindRef, RefName: name}, true
	default:
		c.add(IssueInvalidType, path+".kind", "unknown type kind %q (want primitive, array, enum or ref)", kind)
		return nil, false
	}
}

func nonEmptyStringField(obj map[string]any, key string) (string, bool) {
	v, ok := obj[key]
	if !ok {
		return "", false
	}
	s, ok := asString(v)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

func isGoIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') {
			return false
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	// Field/enum names that collide with Go builtins would still compile in
	// most contexts, but reject the common footgun explicitly.
	switch s {
	case "string", "int", "bool", "float", "byte", "rune", "error",
		"true", "false", "nil", "map", "len", "make", "new":
		return false
	}
	return true
}

// isGoEnumValue reports whether s can be used verbatim as a Go const
// identifier for an enum member.
func isGoEnumValue(s string) bool {
	return isGoIdentifier(s)
}

func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		return "number"
	case float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
