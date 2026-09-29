package plot

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPlotDoesNotDependOnContentOrAbove guards the arrow the content tooling needs.
//
// Content validates a story pack and calls this package for the plot rules, so the
// dependency runs content -> plot and must never run back. If plot imported content
// it could no longer be called by it, and worse, the plot rules would start to
// depend on the shape of a story pack — the two would have to be changed together
// forever.
//
// plot may depend on world for domain vocabulary. Anything above that has to be
// argued for in review, which is why the allowed list is explicit.
func TestPlotDoesNotDependOnContentOrAbove(t *testing.T) {
	allowed := map[string]bool{
		"gameagent/backend/internal/world": true,
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
				continue // standard library
			}
			if !allowed[path] {
				t.Errorf("%s imports %q: plot may depend only on world; content and turn depend on plot, not the reverse", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
