package schema

import (
	"fmt"
	"strings"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// 支持的 OpenAPI 3.0 子集示例：
//
//	{
//	  "openapi": "3.0.0",
//	  "info": {"version": "2.0.0"},
//	  "components": {
//	    "schemas": {
//	      "Status": {"type": "string", "enum": ["ACTIVE", "INACTIVE"]},
//	      "User": {
//	        "type": "object",
//	        "required": ["id"],
//	        "properties": {
//	          "id": {"type": "string"},
//	          "status": {"$ref": "#/components/schemas/Status"}
//	        }
//	      }
//	    }
//	  }
//	}
//
// 归一化约定：
//   - version 取 info.version；
//   - type:string 且带 enum 的组件归为枚举，其余 type:object 组件归为实体；
//   - 属性仅支持原始类型或同 components.schemas 下的 $ref；
//   - 属性名必须是合法 Go 导出标识符；
//   - required 数组中出现的字段标记为非可选，其余为可选。
func parseOpenAPI(root map[string]any) (*model.Schema, []Issue) {
	var issues []Issue
	b := newBuilder()

	if infoRaw, ok := root["info"]; ok {
		info, ok := asObject(infoRaw)
		if !ok {
			issues = append(issues, issue(KindInvalidShape, "info", "info 必须是 JSON 对象"))
		} else if v, ok := stringAttr(info, "version"); !ok || strings.TrimSpace(v) == "" {
			issues = append(issues, issue(KindMissingVersion, "info.version", "info.version 必须是非空字符串"))
		} else {
			b.version = strings.TrimSpace(v)
		}
	} else {
		issues = append(issues, issue(KindMissingVersion, "info.version", "缺少 info.version"))
	}

	var schemas map[string]any
	compsRaw, hasComps := root["components"]
	if hasComps {
		comps, ok := asObject(compsRaw)
		if !ok {
			issues = append(issues, issue(KindInvalidShape, "components", "components 必须是 JSON 对象"))
		} else if sRaw, ok := comps["schemas"]; ok {
			schemas, ok = asObject(sRaw)
			if !ok {
				issues = append(issues, issue(KindInvalidShape, "components.schemas", "components.schemas 必须是 JSON 对象"))
			}
		}
	}

	// 第一轮：登记全部名称，使 $ref 解析与字段出现顺序无关。
	for _, name := range sortedKeys(schemas) {
		path := "components.schemas." + name
		if strings.TrimSpace(name) == "" || !validExportedName(name) {
			issues = append(issues, issue(KindInvalidName, path, fmt.Sprintf("组件名 %q 不是合法的 Go 导出标识符", name)))
			continue
		}
		b.declareComponentName(name)
	}

	// 第二轮：先解析全部枚举（string + enum），再解析实体（object），
	// 使实体属性对枚举的 $ref 解析与组件声明顺序无关。
	for _, pass := range []struct {
		wantType string
	}{{"string"}, {"object"}} {
		for _, name := range sortedKeys(schemas) {
			path := "components.schemas." + name
			if !validExportedName(name) {
				continue // 第一轮已记录
			}
			obj, ok := asObject(schemas[name])
			if !ok {
				if pass.wantType == "string" {
					issues = append(issues, issue(KindEmptyEntry, path, "schema 组件必须是 JSON 对象"))
				}
				continue
			}
			t, _ := stringAttr(obj, "type")
			isEnum := t == "string" && obj["enum"] != nil
			if pass.wantType == "string" && !isEnum {
				continue
			}
			if pass.wantType == "object" {
				if isEnum {
					continue
				}
				if t != "object" {
					issues = append(issues, issue(KindInvalidType, path+".type",
						fmt.Sprintf("组件 %s 的类型 %q 不受支持（本子集仅支持 object 实体与 string 枚举）", name, t)))
					continue
				}
				fields, fIssues := parseOpenAPIEntity(obj, path, name, b)
				issues = append(issues, fIssues...)
				b.classifyEntity(name, fields)
				continue
			}
			// 枚举解析
			values, ok := asStringArray(obj["enum"])
			if !ok {
				issues = append(issues, issue(KindInvalidEnumValue, path+".enum", "枚举组件的 enum 必须是字符串数组"))
				continue
			}
			if len(values) == 0 {
				issues = append(issues, issue(KindInvalidEnumValue, path+".enum", fmt.Sprintf("枚举 %s 至少需要一个取值", name)))
				continue
			}
			seen := map[string]bool{}
			cleaned := make([]string, 0, len(values))
			for _, v := range values {
				if strings.TrimSpace(v) == "" {
					issues = append(issues, issue(KindInvalidEnumValue, path+".enum", fmt.Sprintf("枚举 %s 包含空取值", name)))
					continue
				}
				if seen[v] {
					issues = append(issues, issue(KindDuplicateEnumVal, path+".enum", fmt.Sprintf("枚举 %s 的取值 %q 重复", name, v)))
					continue
				}
				seen[v] = true
				cleaned = append(cleaned, v)
			}
			b.classifyEnum(name, cleaned)
		}
	}

	return b.build(), append(issues, b.issues()...)
}

func parseOpenAPIEntity(obj map[string]any, path, entityName string, b *builder) ([]model.Field, []Issue) {
	var issues []Issue
	required := map[string]bool{}
	if rRaw, ok := obj["required"]; ok {
		arr, ok := rRaw.([]any)
		if !ok {
			issues = append(issues, issue(KindInvalidShape, path+".required", "required 必须是数组"))
		} else {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					required[s] = true
				} else {
					issues = append(issues, issue(KindInvalidName, path+".required", "required 数组只能包含字符串"))
				}
			}
		}
	}

	propsRaw, hasProps := obj["properties"]
	if !hasProps {
		issues = append(issues, issue(KindInvalidShape, path+".properties", fmt.Sprintf("实体 %s 缺少 properties", entityName)))
		return nil, issues
	}
	props, ok := asObject(propsRaw)
	if !ok {
		issues = append(issues, issue(KindInvalidShape, path+".properties", "properties 必须是 JSON 对象"))
		return nil, issues
	}

	var fields []model.Field
	seen := map[string]bool{}
	for _, name := range sortedKeys(props) {
		fpath := path + ".properties." + name
		if strings.TrimSpace(name) == "" || !validExportedName(name) {
			issues = append(issues, issue(KindInvalidName, fpath, fmt.Sprintf("字段名 %q 不是合法的 Go 导出标识符", name)))
			continue
		}
		if seen[name] {
			// JSON 对象中键天然唯一，此分支仅作防御。
			issues = append(issues, issue(KindDuplicateField, fpath, fmt.Sprintf("字段 %q 重复定义", name)))
			continue
		}
		pm, ok := asObject(props[name])
		if !ok {
			issues = append(issues, issue(KindEmptyEntry, fpath, "属性必须是 JSON 对象"))
			continue
		}
		var f model.Field
		if ref, ok := stringAttr(pm, "$ref"); ok {
			const prefix = "#/components/schemas/"
			if !strings.HasPrefix(ref, prefix) {
				issues = append(issues, issue(KindUnknownEnumRef, fpath+".$ref",
					fmt.Sprintf("字段 %s 的引用 %q 不受支持（仅允许 %s 下的本地引用）", name, ref, prefix)))
				continue
			}
			target := strings.TrimPrefix(ref, prefix)
			if b.kindOf(target) != compEnum {
				issues = append(issues, issue(KindInvalidType, fpath+".$ref",
					fmt.Sprintf("字段 %s 引用的 %s 不是 string 枚举（本子集不支持对象/数组引用）", name, target)))
				continue
			}
			f = model.Field{Name: name, Type: model.TypeString, EnumRef: target}
		} else {
			t, ok := stringAttr(pm, "type")
			if !ok {
				issues = append(issues, issue(KindInvalidType, fpath+".type", fmt.Sprintf("字段 %s 缺少 type 或 $ref", name)))
				continue
			}
			pt := model.PrimitiveType(t)
			if !model.IsValidPrimitive(pt) {
				issues = append(issues, issue(KindInvalidType, fpath+".type",
					fmt.Sprintf("字段 %s 的类型 %q 不受支持（允许: string/integer/number/boolean）", name, t)))
				continue
			}
			f = model.Field{Name: name, Type: pt}
		}
		// OpenAPI 语义：出现在 required 中表示必填，否则可选。
		f.Optional = !required[name]
		seen[name] = true
		fields = append(fields, f)
	}

	// required 中引用了不存在的字段属于畸形输入，必须回报。
	for r := range required {
		if !seen[r] {
			issues = append(issues, issue(KindMissingFieldName, path+".required",
				fmt.Sprintf("实体 %s 的 required 引用了不存在的字段 %q", entityName, r)))
		}
	}
	return fields, issues
}
