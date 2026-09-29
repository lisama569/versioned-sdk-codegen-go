// Command sdkgen is the CLI for schema normalization, compatibility checks
// and SDK code generation.
//
// Usage:
//
//	sdkgen normalize --format simple schema.json
//	sdkgen compat old.json new.json
//	sdkgen generate --key users-v2 --format auto --out ./sdk/v2 schema.json
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/compat"
	"github.com/lisama569/versioned-sdk-codegen-go/internal/gen"
	"github.com/lisama569/versioned-sdk-codegen-go/internal/normalize"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sdkgen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing subcommand")
	}
	switch args[0] {
	case "normalize":
		return cmdNormalize(args[1:])
	case "compat":
		return cmdCompat(args[1:])
	case "generate":
		return cmdGenerate(args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: sdkgen <command> [flags] [file...]

commands:
  normalize   parse one schema and print the normalized model (or categorized errors)
  compat      compare old vs new schema and print a deterministic compatibility report
  generate    render one schema into a versioned Go SDK package

run "sdkgen <command> -h" for command-specific flags`)
}

func parseFormat(v string) (normalize.Format, error) {
	switch v {
	case "", "auto":
		return normalize.FormatAuto, nil
	case "simple":
		return normalize.FormatSimple, nil
	case "openapi":
		return normalize.FormatOpenAPI, nil
	default:
		return "", fmt.Errorf("unknown format %q (want simple, openapi or auto)", v)
	}
}

func loadSchema(path, formatHint string) (*normalize.Source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := parseFormat(formatHint)
	if err != nil {
		return nil, err
	}
	return &normalize.Source{Format: f, Name: path, Data: data}, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdNormalize(args []string) error {
	fs := flag.NewFlagSet("normalize", flag.ContinueOnError)
	formatFlag := fs.String("format", "auto", "source dialect: auto|simple|openapi")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("normalize requires exactly one schema file")
	}
	src, err := loadSchema(fs.Arg(0), *formatFlag)
	if err != nil {
		return err
	}
	schema, err := normalize.Normalize(*src)
	if err != nil {
		var issues normalize.Issues
		if errors.As(err, &issues) {
			if pErr := printJSON(map[string]any{"ok": false, "issues": issues}); pErr != nil {
				return pErr
			}
			// Categorized schema errors are still failures for callers.
			return fmt.Errorf("%d issue(s) found in %s", len(issues), src.Name)
		}
		return err
	}
	return printJSON(map[string]any{"ok": true, "schema": schema})
}

func cmdCompat(args []string) error {
	fs := flag.NewFlagSet("compat", flag.ContinueOnError)
	oldFmt := fs.String("old-format", "auto", "old schema dialect: auto|simple|openapi")
	newFmt := fs.String("new-format", "auto", "new schema dialect: auto|simple|openapi")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("compat requires exactly two schema files: old new")
	}
	oldSrc, err := loadSchema(fs.Arg(0), *oldFmt)
	if err != nil {
		return err
	}
	newSrc, err := loadSchema(fs.Arg(1), *newFmt)
	if err != nil {
		return err
	}
	oldSchema, err := normalize.Normalize(*oldSrc)
	if err != nil {
		return fmt.Errorf("old schema invalid: %w", err)
	}
	newSchema, err := normalize.Normalize(*newSrc)
	if err != nil {
		return fmt.Errorf("new schema invalid: %w", err)
	}
	return printJSON(compat.Check(oldSchema, newSchema))
}

func cmdGenerate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	formatFlag := fs.String("format", "auto", "source dialect: auto|simple|openapi")
	key := fs.String("key", "", "deduplication key for concurrent generations of the same schema")
	out := fs.String("out", "", "output directory for the generated SDK")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("generate requires exactly one schema file")
	}
	if *key == "" {
		return errors.New("generate requires --key")
	}
	if *out == "" {
		return errors.New("generate requires --out")
	}
	src, err := loadSchema(fs.Arg(0), *formatFlag)
	if err != nil {
		return err
	}
	schema, err := normalize.Normalize(*src)
	if err != nil {
		return err
	}
	svc := gen.NewService()
	if err := svc.Generate(*key, schema, gen.Options{OutputDir: *out}); err != nil {
		return err
	}
	fmt.Printf("generated SDK for version %q at %s\n", schema.Version, *out)
	return nil
}
