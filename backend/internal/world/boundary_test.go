package world

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestWorldDependsOnNothingButTheStandardLibrary guards the dependency arrow the
// whole extraction depends on.
//
// Every other narrative package may import world, so world must import none of
// them. If it ever imports storyapp, storage, model or api, the arrow has turned
// around and the module can no longer be extracted without a cycle — and that
// failure would not show up as a test failure anywhere else. The rule is enforced
// more strictly than the four named packages: only the standard library is
// allowed, so the boundary cannot erode one convenient import at a time.
func TestWorldDependsOnNothingButTheStandardLibrary(t *testing.T) {
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
			if strings.Contains(path, ".") {
				t.Errorf("%s imports %q: the domain package may depend only on the standard library", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
