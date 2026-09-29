package gen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

func demoSchema(version string) *model.Schema {
	return &model.Schema{
		Version: version,
		Entities: []model.Entity{
			{Name: "Zeta", Fields: []model.Field{
				{Name: "id", Type: model.Type{Kind: model.KindPrimitive, Primitive: "string"}},
				{Name: "role", Type: model.Type{Kind: model.KindEnum, EnumName: "Role"}},
				{Name: "tags", Optional: true, Type: model.Type{Kind: model.KindArray,
					Elem: ptrType(model.Type{Kind: model.KindPrimitive, Primitive: "string"})}},
				{Name: "owner", Type: model.Type{Kind: model.KindRef, RefName: "Alpha"}},
			}},
			{Name: "Alpha", Fields: []model.Field{
				{Name: "name", Type: model.Type{Kind: model.KindPrimitive, Primitive: "string"}},
				{Name: "score", Type: model.Type{Kind: model.KindPrimitive, Primitive: "float"}},
			}},
		},
		Enums: []model.Enum{{Name: "Role", Values: []string{"admin", "member", "guest"}}},
	}
}

func ptrType(t model.Type) *model.Type { return &t }

func TestRenderIsDeterministic(t *testing.T) {
	files1, err := Render(demoSchema("v1"))
	if err != nil {
		t.Fatal(err)
	}
	// Re-supply entities in shuffled order; output must be identical.
	shuffled := demoSchema("v1")
	shuffled.Entities[0], shuffled.Entities[1] = shuffled.Entities[1], shuffled.Entities[0]
	files2, err := Render(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("render output depends on input ordering:\nkeys1=%v\nkeys2=%v",
			renderFiles(files1), renderFiles(files2))
	}
	wantFiles := []string{"client.go", "doc.go", "enum_role.go", "go.mod", "types_alpha.go", "types_zeta.go"}
	if got := renderFiles(files1); !reflect.DeepEqual(got, wantFiles) {
		t.Fatalf("file set = %v, want %v", got, wantFiles)
	}
	t.Logf("rendered %d files for v1: %v", len(files1), wantFiles)
	for _, p := range wantFiles {
		t.Logf("--- %s ---\n%s", p, files1[p])
	}
}

func TestConcurrentGenerationSameKeyIsIdentical(t *testing.T) {
	base := t.TempDir()
	svc := NewService()
	schema := demoSchema("v2")

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	dirs := make([]string, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dirs[i] = filepath.Join(base, fmt.Sprintf("out-%d", i))
			<-start
			errs[i] = svc.Generate("users-v2", schema, Options{OutputDir: dirs[i]})
		}(i)
	}
	close(start)
	wg.Wait()

	var golden map[string][]byte
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
		got := readTree(t, dirs[i])
		if golden == nil {
			golden = got
			continue
		}
		if !reflect.DeepEqual(golden, got) {
			t.Fatalf("output of run %d differs from first run", i)
		}
	}
	// Regenerate one more time serially into a fresh dir and compare.
	serialDir := filepath.Join(base, "serial")
	if err := svc.Generate("users-v2", schema, Options{OutputDir: serialDir}); err != nil {
		t.Fatal(err)
	}
	if got := readTree(t, serialDir); !reflect.DeepEqual(golden, got) {
		t.Fatal("serial regeneration does not match concurrent outputs")
	}
	t.Logf("all %d concurrent runs plus a serial run produced identical file sets: %v", n, sortedKeysOf(golden))
}

func TestConcurrentSameOutputSameKeySerializes(t *testing.T) {
	// All goroutines target one directory with the same key: the per-key
	// lock guarantees only one generation at a time and the final content
	// equals a standalone serial run.
	shared := filepath.Join(t.TempDir(), "shared")
	svc := NewService()
	schema := demoSchema("v3")

	const n = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := svc.Generate("shared-v3", schema, Options{OutputDir: shared}); err != nil {
				t.Errorf("concurrent generate: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	serial := filepath.Join(t.TempDir(), "serial")
	if err := svc.Generate("shared-v3", schema, Options{OutputDir: serial}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readTree(t, shared), readTree(t, serial)) {
		t.Fatal("shared-dir concurrent result differs from serial result")
	}
}

func TestGeneratedSDKIsReproducibleAcrossRuns(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "a")
	dir2 := filepath.Join(t.TempDir(), "b")
	svc := NewService()
	if err := svc.Generate("k", demoSchema("v4"), Options{OutputDir: dir1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Generate("k", demoSchema("v4"), Options{OutputDir: dir2}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readTree(t, dir1), readTree(t, dir2)) {
		t.Fatal("two separate runs of the same schema diverge")
	}
}

func readTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = bytes.Clone(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sortedKeysOf(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
