// Package schema 负责把不同来源、不同写法的原始 schema 归一化到
// 统一内部模型 internal/model。
//
// 归一化过程中遇到的任何问题（字段缺失、类型非法、命名冲突等）
// 都不会被静默跳过，而是按原因分类记录到 Report 中返回。
//
// 问题清单在返回前会按 (Path, Kind, Message) 排序，因此同一份
// 输入无论解析多少次，报告的内容与顺序都是确定的。
package schema

import (
	"bytes"
	"encoding/json"
	"sort"
	"unicode"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// Dialect 表示原始 schema 的写法（方言）。
type Dialect string

const (
	// DialectSimple 是本工具约定的简化写法。
	DialectSimple Dialect = "simple"
	// DialectOpenAPI 是 OpenAPI 3.0 组件定义的一个子集写法。
	DialectOpenAPI Dialect = "openapi"
	// DialectAuto 表示根据内容自动嗅探方言。
	DialectAuto Dialect = "auto"
)

// Kind 是归一化问题的原因分类。
type Kind string

const (
	KindInvalidJSON      Kind = "invalid_json"
	KindInvalidShape     Kind = "invalid_shape"
	KindUnknownDialect   Kind = "unknown_dialect"
	KindMissingVersion   Kind = "missing_version"
	KindInvalidName      Kind = "invalid_name"
	KindEmptyEntry       Kind = "empty_entry"
	KindDuplicateName    Kind = "duplicate_name"
	KindMissingFieldName Kind = "missing_field_name"
	KindDuplicateField   Kind = "duplicate_field"
	KindInvalidType      Kind = "invalid_type"
	KindUnknownEnumRef   Kind = "unknown_enum_ref"
	KindInvalidEnumValue Kind = "invalid_enum_value"
	KindDuplicateEnumVal Kind = "duplicate_enum_value"
)

// Severity 表示问题的严重级别。
type Severity string

const (
	SeverityError Severity = "error"
)

// Issue 描述归一化过程中发现的一个问题。
type Issue struct {
	Kind     Kind
	Severity Severity
	// Path 是问题条目在原始文档中的定位（如 "entities.User.fields.0"）。
	Path string
	// Message 是可读的原因说明。
	Message string
}

// Report 汇总一次归一化的产物与问题清单。
type Report struct {
	Dialect Dialect
	// Schema 是归一化产物；存在 error 级问题时为 nil，
	// 调用方必须先检查 Issues / HasErrors。
	Schema *model.Schema
	Issues []Issue
}

// HasErrors 判断报告中是否存在 error 级问题。
func (r *Report) HasErrors() bool {
	for _, is := range r.Issues {
		if is.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Normalize 把一份原始 schema 数据归一化为内部模型。
// dialect 为 DialectAuto 时根据内容自动嗅探。
// 任何问题都会记录在返回的 Report 中，而不是被静默跳过。
func Normalize(data []byte, dialect Dialect) *Report {
	var root any
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil {
		return errorReport(dialect, Issue{
			Kind: KindInvalidJSON, Severity: SeverityError,
			Path: "$", Message: "输入不是合法 JSON: " + err.Error(),
		})
	}
	if _, ok := root.(map[string]any); !ok {
		return errorReport(dialect, Issue{
			Kind: KindInvalidJSON, Severity: SeverityError,
			Path: "$", Message: "schema 根节点必须是 JSON 对象",
		})
	}

	if dialect == DialectAuto {
		dialect = sniff(root.(map[string]any))
	}

	var sc *model.Schema
	var issues []Issue
	switch dialect {
	case DialectSimple:
		sc, issues = parseSimple(root.(map[string]any))
	case DialectOpenAPI:
		sc, issues = parseOpenAPI(root.(map[string]any))
	default:
		msg := "无法识别的 schema 方言: " + string(dialect)
		if dialect == "" {
			msg = "无法从内容自动识别 schema 方言（缺少 openapi/entities/enums 等顶层特征字段）"
		}
		return errorReport(dialect, Issue{
			Kind: KindUnknownDialect, Severity: SeverityError,
			Path: "$", Message: msg,
		})
	}

	sortIssues(issues)
	if len(issues) > 0 {
		return &Report{Dialect: dialect, Schema: nil, Issues: issues}
	}
	sc.Sort()
	return &Report{Dialect: dialect, Schema: sc}
}

// sniff 根据顶层特征字段嗅探方言；无法判定时返回 ""。
func sniff(root map[string]any) Dialect {
	if _, ok := root["openapi"]; ok {
		return DialectOpenAPI
	}
	if _, ok := root["entities"]; ok {
		return DialectSimple
	}
	if _, ok := root["enums"]; ok {
		return DialectSimple
	}
	return ""
}

func errorReport(d Dialect, issues ...Issue) *Report {
	sortIssues(issues)
	return &Report{Dialect: d, Schema: nil, Issues: issues}
}

func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Path != issues[j].Path {
			return issues[i].Path < issues[j].Path
		}
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		return issues[i].Message < issues[j].Message
	})
}

// validExportedName 判断名称是否可作为 Go 导出标识符
// （字母或下划线开头，仅含字母、数字、下划线，且首字母大写）。
func validExportedName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			if r != '_' && !unicode.IsUpper(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// asObject 断言 v 是否为 JSON 对象。
func asObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// asStringArray 断言 v 是否为字符串数组。
func asStringArray(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// sortedKeys 返回 map 的键按字典序排列后的列表，
// 避免 Go map 遍历随机顺序影响归一化确定性。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
