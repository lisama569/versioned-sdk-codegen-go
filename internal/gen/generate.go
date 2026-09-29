// Package gen renders normalized schemas into versioned Go SDK packages.
//
// Generation is keyed so that concurrent calls for the same schema serialize
// and produce byte-identical output to serial calls. Output is rendered into
// a temporary directory, formatted, compiled with "go build" and only then
// swapped into place atomically, so a failed run never leaves a half-written
// result behind.
package gen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// Filesystem seams, overridable in tests. Default to the real os calls.
var (
	renameFunc    = os.Rename
	mkdirTempFunc = os.MkdirTemp
	removeAllFunc = os.RemoveAll
	mkdirAllFunc  = os.MkdirAll
	writeFileFunc = os.WriteFile
	statFunc      = os.Stat
)

// Service serializes generations per schema key and keeps the rendering
// itself deterministic.
type Service struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewService creates an empty generation service.
func NewService() *Service {
	return &Service{locks: map[string]*sync.Mutex{}}
}

// lockFor returns the mutex serializing all runs with the same key.
func (s *Service) lockFor(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	l, ok := s.locks[key]
	if !ok {
		l = &sync.Mutex{}
		s.locks[key] = l
	}
	return l
}

// Options controls one generation run.
type Options struct {
	// OutputDir is the final directory the SDK is written to.
	OutputDir string
}

// Generate renders schema into opts.OutputDir. Concurrent calls with the same
// key are serialized; the result is byte-identical to a serial run. On
// failure the output directory is left untouched (or absent).
func (s *Service) Generate(key string, schema *model.Schema, opts Options) error {
	if key == "" {
		return errors.New("generate: empty key")
	}
	if schema == nil {
		return errors.New("generate: nil schema")
	}
	if opts.OutputDir == "" {
		return errors.New("generate: empty output directory")
	}
	lock := s.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	files, err := Render(schema)
	if err != nil {
		return err
	}

	parent := filepath.Dir(opts.OutputDir)
	if parent == "" {
		parent = "."
	}
	if err := mkdirAllFunc(parent, 0o755); err != nil {
		return fmt.Errorf("generate: prepare output parent: %w", err)
	}
	tmp, err := mkdirTempFunc(parent, ".sdkgen-*")
	if err != nil {
		return fmt.Errorf("generate: create staging dir: %w", err)
	}
	// On any failure after creating the staging dir, remove it: the real
	// output path must never be left half-written.
	cleanup := true
	defer func() {
		if cleanup {
			_ = removeAllFunc(tmp)
		}
	}()

	// Deterministic write order.
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		full := filepath.Join(tmp, filepath.FromSlash(p))
		if err := mkdirAllFunc(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("generate: mkdir %s: %w", p, err)
		}
		if err := writeFileFunc(full, files[p], 0o644); err != nil {
			return fmt.Errorf("generate: write %s: %w", p, err)
		}
	}

	if err := buildStaging(tmp); err != nil {
		return fmt.Errorf("generate: staged SDK does not compile: %w", err)
	}

	// Atomic swap into place.
	backup := ""
	if _, err := statFunc(opts.OutputDir); err == nil {
		backup, err = mkdirTempFunc(parent, ".sdkgen-old-*")
		if err != nil {
			return fmt.Errorf("generate: prepare backup: %w", err)
		}
		// Move existing output aside (rename onto a dir inside a fresh
		// temp dir, after removing that empty dir).
		_ = removeAllFunc(backup)
		if err := renameFunc(opts.OutputDir, backup); err != nil {
			return fmt.Errorf("generate: move existing output aside: %w", err)
		}
	}
	if err := renameFunc(tmp, opts.OutputDir); err != nil {
		// Try to roll the previous output back.
		if backup != "" {
			_ = renameFunc(backup, opts.OutputDir)
		}
		return fmt.Errorf("generate: swap staged output into place: %w", err)
	}
	if backup != "" {
		_ = removeAllFunc(backup)
	}
	cleanup = false
	return nil
}

// buildStaging formats and compiles the staged SDK in-place. "go mod tidy"
// would introduce network/time dependent noise, so the module has no
// dependencies and only gofmt + go build are used.
func buildStaging(dir string) error {
	gofmt := exec.Command("gofmt", "-w", ".")
	gofmt.Dir = dir
	var fmtErr bytes.Buffer
	gofmt.Stderr = &fmtErr
	if err := gofmt.Run(); err != nil {
		return fmt.Errorf("gofmt: %w: %s", err, strings.TrimSpace(fmtErr.String()))
	}
	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	var buildErr bytes.Buffer
	build.Stderr = &buildErr
	if err := build.Run(); err != nil {
		return fmt.Errorf("go build: %w: %s", err, strings.TrimSpace(buildErr.String()))
	}
	return nil
}

// Render turns a schema into the complete set of output files keyed by
// forward-slash path. The map content is deterministic for a given schema.
func Render(schema *model.Schema) (map[string][]byte, error) {
	if schema == nil {
		return nil, errors.New("render: nil schema")
	}
	if schema.Version == "" {
		return nil, errors.New("render: schema has no version")
	}
	r := newRenderer(schema)
	out := map[string][]byte{}

	var buf bytes.Buffer
	r.writeGoMod(&buf)
	out["go.mod"] = bytes.Clone(buf.Bytes())

	buf.Reset()
	r.writeDoc(&buf)
	out["doc.go"] = bytes.Clone(buf.Bytes())

	buf.Reset()
	r.writeClient(&buf)
	out["client.go"] = bytes.Clone(buf.Bytes())

	// Sorted entity/enum slices already arrive that way from normalize;
	// sort again defensively so Render never depends on caller ordering.
	for _, e := range sortedEntities(schema) {
		var b bytes.Buffer
		if err := r.writeEntity(&b, e); err != nil {
			return nil, err
		}
		out["types_"+strings.ToLower(e.Name)+".go"] = b.Bytes()
	}
	for _, en := range sortedEnums(schema) {
		var b bytes.Buffer
		r.writeEnum(&b, en)
		out["enum_"+strings.ToLower(en.Name)+".go"] = b.Bytes()
	}
	return out, nil
}

func sortedEntities(s *model.Schema) []model.Entity {
	out := append([]model.Entity(nil), s.Entities...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedEnums(s *model.Schema) []model.Enum {
	out := append([]model.Enum(nil), s.Enums...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
