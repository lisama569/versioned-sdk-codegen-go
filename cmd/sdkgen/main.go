// Command sdkgen 是版本化 SDK 生成工具链的本地命令行入口。
//
// 用法：
//
//	sdkgen normalize --schema=path [--dialect=auto|simple|openapi]
//	sdkgen check    --old=path --new=path [--dialect=auto|simple|openapi]
//	sdkgen generate --schema=path --out=dir [--package=sdk] [--dialect=...]
//
// 退出码：0 成功；1 用户输入错误（畸形 schema / 破坏性变更）；2 工具内部错误。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/codegen"
	"github.com/lisama569/versioned-sdk-codegen-go/internal/compat"
	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
	"github.com/lisama569/versioned-sdk-codegen-go/internal/schema"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "normalize":
		err = runNormalize(args)
	case "check":
		err = runCheck(args)
	case "generate":
		err = runGenerate(args)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		var ue *userError
		if errorsAs(err, &ue) {
			os.Exit(1)
		}
		os.Exit(2)
	}
}

type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }

func usage() {
	fmt.Fprint(os.Stderr, `sdkgen — schema 驱动的版本化 SDK 生成工具链

用法:
  sdkgen normalize --schema=path [--dialect=auto|simple|openapi]
  sdkgen check    --old=path --new=path [--dialect=auto|simple|openapi]
  sdkgen generate --schema=path --out=dir [--package=sdk] [--dialect=...]
`)
}

func newFS() *flag.FlagSet {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func dialectFlag(fs *flag.FlagSet) *string {
	return fs.String("dialect", "auto", "schema 方言: auto|simple|openapi")
}

func loadSchema(path, dialect string) (*model.Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rep := schema.Normalize(data, schema.Dialect(dialect))
	if rep.HasErrors() {
		var b strings.Builder
		fmt.Fprintf(&b, "schema %s 归一化失败，共 %d 个问题:\n", path, len(rep.Issues))
		for _, is := range rep.Issues {
			fmt.Fprintf(&b, "  [%s] %s: %s\n", is.Kind, is.Path, is.Message)
		}
		return nil, &userError{strings.TrimRight(b.String(), "\n")}
	}
	return rep.Schema, nil
}

func errorsAs(err error, target **userError) bool {
	u, ok := err.(*userError)
	if !ok {
		return false
	}
	*target = u
	return true
}

func runNormalize(args []string) error {
	fs := newFS()
	schemaPath := fs.String("schema", "", "schema 文件路径")
	d := dialectFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	sc, err := loadSchema(*schemaPath, *d)
	if err != nil {
		return err
	}
	fmt.Println(dumpSchema(sc))
	return nil
}

func runCheck(args []string) error {
	fs := newFS()
	oldPath := fs.String("old", "", "旧版 schema 文件路径")
	newPath := fs.String("new", "", "新版 schema 文件路径")
	d := dialectFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	oldSC, err := loadSchema(*oldPath, *d)
	if err != nil {
		return err
	}
	newSC, err := loadSchema(*newPath, *d)
	if err != nil {
		return err
	}
	rep := compat.Compare(oldSC, newSC)
	for _, c := range rep.Changes {
		fmt.Printf("[%s] %s: %s\n", c.Severity, c.Path, c.Message)
	}
	verdict := "兼容"
	if !rep.IsCompatible() {
		verdict = "不兼容"
	}
	fmt.Printf("结论: %s（变更 %d 项，其中破坏性 %d 项）\n",
		verdict, len(rep.Changes), rep.BreakingCount())
	if !rep.IsCompatible() {
		return &userError{"新 schema 不向后兼容"}
	}
	return nil
}

func runGenerate(args []string) error {
	fs := newFS()
	schemaPath := fs.String("schema", "", "schema 文件路径")
	out := fs.String("out", "", "输出目录")
	pkgName := fs.String("package", "sdk", "生成代码的 Go 包名")
	d := dialectFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	sc, err := loadSchema(*schemaPath, *d)
	if err != nil {
		return err
	}
	g := codegen.New()
	res, err := g.Generate(context.Background(), sc, *out, codegen.Options{PackageName: *pkgName})
	if err != nil {
		return err
	}
	fmt.Printf("生成成功，共 %d 个文件:\n", len(res.Files))
	for _, f := range res.Files {
		fmt.Println("  " + f)
	}
	return nil
}

// dumpSchema 以稳定的文本形式输出归一化结果，便于人工核对。
func dumpSchema(sc *model.Schema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: %s\n", sc.Version)
	for _, e := range sc.Enums {
		fmt.Fprintf(&b, "enum %s: %s\n", e.Name, strings.Join(e.Values, ", "))
	}
	for _, e := range sc.Entities {
		fmt.Fprintf(&b, "entity %s:\n", e.Name)
		for _, f := range e.Fields {
			opt := "required"
			if f.Optional {
				opt = "optional"
			}
			ref := ""
			if f.EnumRef != "" {
				ref = " enum=" + f.EnumRef
			}
			fmt.Fprintf(&b, "  - %s: %s (%s)%s\n", f.Name, f.Type, opt, ref)
		}
	}
	return b.String()
}
