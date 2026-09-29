package schema

import (
	"fmt"
	"strings"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// simple 方言示例：
//
//	{
//	  "version": "1.0.0",
//	  "enums": [
//	    {"name": "Status", "values": ["ACTIVE", "INACTIVE"]}
//	  ],
//	  "entities": [
//	    {"name": "User", "fields": [
//	      {"name": "id", "type": "string"},
//	      {"name": "age", "type": "integer", "optional": true},
//	      {"name": "status", "type": "string", "enum": "Status"}
//	    ]}
//	  ]
//	}
func parseSimple(root map[string]any) (*model.Schema, []Issue) {
	var issues []Issue
	b := newBuilder()

	version, ok := root["version"]
	if !ok {
		issues = append(issues, issue(KindMissingVersion, "version", "缺少 version 字段"))
	} else {
		sv, ok := version.(string)
		if !ok || strings.TrimSpace(sv) == "" {
			issues = append(issues, issue(KindMissingVersion, "version", "version 必须是非空字符串"))
		} else {
			b.version = strings.TrimSpace(sv)
		}
	}

	if rawEnums, present := root["enums"]; present {
		arr, ok := rawEnums.([]any)
		if !ok {
			issues = append(issues, issue(KindInvalidShape, "enums", "enums 必须是数组"))
		} else {
			for i, e := range arr {
				path := fmt.Sprintf("enums.%d", i)
				em, ok := asObject(e)
				if !ok {
					issues = append(issues, issue(KindEmptyEntry, path, "枚举条目必须是 JSON 对象"))
					continue
				}
				name, ok := stringAttr(em, "name")
				if !ok {
					issues = append(issues, issue(KindInvalidName, path, "枚举条目缺少字符串类型的 name"))
					continue
				}
				if strings.TrimSpace(name) == "" {
					issues = append(issues, issue(KindInvalidName, path+".name", "枚举 name 不能为空"))
					continue
				}
				if !validExportedName(name) {
					issues = append(issues, issue(KindInvalidName, path+".name", fmt.Sprintf("枚举名 %q 不是合法的 Go 导出标识符", name)))
					continue
				}
				values, ok := asStringArray(em["values"])
				if !ok {
					issues = append(issues, issue(KindInvalidEnumValue, path+".values", "枚举 values 必须是字符串数组"))
					continue
				}
				if len(values) == 0 {
					issues = append(issues, issue(KindInvalidEnumValue, path+".values", fmt.Sprintf("枚举 %s 至少需要一个取值", name)))
					continue
				}
				seen := map[string]bool{}
				cleaned := make([]string, 0, len(values))
				valuePath := path + ".values"
				for _, v := range values {
					if strings.TrimSpace(v) == "" {
						issues = append(issues, issue(KindInvalidEnumValue, valuePath, fmt.Sprintf("枚举 %s 包含空取值", name)))
						continue
					}
					if seen[v] {
						issues = append(issues, issue(KindDuplicateEnumVal, valuePath, fmt.Sprintf("枚举 %s 的取值 %q 重复", name, v)))
						continue
					}
					seen[v] = true
					cleaned = append(cleaned, v)
				}
				if !b.declareEnum(name, path) {
					// 冲突 issue 已由 builder 记录，跳过该条目。
					continue
				}
				if len(cleaned) > 0 {
					b.addEnum(name, cleaned)
				}
			}
		}
	}

	if rawEntities, present := root["entities"]; present {
		arr, ok := rawEntities.([]any)
		if !ok {
			issues = append(issues, issue(KindInvalidShape, "entities", "entities 必须是数组"))
		} else {
			for i, e := range arr {
				path := fmt.Sprintf("entities.%d", i)
				em, ok := asObject(e)
				if !ok {
					issues = append(issues, issue(KindEmptyEntry, path, "实体条目必须是 JSON 对象"))
					continue
				}
				name, ok := stringAttr(em, "name")
				if !ok {
					issues = append(issues, issue(KindInvalidName, path, "实体条目缺少字符串类型的 name"))
					continue
				}
				if strings.TrimSpace(name) == "" {
					issues = append(issues, issue(KindInvalidName, path+".name", "实体 name 不能为空"))
					continue
				}
				if !validExportedName(name) {
					issues = append(issues, issue(KindInvalidName, path+".name", fmt.Sprintf("实体名 %q 不是合法的 Go 导出标识符", name)))
					continue
				}
				// 先登记名称：冲突（与枚举同名或实体重复定义）的条目不收集，
				// 但其字段仍继续检查，保证同一文档内的其他问题不会被掩盖。
				// 冲突 issue 已由 builder 记录。
				nameConflict := !b.declareEntity(name, path)
				fields, fieldIssues := parseSimpleFields(em, path, b)
				issues = append(issues, fieldIssues...)
				if !nameConflict {
					b.addEntity(name, fields)
				}
			}
		}
	}

	return b.build(), append(issues, b.issues()...)
}

func parseSimpleFields(em map[string]any, entityPath string, b *builder) ([]model.Field, []Issue) {
	var issues []Issue
	rawFields, present := em["fields"]
	if !present {
		issues = append(issues, issue(KindInvalidShape, entityPath+".fields", "实体缺少 fields 数组"))
		return nil, issues
	}
	arr, ok := rawFields.([]any)
	if !ok {
		issues = append(issues, issue(KindInvalidShape, entityPath+".fields", "fields 必须是数组"))
		return nil, issues
	}
	var fields []model.Field
	seen := map[string]bool{}
	for i, f := range arr {
		path := fmt.Sprintf("%s.fields.%d", entityPath, i)
		fm, ok := asObject(f)
		if !ok {
			issues = append(issues, issue(KindEmptyEntry, path, "字段条目必须是 JSON 对象"))
			continue
		}
		name, ok := stringAttr(fm, "name")
		if !ok {
			issues = append(issues, issue(KindMissingFieldName, path, "字段缺少字符串类型的 name"))
			continue
		}
		if strings.TrimSpace(name) == "" {
			issues = append(issues, issue(KindMissingFieldName, path+".name", "字段 name 不能为空"))
			continue
		}
		if !validExportedName(name) {
			issues = append(issues, issue(KindInvalidName, path+".name", fmt.Sprintf("字段名 %q 不是合法的 Go 导出标识符", name)))
			continue
		}
		if seen[name] {
			issues = append(issues, issue(KindDuplicateField, path, fmt.Sprintf("字段 %q 在同一实体中重复定义", name)))
			continue
		}
		t, ok := stringAttr(fm, "type")
		if !ok {
			issues = append(issues, issue(KindInvalidType, path+".type", fmt.Sprintf("字段 %s 缺少字符串类型的 type", name)))
			continue
		}
		pt := model.PrimitiveType(t)
		if !model.IsValidPrimitive(pt) {
			issues = append(issues, issue(KindInvalidType, path+".type", fmt.Sprintf("字段 %s 的类型 %q 不受支持（允许: string/integer/number/boolean）", name, t)))
			continue
		}
		var enumRef string
		if rawEnum, hasEnum := fm["enum"]; hasEnum {
			ref, ok := rawEnum.(string)
			if !ok {
				issues = append(issues, issue(KindUnknownEnumRef, path+".enum", fmt.Sprintf("字段 %s 的 enum 必须是字符串", name)))
				continue
			}
			if pt != model.TypeString {
				issues = append(issues, issue(KindInvalidType, path+".type", fmt.Sprintf("字段 %s 引用枚举 %s，但底层类型为 %s（枚举引用必须为 string）", name, ref, pt)))
				continue
			}
			if !b.hasEnum(ref) {
				issues = append(issues, issue(KindUnknownEnumRef, path+".enum", fmt.Sprintf("字段 %s 引用了未定义的枚举 %q", name, ref)))
				continue
			}
			enumRef = ref
		}
		optional := false
		if rawOpt, hasOpt := fm["optional"]; hasOpt {
			v, ok := rawOpt.(bool)
			if !ok {
				issues = append(issues, issue(KindInvalidType, path+".optional", fmt.Sprintf("字段 %s 的 optional 必须是布尔值", name)))
				continue
			}
			optional = v
		}
		seen[name] = true
		fields = append(fields, model.Field{Name: name, Type: pt, Optional: optional, EnumRef: enumRef})
	}
	return fields, issues
}

func stringAttr(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func issue(k Kind, path, msg string) Issue {
	return Issue{Kind: k, Severity: SeverityError, Path: path, Message: msg}
}
