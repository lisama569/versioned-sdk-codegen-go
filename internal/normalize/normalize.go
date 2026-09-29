// Package normalize converts schema documents in different source dialects
// into the unified internal model and reports every malformed entry
// categorized by reason instead of silently dropping it.
package normalize

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// Format selects the source dialect. FormatAuto detects it from the
// document shape.
type Format string

const (
	FormatAuto    Format = ""
	FormatSimple  Format = "simple"
	FormatOpenAPI Format = "openapi"
)

// IssueCode categorizes a normalization problem by reason.
type IssueCode string

const (
	IssueInvalidSyntax  IssueCode = "invalid_syntax" // document is not valid JSON / wrong shape
	IssueUnsupported    IssueCode = "unsupported_format"
	IssueMissingVersion IssueCode = "missing_version"
	IssueMissingName    IssueCode = "missing_name" // entity/enum/field without a name
	IssueMissingType    IssueCode = "missing_type" // field without a type
	IssueInvalidType    IssueCode = "invalid_type" // unknown or malformed type
	IssueUnknownRef     IssueCode = "unknown_ref"  // ref points at nothing/wrong kind
	IssueNameConflict   IssueCode = "name_conflict"
	IssueInvalidName    IssueCode = "invalid_name" // name is not a usable identifier
)

// Issue is a single categorized problem found in a source document.
type Issue struct {
	Code    IssueCode
	Path    string // e.g. entities[2].fields[0].type
	Message string
}

// Error implements error.
func (i Issue) Error() string {
	if i.Path == "" {
		return fmt.Sprintf("%s: %s", i.Code, i.Message)
	}
	return fmt.Sprintf("%s: %s: %s", i.Path, i.Code, i.Message)
}

// Issues is the deterministic, ordered list of problems found while
// normalizing one document.
type Issues []Issue

// Error implements error.
func (ii Issues) Error() string {
	parts := make([]string, len(ii))
	for i, is := range ii {
		parts[i] = is.Error()
	}
	return strings.Join(parts, "\n")
}

// Has reports whether any issue with the given code exists.
func (ii Issues) Has(code IssueCode) bool {
	for _, is := range ii {
		if is.Code == code {
			return true
		}
	}
	return false
}

// AsIssues extracts the categorized issue list from a normalization error.
func AsIssues(err error) (Issues, bool) {
	var ii Issues
	if errors.As(err, &ii) {
		return ii, true
	}
	return nil, false
}

// Source is one schema document to normalize.
type Source struct {
	Format Format // dialect hint; FormatAuto to detect
	Name   string // file/logical name used in issue paths and logs
	Data   []byte
}

type collector struct {
	src    Source
	issues []Issue
}

func (c *collector) add(code IssueCode, path, format string, args ...any) {
	c.issues = append(c.issues, Issue{
		Code:    code,
		Path:    path,
		Message: fmt.Sprintf(format, args...),
	})
}

// Normalize converts a source document into the unified model. It returns a
// non-nil error (wrapping categorized Issues) when the document contains any
// malformed entry; malformed entries are never silently skipped.
func Normalize(src Source) (*model.Schema, error) {
	c := &collector{src: src}

	var root map[string]any
	dec := json.NewDecoder(strings.NewReader(string(src.Data)))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		c.add(IssueInvalidSyntax, "", "source %q is not a valid JSON object: %v", src.Name, err)
		return nil, c.finalize()
	}
	if root == nil {
		c.add(IssueInvalidSyntax, "", "source %q is not a JSON object", src.Name)
		return nil, c.finalize()
	}

	format := src.Format
	if format == FormatAuto {
		switch {
		case root["openapi"] != nil || root["components"] != nil:
			format = FormatOpenAPI
		case root["entities"] != nil || root["enums"] != nil:
			format = FormatSimple
		default:
			c.add(IssueUnsupported, "", "source %q matches neither the simple nor the OpenAPI dialect", src.Name)
			return nil, c.finalize()
		}
	}

	var schema *model.Schema
	switch format {
	case FormatSimple:
		schema = parseSimple(c, root)
	case FormatOpenAPI:
		schema = parseOpenAPI(c, root)
	default:
		c.add(IssueUnsupported, "", "unknown source format %q", format)
		return nil, c.finalize()
	}
	if schema == nil {
		schema = &model.Schema{}
	}
	if err := c.finalize(); err != nil {
		return nil, err
	}
	// Stable ordering across dialects; field order is the declared order.
	sort.Slice(schema.Entities, func(i, j int) bool {
		return schema.Entities[i].Name < schema.Entities[j].Name
	})
	sort.Slice(schema.Enums, func(i, j int) bool {
		return schema.Enums[i].Name < schema.Enums[j].Name
	})
	return schema, nil
}

func (c *collector) finalize() error {
	if len(c.issues) == 0 {
		return nil
	}
	sort.SliceStable(c.issues, func(i, j int) bool {
		a, b := c.issues[i], c.issues[j]
		if cmp := comparePaths(a.Path, b.Path); cmp != 0 {
			return cmp < 0
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
	// Deduplicate identical issues while keeping the sorted order.
	uniq := c.issues[:0]
	var prev Issue
	for i, is := range c.issues {
		if i > 0 && is == prev {
			continue
		}
		uniq = append(uniq, is)
		prev = is
	}
	return Issues(uniq)
}

// asMap / asSlice helpers with strict shape reporting.

func asObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// comparePaths orders issue paths in a human-natural way: text segments
// compare lexicographically while bracketed indexes compare numerically, so
// "fields[2]" precedes "fields[10]".
func comparePaths(a, b string) int {
	pa, pb := pathSegments(a), pathSegments(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, y := pa[i], pb[i]
		if x.num && y.num {
			if x.n != y.n {
				if x.n < y.n {
					return -1
				}
				return 1
			}
			continue
		}
		if x.text != y.text {
			if x.text < y.text {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
		return 1
	}
	return 0
}

type pathSeg struct {
	text string
	num  bool
	n    int
}

func pathSegments(p string) []pathSeg {
	var segs []pathSeg
	for i := 0; i < len(p); {
		switch p[i] {
		case '[':
			j := i + 1
			n := 0
			for j < len(p) && p[j] >= '0' && p[j] <= '9' {
				n = n*10 + int(p[j]-'0')
				j++
			}
			if j < len(p) && p[j] == ']' && j > i+1 {
				segs = append(segs, pathSeg{num: true, n: n})
				i = j + 1
				continue
			}
		case '.':
			i++
			continue
		}
		j := i
		for j < len(p) && p[j] != '.' && p[j] != '[' {
			j++
		}
		segs = append(segs, pathSeg{text: p[i:j]})
		i = j
	}
	return segs
}

// canonicalPrimitive maps the accepted primitive spellings onto the four
// canonical primitives. Unknown spellings are reported by the caller.
func canonicalPrimitive(raw string) (string, bool) {
	switch raw {
	case "string", "str":
		return "string", true
	case "int", "integer", "long":
		return "int", true
	case "bool", "boolean":
		return "bool", true
	case "float", "number", "double":
		return "float", true
	default:
		return "", false
	}
}
