package storage

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestStorageDoesNotDependOnBusinessPackages guards the dependency arrow R3 is
// establishing for persistence.
//
// storage answers questions about persisted facts; the modules above it decide what
// those facts mean. If storage ever imports storyapp, turn or content, the arrow has
// reversed and the persistence layer has started to know about the narrative that
// uses it — a failure that shows up as no test failure at all.
//
// The permitted module imports are world (the vocabulary in the rows it reads) and
// wire (text and timestamps). Anything else has to be argued for in review, which is
// the point of listing them explicitly rather than listing what is forbidden.
func TestStorageDoesNotDependOnBusinessPackages(t *testing.T) {
	allowed := map[string]bool{
		"gameagent/backend/internal/world": true,
		"gameagent/backend/internal/wire":  true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote %s: %v", name, spec.Path.Value, err)
			}
			if !strings.Contains(path, "gameagent/") {
				continue // standard library or the sqlite driver
			}
			if !allowed[path] {
				t.Errorf("%s imports %q: storage may depend only on world and wire", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
