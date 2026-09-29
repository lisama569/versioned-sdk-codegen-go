package normalize

import (
	"fmt"
	"strings"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// parseOpenAPI handles an OpenAPI-3-shaped document, using only the subset
// needed by the internal model:
//
//	{
//	  "openapi": "3.0.3",
//	  "info": {"version": "v2"},
//	  "components": {
//	    "schemas": {
//	      "User": {
//	        "type": "object",
//	        "required": ["id"],
//	        "properties": {
//	          "id": {"type": "string"},
//	          "age": {"type": "integer"},
//	          "tags": {"type": "array", "items": {"type": "string"}},
//	          "role": {"$ref": "#/components/schemas/Role"},
//	          "kind": {"type": "string", "enum": ["a", "b"]}
//	        }
//	      },
//	      "Role": {"type": "string", "enum": ["admin", "user"]}
//	    }
//	  }
//	}
//
// A component is an enum when it carries "enum"; otherwise a "type":"object"
// component is an entity. Any other component shape is reported, never
// silently skipped.
func parseOpenAPI(c *collector, root map[string]any) *model.Schema {
	schema := &model.Schema{}

	if info, ok := asObject(root["info"]); ok {
		if v, ok := info["version"]; ok {
			if s, ok := asString(v); ok && s != "" {
				schema.Version = s
			} else {
				c.add(IssueMissingVersion, "info.version", "info.version must be a non-empty string, got %s", jsonKind(v))
			}
		} else {
			c.add(IssueMissingVersion, "info.version", "schema is missing info.version")
		}
	} else {
		c.add(IssueMissingVersion, "info.version", "schema is missing the info.version field")
	}

	components, componentsOK := asObject(root["components"])
	if root["components"] != nil && !componentsOK {
		c.add(IssueInvalidSyntax, "components", "components must be an object, got %s", jsonKind(root["components"]))
		return schema
	}
	schemas, schemasOK := asObject(components["schemas"])
	if components != nil && components["schemas"] != nil && !schemasOK {
		c.add(IssueInvalidSyntax, "components.schemas", "components.schemas must be an object, got %s", jsonKind(components["schemas"]))
		return schema
	}

	names := sortedKeys(schemas)
	kindOf := map[string]string{} // name -> "enum" | "entity"
	for _, name := range names {
		p := "components.schemas." + name
		obj, ok := asObject(schemas[name])
		if !ok {
			c.add(IssueInvalidSyntax, p, "schema %q must be an object, got %s", name, jsonKind(schemas[name]))
			continue
		}
		if !isGoIdentifier(name) {
			c.add(IssueInvalidName, p+".name", "schema name %q must be a valid Go identifier", name)
			continue
		}
		if _, hasEnum := obj["enum"]; hasEnum {
			kindOf[name] = "enum"
			continue
		}
		t, _ := nonEmptyStringField(obj, "type")
		if t == "object" || obj["properties"] != nil {
			kindOf[name] = "entity"
			continue
		}
		c.add(IssueUnsupported, p+".type", "schema %q is neither an object nor an enum (type=%q)", name, t)
	}

	knownEntities := map[string]bool{}
	knownEnums := map[string]bool{}
	for _, name := range names {
		switch kindOf[name] {
		case "entity":
			knownEntities[name] = true
		case "enum":
			knownEnums[name] = true
		}
	}

	// Enums.
	for _, name := range names {
		if kindOf[name] != "enum" {
			continue
		}
		p := "components.schemas." + name
		obj := schemas[name].(map[string]any)
		valuesRaw, ok := asSlice(obj["enum"])
		if !ok || len(valuesRaw) == 0 {
			c.add(IssueInvalidType, p+".enum", "enum %q requires a non-empty enum array", name)
			continue
		}
		enum := model.Enum{Name: name}
		seen := map[string]bool{}
		for i, vr := range valuesRaw {
			vp := fmt.Sprintf("%s.enum[%d]", p, i)
			val, ok := asString(vr)
			if !ok || val == "" {
				c.add(IssueInvalidType, vp, "enum value must be a non-empty string, got %s", jsonKind(vr))
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

	// Entities.
	for _, name := range names {
		if kindOf[name] != "entity" {
			continue
		}
		p := "components.schemas." + name
		obj := schemas[name].(map[string]any)
		entity := model.Entity{Name: name}

		required := map[string]bool{}
		if r, ok := asSlice(obj["required"]); ok {
			for _, rv := range r {
				if s, ok := asString(rv); ok {
					required[s] = true
				}
			}
		} else if obj["required"] != nil {
			c.add(IssueInvalidSyntax, p+".required", "required must be an array, got %s", jsonKind(obj["required"]))
		}

		props, propsOK := asObject(obj["properties"])
		if obj["properties"] != nil && !propsOK {
			c.add(IssueInvalidSyntax, p+".properties", "properties must be an object, got %s", jsonKind(obj["properties"]))
			continue
		}
		fieldNames := map[string]bool{}
		for _, fname := range sortedKeys(props) {
			fp := fmt.Sprintf("%s.properties.%s", p, fname)
			if !isGoIdentifier(fname) {
				c.add(IssueInvalidName, fp+".name", "field name %q in entity %q must be a valid Go identifier", fname, name)
				continue
			}
			if fieldNames[fname] {
				c.add(IssueNameConflict, fp, "entity %q declares field %q more than once", name, fname)
				continue
			}
			fieldNames[fname] = true
			pspec, ok := asObject(props[fname])
			if !ok {
				c.add(IssueInvalidType, fp, "property must be an object, got %s", jsonKind(props[fname]))
				continue
			}
			field := model.Field{Name: fname, Optional: !required[fname]}
			if desc, ok := asString(pspec["description"]); ok {
				field.Note = desc
			}
			ft, ok := parseOpenAPIType(c, pspec, fp, knownEntities, knownEnums)
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

// parseOpenAPIType parses a single OpenAPI schema object used as a property.
func parseOpenAPIType(c *collector, obj map[string]any, path string, entities, enums map[string]bool) (*model.Type, bool) {
	if ref, ok := nonEmptyStringField(obj, "$ref"); ok {
		name, err := refTarget(ref)
		if err != nil {
			c.add(IssueInvalidType, path+".$ref", "%v", err)
			return nil, false
		}
		switch {
		case entities[name]:
			return &model.Type{Kind: model.KindRef, RefName: name}, true
		case enums[name]:
			return &model.Type{Kind: model.KindEnum, EnumName: name}, true
		default:
			c.add(IssueUnknownRef, path+".$ref", "reference %q does not point to a known schema", ref)
			return nil, false
		}
	}

	t, _ := nonEmptyStringField(obj, "type")
	if _, hasEnum := obj["enum"]; hasEnum {
		if t != "" && t != "string" {
			c.add(IssueInvalidType, path+".type", "inline enum must have type string, got %q", t)
			return nil, false
		}
		valuesRaw, ok := asSlice(obj["enum"])
		if !ok || len(valuesRaw) == 0 {
			c.add(IssueInvalidType, path+".enum", "inline enum requires a non-empty enum array")
			return nil, false
		}
		ty := &model.Type{Kind: model.KindEnum}
		seen := map[string]bool{}
		for i, vr := range valuesRaw {
			vp := fmt.Sprintf("%s.enum[%d]", path, i)
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
			ty.Values = append(ty.Values, val)
		}
		return ty, true
	}

	switch t {
	case "array":
		items, ok := asObject(obj["items"])
		if !ok {
			c.add(IssueMissingType, path+".items", "array property requires an items schema")
			return nil, false
		}
		elem, ok := parseOpenAPIType(c, items, path+".items", entities, enums)
		if !ok {
			return nil, false
		}
		return &model.Type{Kind: model.KindArray, Elem: elem}, true
	case "":
		c.add(IssueMissingType, path+".type", "property is missing a type or $ref")
		return nil, false
	default:
		prim, ok := canonicalPrimitive(t)
		if !ok {
			c.add(IssueInvalidType, path+".type", "unsupported OpenAPI type %q", t)
			return nil, false
		}
		return &model.Type{Kind: model.KindPrimitive, Primitive: prim}, true
	}
}

// refTarget resolves an OpenAPI local reference of the form
// "#/components/schemas/Name" to its schema name.
func refTarget(ref string) (string, error) {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("unsupported $ref %q: only local %s* references are supported", ref, prefix)
	}
	name := strings.TrimPrefix(ref, prefix)
	if name == "" || strings.ContainsAny(name, "/~") {
		return "", fmt.Errorf("invalid $ref %q", ref)
	}
	return name, nil
}
