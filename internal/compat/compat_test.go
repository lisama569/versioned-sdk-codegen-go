package compat

import (
	"strings"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

func schemaV1() *model.Schema {
	return &model.Schema{
		Version: "1.0.0",
		Enums: []model.Enum{
			{Name: "Status", Values: []string{"ACTIVE", "INACTIVE"}},
			{Name: "Color", Values: []string{"RED", "GREEN", "BLUE"}},
		},
		Entities: []model.Entity{
			{Name: "User", Fields: []model.Field{
				{Name: "Id", Type: model.TypeString, Optional: false},
				{Name: "Age", Type: model.TypeInteger, Optional: true},
				{Name: "State", Type: model.TypeString, Optional: true, EnumRef: "Status"},
			}},
			{Name: "Legacy", Fields: []model.Field{{Name: "X", Type: model.TypeString}}},
		},
	}
}

func TestCompareCompatibleChanges(t *testing.T) {
	old := schemaV1()
	new := schemaV1()
	new.Version = "1.1.0"
	// 新增字段（必填新增也算兼容；删除才破坏）
	new.Entities[0].Fields = append(new.Entities[0].Fields,
		model.Field{Name: "Email", Type: model.TypeString, Optional: true})
	// 必填 -> 可选（放宽）
	new.Entities[0].Fields[0].Optional = true
	// 枚举新增取值
	new.Enums[0].Values = append(new.Enums[0].Values, "PENDING")
	// 新增实体与枚举
	new.Entities = append(new.Entities, model.Entity{Name: "Order", Fields: []model.Field{
		{Name: "Id", Type: model.TypeString},
	}})
	new.Enums = append(new.Enums, model.Enum{Name: "Tier", Values: []string{"FREE", "PAID"}})
	new.Sort()

	r := Compare(old, new)
	t.Logf("输入 old=v1 new=v1.1；共识别变更 %d 项", len(r.Changes))
	for _, c := range r.Changes {
		t.Logf("  [%s] %s: %s", c.Severity, c.Path, c.Message)
	}
	if !r.IsCompatible() {
		var breaks []string
		for _, c := range r.Changes {
			if c.Severity == SeverityBreaking {
				breaks = append(breaks, c.Path)
			}
		}
		t.Fatalf("期望兼容，但存在破坏性变更: %s", strings.Join(breaks, ", "))
	}
	assertKinds(t, r, map[ChangeKind]int{
		FieldAdded:        1,
		FieldOptionalness: 1,
		EnumValueAdded:    1,
		EntityAdded:       1,
		EnumAdded:         1,
	})
}

func TestCompareBreakingChanges(t *testing.T) {
	old := schemaV1()
	new := schemaV1()
	new.Version = "2.0.0"
	// 删字段
	var kept []model.Field
	for _, f := range new.Entities[0].Fields {
		if f.Name != "Age" {
			kept = append(kept, f)
		}
	}
	new.Entities[0].Fields = kept
	// 改类型
	for i := range new.Entities[0].Fields {
		if new.Entities[0].Fields[i].Name == "Id" {
			new.Entities[0].Fields[i].Type = model.TypeInteger
		}
	}
	// 可选 -> 必填（收紧）
	for i := range new.Entities[0].Fields {
		if new.Entities[0].Fields[i].Name == "State" {
			new.Entities[0].Fields[i].Optional = false
		}
	}
	// 枚举引用目标变更
	for i := range new.Entities[0].Fields {
		if new.Entities[0].Fields[i].Name == "State" {
			new.Entities[0].Fields[i].EnumRef = "Color"
		}
	}
	// 收紧枚举：删除 INACTIVE
	var ev []string
	for _, v := range new.Enums[0].Values {
		if v != "INACTIVE" {
			ev = append(ev, v)
		}
	}
	new.Enums[0].Values = ev
	// 删除实体
	var ne []model.Entity
	for _, e := range new.Entities {
		if e.Name != "Legacy" {
			ne = append(ne, e)
		}
	}
	new.Entities = ne
	// 删除枚举
	var ne2 []model.Enum
	for _, e := range new.Enums {
		if e.Name != "Color" {
			ne2 = append(ne2, e)
		}
	}
	new.Enums = ne2
	new.Sort()

	r := Compare(old, new)
	t.Logf("输入 old=v1 new=v2(破坏版)；判定依据：")
	for _, c := range r.Changes {
		t.Logf("  [%s] %s: %s", c.Severity, c.Path, c.Message)
	}
	if r.IsCompatible() {
		t.Fatalf("期望判定为不兼容")
	}
	if r.BreakingCount() == 0 {
		t.Fatalf("BreakingCount = 0")
	}
	assertKinds(t, r, map[ChangeKind]int{
		FieldRemoved:       1,
		FieldTypeChanged:   1,
		FieldOptionalness:  1,
		FieldEnumRefChange: 1,
		EnumValueRemoved:   1,
		EntityRemoved:      1,
		EnumRemoved:        1,
	})
}

func TestCompareDeterministic(t *testing.T) {
	old := schemaV1()
	new := schemaV1()
	new.Version = "1.2.0"
	new.Entities[0].Fields = append(new.Entities[0].Fields,
		model.Field{Name: "Email", Type: model.TypeString})
	new.Enums[0].Values = append(new.Enums[0].Values, "PENDING")
	new.Sort()

	r1 := Compare(old, new)
	for i := 0; i < 20; i++ {
		r := Compare(old, new)
		if renderReport(r) != renderReport(r1) {
			t.Fatalf("第 %d 次判定结论与首次不一致:\n%s\n!=\n%s", i, renderReport(r), renderReport(r1))
		}
	}
}

func TestCompareNoChange(t *testing.T) {
	r := Compare(schemaV1(), schemaV1())
	if len(r.Changes) != 0 || !r.IsCompatible() {
		t.Fatalf("完全相同的 schema 应判定兼容且无变更, got=%v", r.Changes)
	}
}

func assertKinds(t *testing.T, r *Report, want map[ChangeKind]int) {
	t.Helper()
	got := map[ChangeKind]int{}
	for _, c := range r.Changes {
		got[c.Kind]++
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("变更类型 %s 出现 %d 次, 期望 %d 次", k, got[k], n)
		}
	}
	for k, n := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("出现了未预期的变更类型 %s (%d 次)", k, n)
		}
	}
}

func renderReport(r *Report) string {
	var b strings.Builder
	for _, c := range r.Changes {
		b.WriteString(string(c.Severity))
		b.WriteByte('|')
		b.WriteString(string(c.Kind))
		b.WriteByte('|')
		b.WriteString(c.Path)
		b.WriteByte('|')
		b.WriteString(c.Message)
		b.WriteByte('\n')
	}
	return b.String()
}
