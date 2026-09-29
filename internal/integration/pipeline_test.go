// Package integration 是端到端集成测试：把不同写法的 schema
// 依次送入归一化、兼容性判定、代码生成三个环节，验证工具链主路径。
package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/codegen"
	"github.com/lisama569/versioned-sdk-codegen-go/internal/compat"
	"github.com/lisama569/versioned-sdk-codegen-go/internal/schema"
)

// 两份不同来源、同一业务的 schema：旧版用 simple 写法，新版用 OpenAPI 子集写法。
const oldSimple = `{
  "version": "1.0.0",
  "enums": [{"name": "Status", "values": ["ACTIVE", "INACTIVE"]}],
  "entities": [{"name": "User", "fields": [
    {"name": "Id", "type": "string"},
    {"name": "Status", "type": "string", "enum": "Status"}
  ]}]
}`

// 新版删了 INACTIVE 枚举值（收紧），并新增可选字段 Email。
const newOpenAPI = `{
  "openapi": "3.0.0",
  "info": {"version": "2.0.0"},
  "components": {"schemas": {
    "Status": {"type": "string", "enum": ["ACTIVE"]},
    "User": {"type": "object", "required": ["Id", "Status"], "properties": {
      "Id": {"type": "string"},
      "Email": {"type": "string"},
      "Status": {"$ref": "#/components/schemas/Status"}
    }}
  }}
}`

func TestCrossDialectPipeline(t *testing.T) {
	t.Logf("输入-旧(simple): %s", oldSimple)
	t.Logf("输入-新(openapi): %s", newOpenAPI)

	oldRep := schema.Normalize([]byte(oldSimple), schema.DialectAuto)
	if oldRep.HasErrors() {
		t.Fatalf("旧 schema 归一化失败: %v", oldRep.Issues)
	}
	newRep := schema.Normalize([]byte(newOpenAPI), schema.DialectAuto)
	if newRep.HasErrors() {
		t.Fatalf("新 schema 归一化失败: %v", newRep.Issues)
	}
	if oldRep.Dialect != schema.DialectSimple || newRep.Dialect != schema.DialectOpenAPI {
		t.Fatalf("方言嗅探错误: old=%s new=%s", oldRep.Dialect, newRep.Dialect)
	}

	rep := compat.Compare(oldRep.Schema, newRep.Schema)
	t.Logf("判定依据（共 %d 项变更）:", len(rep.Changes))
	for _, c := range rep.Changes {
		t.Logf("  [%s] %s: %s", c.Severity, c.Path, c.Message)
	}
	if rep.IsCompatible() {
		t.Fatalf("删除枚举取值 INACTIVE 必须判定为破坏性变更")
	}
	if rep.BreakingCount() != 1 {
		t.Fatalf("破坏性变更数 = %d，期望 1（仅枚举收紧；新增 Email 兼容）", rep.BreakingCount())
	}

	// 新版 schema 仍然可以成功生成客户端代码（破坏性不影响生成本身）。
	dir := filepath.Join(t.TempDir(), "sdk")
	res, err := codegen.New().Generate(context.Background(), newRep.Schema, dir, codegen.Options{PackageName: "sdk"})
	if err != nil {
		t.Fatalf("代码生成失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, res.Files[0])); err != nil {
		t.Fatalf("生成文件不存在: %v", err)
	}
	t.Logf("生成产物: %v", res.Files)
}

func TestMalformedDoesNotReachCompare(t *testing.T) {
	bad := `{"version": "1", "entities": [{"name": "user", "fields": []}]}`
	r := schema.Normalize([]byte(bad), schema.DialectSimple)
	if !r.HasErrors() || r.Schema != nil {
		t.Fatalf("畸形 schema 必须报错且不产出模型")
	}
	t.Logf("畸形输入被拦截，归类: %s（%s）", r.Issues[0].Kind, r.Issues[0].Message)
}
