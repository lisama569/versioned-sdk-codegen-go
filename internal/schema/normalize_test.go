package schema

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// validSimpleV1 是一份基线合法 simple schema，用例通过它做局部变异
// 构造畸形输入，避免每个用例重复整段 JSON。
const validSimpleV1 = `{
  "version": "1.0.0",
  "enums": [
    {"name": "Status", "values": ["ACTIVE", "INACTIVE"]}
  ],
  "entities": [
    {"name": "User", "fields": [
      {"name": "Id", "type": "string"},
      {"name": "Age", "type": "integer", "optional": true},
      {"name": "Status", "type": "string", "enum": "Status"}
    ]}
  ]
}`

func TestNormalizeSimpleOK(t *testing.T) {
	r := Normalize([]byte(validSimpleV1), DialectSimple)
	if r.HasErrors() {
		for _, is := range r.Issues {
			t.Errorf("unexpected issue: [%s] %s: %s", is.Kind, is.Path, is.Message)
		}
		t.Fatalf("baseline schema should normalize cleanly")
	}
	if r.Schema.Version != "1.0.0" {
		t.Fatalf("version = %q", r.Schema.Version)
	}
	if got := len(r.Schema.Entities); got != 1 || r.Schema.Entities[0].Name != "User" {
		t.Fatalf("entities = %v", r.Schema.Entities)
	}
	if e := r.Schema.Entity("User"); e == nil || len(e.Fields) != 3 {
		t.Fatalf("User fields = %v", e)
	}
	if e := r.Schema.Entity("User"); e.Field("Status").EnumRef != "Status" {
		t.Fatalf("Status enum ref missing")
	}
}

func TestNormalizeMalformed(t *testing.T) {
	cases := []struct {
		name    string
		dialect Dialect
		input   string
		want    Kind
	}{
		{
			name:    "非法 JSON",
			dialect: DialectSimple,
			input:   `{"version": }`,
			want:    KindInvalidJSON,
		},
		{
			name:    "根节点非对象",
			dialect: DialectSimple,
			input:   `[1,2,3]`,
			want:    KindInvalidJSON,
		},
		{
			name:    "缺少 version",
			dialect: DialectSimple,
			input:   `{"entities": []}`,
			want:    KindMissingVersion,
		},
		{
			name:    "version 为空白",
			dialect: DialectSimple,
			input:   `{"version": "   ", "entities": []}`,
			want:    KindMissingVersion,
		},
		{
			name:    "实体名小写不合法",
			dialect: DialectSimple,
			input: `{"version": "1", "entities": [
			  {"name": "user", "fields": []}
			]}`,
			want: KindInvalidName,
		},
		{
			name:    "枚举名含非法字符",
			dialect: DialectSimple,
			input: `{"version": "1", "enums": [
			  {"name": "Bad-Name", "values": ["A"]}
			]}`,
			want: KindInvalidName,
		},
		{
			name:    "实体与枚举命名冲突",
			dialect: DialectSimple,
			input: `{"version": "1",
			  "enums": [{"name": "Thing", "values": ["A"]}],
			  "entities": [{"name": "Thing", "fields": []}]
			}`,
			want: KindDuplicateName,
		},
		{
			name:    "实体重复定义",
			dialect: DialectSimple,
			input: `{"version": "1", "entities": [
			  {"name": "Dup", "fields": []},
			  {"name": "Dup", "fields": []}
			]}`,
			want: KindDuplicateName,
		},
		{
			name:    "字段缺少 name",
			dialect: DialectSimple,
			input: `{"version": "1", "entities": [
			  {"name": "E", "fields": [{"type": "string"}]}
			]}`,
			want: KindMissingFieldName,
		},
		{
			name:    "同实体内字段重复",
			dialect: DialectSimple,
			input: `{"version": "1", "entities": [
			  {"name": "E", "fields": [
			    {"name": "F", "type": "string"},
			    {"name": "F", "type": "integer"}
			  ]}
			]}`,
			want: KindDuplicateField,
		},
		{
			name:    "字段类型非法",
			dialect: DialectSimple,
			input: `{"version": "1", "entities": [
			  {"name": "E", "fields": [{"name": "F", "type": "datetime"}]}
			]}`,
			want: KindInvalidType,
		},
		{
			name:    "字段缺少 type",
			dialect: DialectSimple,
			input: `{"version": "1", "entities": [
			  {"name": "E", "fields": [{"name": "F"}]}
			]}`,
			want: KindInvalidType,
		},
		{
			name:    "枚举引用未定义",
			dialect: DialectSimple,
			input: `{"version": "1", "entities": [
			  {"name": "E", "fields": [{"name": "F", "type": "string", "enum": "Nope"}]}
			]}`,
			want: KindUnknownEnumRef,
		},
		{
			name:    "枚举引用字段底层类型不是 string",
			dialect: DialectSimple,
			input: `{"version": "1",
			  "enums": [{"name": "S", "values": ["A"]}],
			  "entities": [{"name": "E", "fields": [
			    {"name": "F", "type": "integer", "enum": "S"}
			  ]}]}`,
			want: KindInvalidType,
		},
		{
			name:    "枚举取值重复",
			dialect: DialectSimple,
			input: `{"version": "1", "enums": [
			  {"name": "S", "values": ["A", "A"]}
			]}`,
			want: KindDuplicateEnumVal,
		},
		{
			name:    "枚举取值为空",
			dialect: DialectSimple,
			input: `{"version": "1", "enums": [
			  {"name": "S", "values": ["A", ""]}
			]}`,
			want: KindInvalidEnumValue,
		},
		{
			name:    "枚举值列表为空",
			dialect: DialectSimple,
			input: `{"version": "1", "enums": [
			  {"name": "S", "values": []}
			]}`,
			want: KindInvalidEnumValue,
		},
		{
			name:    "enums 结构错误",
			dialect: DialectSimple,
			input:   `{"version": "1", "enums": {}}`,
			want:    KindInvalidShape,
		},
		{
			name:    "自动嗅探失败",
			dialect: DialectAuto,
			input:   `{"version": "1", "unknown": true}`,
			want:    KindUnknownDialect,
		},
		{
			name:    "显式指定未知方言",
			dialect: Dialect("protobuf"),
			input:   `{"version": "1"}`,
			want:    KindUnknownDialect,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 打印输入，满足边界用例可追溯要求。
			t.Logf("输入 dialect=%s schema=%s", tc.dialect, compact(tc.input))
			r := Normalize([]byte(tc.input), tc.dialect)
			if !r.HasErrors() {
				t.Fatalf("期望报错 %s，但归一化成功", tc.want)
			}
			found := false
			for _, is := range r.Issues {
				// 打印每个问题的判定依据。
				t.Logf("  问题归类 [%s] 位置=%s 原因=%s", is.Kind, is.Path, is.Message)
				if is.Kind == tc.want {
					found = true
				}
			}
			if !found {
				var kinds []string
				for _, is := range r.Issues {
					kinds = append(kinds, string(is.Kind))
				}
				t.Fatalf("期望问题归类 %s，实际归类: %s", tc.want, strings.Join(kinds, ", "))
			}
			if r.Schema != nil {
				t.Fatalf("存在错误时 Schema 必须为 nil，避免半截模型被消费")
			}
		})
	}
}

