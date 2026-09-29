package schema

import "testing"

const validOpenAPI = `{
  "openapi": "3.0.0",
  "info": {"version": "2.1.0"},
  "components": {"schemas": {
    "Status": {"type": "string", "enum": ["ACTIVE", "INACTIVE"]},
    "User": {
      "type": "object",
      "required": ["Id"],
      "properties": {
        "Id": {"type": "string"},
        "Age": {"type": "integer"},
        "State": {"$ref": "#/components/schemas/Status"}
      }
    }
  }}
}`

func TestNormalizeOpenAPIOK(t *testing.T) {
	t.Logf("输入 dialect=auto schema=%s", compact(validOpenAPI))
	r := Normalize([]byte(validOpenAPI), DialectAuto)
	if r.Dialect != DialectOpenAPI {
		t.Fatalf("嗅探方言 = %q, 期望 openapi", r.Dialect)
	}
	if r.HasErrors() {
		for _, is := range r.Issues {
			t.Errorf("[%s] %s: %s", is.Kind, is.Path, is.Message)
		}
		t.Fatalf("合法 OpenAPI 不应报错")
	}
	if r.Schema.Version != "2.1.0" {
		t.Fatalf("version = %q", r.Schema.Version)
	}
	u := r.Schema.Entity("User")
	if u == nil {
		t.Fatalf("缺少实体 User")
	}
	if f := u.Field("Id"); f == nil || f.Optional {
		t.Fatalf("Id 应为必填字段, got=%v", f)
	}
	if f := u.Field("Age"); f == nil || !f.Optional {
		t.Fatalf("Age 应为可选字段, got=%v", f)
	}
	if f := u.Field("State"); f == nil || f.EnumRef != "Status" || f.Type != "string" {
		t.Fatalf("State 应为 string 枚举引用, got=%v", f)
	}
}

func TestNormalizeOpenAPIOrderIndependent(t *testing.T) {
	// 实体在枚举之前声明，$ref 仍应正确解析。
	input := `{
  "openapi": "3.0.0",
  "info": {"version": "1"},
  "components": {"schemas": {
    "User": {"type": "object", "properties": {
      "State": {"$ref": "#/components/schemas/Status"}
    }},
    "Status": {"type": "string", "enum": ["A", "B"]}
  }}
}`
	r := Normalize([]byte(input), DialectOpenAPI)
	if r.HasErrors() {
		t.Fatalf("不应因声明顺序报错: %v", r.Issues)
	}
	if f := r.Schema.Entity("User").Field("State"); f == nil || f.EnumRef != "Status" {
		t.Fatalf("State 应解析到枚举 Status, got=%v", f)
	}
}

func TestNormalizeOpenAPIMalformed(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Kind
	}{
		{
			name:  "缺少 info.version",
			input: `{"openapi":"3.0.0","components":{"schemas":{}}}`,
			want:  KindMissingVersion,
		},
		{
			name: "引用非枚举组件",
			input: `{"openapi":"3.0.0","info":{"version":"1"},"components":{"schemas":{
			  "Other":{"type":"object","properties":{}},
			  "User":{"type":"object","properties":{"Ref":{"$ref":"#/components/schemas/Other"}}}
			}}}`,
			want: KindInvalidType,
		},
		{
			name: "required 引用不存在字段",
			input: `{"openapi":"3.0.0","info":{"version":"1"},"components":{"schemas":{
			  "User":{"type":"object","required":["Ghost"],"properties":{}}
			}}}`,
			want: KindMissingFieldName,
		},
		{
			name: "不支持的属性类型 array",
			input: `{"openapi":"3.0.0","info":{"version":"1"},"components":{"schemas":{
			  "User":{"type":"object","properties":{"Tags":{"type":"array"}}}
			}}}`,
			want: KindInvalidType,
		},
		{
			name: "不支持的顶层组件类型 integer",
			input: `{"openapi":"3.0.0","info":{"version":"1"},"components":{"schemas":{
			  "Counter":{"type":"integer"}
			}}}`,
			want: KindInvalidType,
		},
		{
			name: "非法跨文件引用",
			input: `{"openapi":"3.0.0","info":{"version":"1"},"components":{"schemas":{
			  "User":{"type":"object","properties":{"X":{"$ref":"other.yaml#/components/schemas/X"}}}
			}}}`,
			want: KindUnknownEnumRef,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("输入=%s", compact(tc.input))
			r := Normalize([]byte(tc.input), DialectOpenAPI)
			if !r.HasErrors() {
				t.Fatalf("期望报错 %s", tc.want)
			}
			ok := false
			for _, is := range r.Issues {
				t.Logf("  归类 [%s] 位置=%s 原因=%s", is.Kind, is.Path, is.Message)
				if is.Kind == tc.want {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("期望归类 %s", tc.want)
			}
		})
	}
}
