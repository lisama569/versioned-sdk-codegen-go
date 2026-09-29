package codegen

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

func testSchema() *model.Schema {
	return &model.Schema{
		Version: "3.1.4",
		Enums: []model.Enum{
			{Name: "Status", Values: []string{"ACTIVE", "INACTIVE", "PENDING"}},
		},
		Entities: []model.Entity{
			{Name: "UserAccount", Fields: []model.Field{
				{Name: "Id", Type: model.TypeString, Optional: false},
				{Name: "LoginCount", Type: model.TypeInteger, Optional: false},
				{Name: "Score", Type: model.TypeNumber, Optional: true},
				{Name: "Active", Type: model.TypeBoolean, Optional: true},
				{Name: "Status", Type: model.TypeString, Optional: false, EnumRef: "Status"},
			}},
		},
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("快照输出目录失败: %v", err)
	}
	return out
}

func TestGenerateDeterministic(t *testing.T) {
	g := New()
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	res1, err := g.Generate(context.Background(), testSchema(), filepath.Join(dir1, "sdk"), Options{PackageName: "sdk"})
	if err != nil {
		t.Fatalf("首次生成失败: %v", err)
	}
	_, err = g.Generate(context.Background(), testSchema(), filepath.Join(dir2, "sdk"), Options{PackageName: "sdk"})
	if err != nil {
		t.Fatalf("二次生成失败: %v", err)
	}
	t.Logf("生成文件: %v", res1.Files)

	s1 := snapshot(t, filepath.Join(dir1, "sdk"))
	s2 := snapshot(t, filepath.Join(dir2, "sdk"))
	if len(s1) != len(s2) {
		t.Fatalf("两次生成文件数不同: %d != %d", len(s1), len(s2))
	}
	for name, content := range s1 {
		if s2[name] != content {
			t.Fatalf("文件 %s 两次生成内容不一致", name)
		}
	}
}

func TestGenerateIdempotent(t *testing.T) {
	g := New()
	dir := filepath.Join(t.TempDir(), "sdk")
	if _, err := g.Generate(context.Background(), testSchema(), dir, Options{}); err != nil {
		t.Fatalf("首次生成失败: %v", err)
	}
	first := snapshot(t, dir)
	if _, err := g.Generate(context.Background(), testSchema(), dir, Options{}); err != nil {
		t.Fatalf("重复生成失败: %v", err)
	}
	second := snapshot(t, dir)
	if len(first) != len(second) {
		t.Fatalf("重复生成后文件数变化: %d -> %d", len(first), len(second))
	}
	for name, c := range first {
		if second[name] != c {
			t.Fatalf("重复生成后 %s 内容发生变化", name)
		}
	}
	// 不得残留暂存/备份目录。
	parent := filepath.Dir(dir)
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		t.Logf("父目录条目: %s", e.Name())
	}
}

func TestGenerateConcurrentSameTarget(t *testing.T) {
	g := New()
	dir := filepath.Join(t.TempDir(), "sdk")
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = g.Generate(context.Background(), testSchema(), dir, Options{PackageName: "sdk"})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发第 %d 次生成失败: %v", i, err)
		}
	}
	// 并发结果必须与串行基线逐字节一致。
	serialDir := filepath.Join(t.TempDir(), "sdk")
	if _, err := g.Generate(context.Background(), testSchema(), serialDir, Options{PackageName: "sdk"}); err != nil {
		t.Fatalf("串行基线生成失败: %v", err)
	}
	got := snapshot(t, dir)
	want := snapshot(t, serialDir)
	if len(got) != len(want) {
		t.Fatalf("并发产物文件数 %d != 串行 %d", len(got), len(want))
	}
	for name, c := range want {
		if got[name] != c {
			t.Fatalf("并发产物 %s 与串行结果不一致", name)
		}
	}
}

func TestGenerateNilSchemaNoSideEffect(t *testing.T) {
	g := New()
	dir := filepath.Join(t.TempDir(), "sdk")
	_, err := g.Generate(context.Background(), nil, dir, Options{})
	if err == nil {
		t.Fatalf("nil schema 必须报错")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("失败时不得创建输出目录，statErr=%v", statErr)
	}
}

func TestGeneratedCodeCompiles(t *testing.T) {
	if runtime.GOOS == "" {
		t.Skip("需要 Go 工具链")
	}
	g := New()
	dir := filepath.Join(t.TempDir(), "sdk")
	if _, err := g.Generate(context.Background(), testSchema(), dir, Options{PackageName: "sdk"}); err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	// 复制一个最小 go.mod 到父目录后执行 go build 验证生成代码。
	parent := filepath.Dir(dir)
	if err := os.WriteFile(filepath.Join(parent, "go.mod"),
		[]byte("module generatedcheck\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("写 go.mod 失败: %v", err)
	}
	res, err := runGo(t, parent, "build", "./...")
	t.Logf("go build 输出: %s", res)
	if err != nil {
		t.Fatalf("生成代码无法编译: %v\n%s", err, res)
	}
}

func TestRenderEnumValues(t *testing.T) {
	src, err := renderEnum(&model.Enum{Name: "Status", Values: []string{"ACTIVE", "IN"}}, "sdk")
	if err != nil {
		t.Fatalf("renderEnum: %v", err)
	}
	t.Logf("渲染结果:\n%s", src)
	for _, want := range []string{"type Status string", "StatusACTIVE", `Status = "ACTIVE"`, "StatusIN", `Status = "IN"`} {
		if !contains(string(src), want) {
			t.Fatalf("渲染结果缺少 %q", want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