func TestNormalizeDeterministic(t *testing.T) {
	// 字段与条目顺序打乱，归一化结果必须一致。
	shuffled := `{
	  "version": "1.0.0",
	  "entities": [
	    {"name": "Order", "fields": [
	      {"name": "Total", "type": "number"},
	      {"name": "Id", "type": "string"}
	    ]},
	    {"name": "User", "fields": [
	      {"name": "Name", "type": "string"}
	    ]}
	  ],
	  "enums": [
	    {"name": "Color", "values": ["GREEN", "BLUE", "RED"]}
	  ]
	}`
	r1 := Normalize([]byte(shuffled), DialectSimple)
	r2 := Normalize([]byte(shuffled), DialectSimple)
	if r1.HasErrors() {
		t.Fatalf("unexpected issues: %v", r1.Issues)
	}
	if dump(r1.Schema) != dump(r2.Schema) {
		t.Fatalf("重复归一化结果不一致:\n%s\n!=\n%s", dump(r1.Schema), dump(r2.Schema))
	}
	want := "enum Color=[BLUE GREEN RED]"
	if !strings.Contains(dump(r1.Schema), want) {
		t.Fatalf("枚举值未排序: %s", dump(r1.Schema))
	}
}

func compact(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

func dump(sc *model.Schema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version=%s\n", sc.Version)
	for _, e := range sc.Enums {
		fmt.Fprintf(&b, "enum %s=%v\n", e.Name, e.Values)
	}
	for _, e := range sc.Entities {
		for _, f := range e.Fields {
			fmt.Fprintf(&b, "field %s.%s type=%s optional=%v enum=%s\n",
				e.Name, f.Name, f.Type, f.Optional, f.EnumRef)
		}
	}
	return b.String()
}
