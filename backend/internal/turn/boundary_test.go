package turn

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTurnDoesNotDependOnTheApplication guards the direction of the extraction.
//
// turn is the story's own vocabulary: what a turn input is, where a failure came
// from, how a player-visible projection is rendered. It may depend on what is
// already below it — world, wire, model and storage — but not on storyapp or
// storyapi, which are the application around it. An import in that direction would
// mean the module was extracted while still being called from the code above it,
// and the only symptom would be a build that keeps working while the boundary stops
// meaning anything.
func TestTurnDoesNotDependOnTheApplication(t *testing.T) {
	allowed := map[string]bool{
		"gameagent/backend/internal/world":   true,
		"gameagent/backend/internal/wire":    true,
		"gameagent/backend/internal/model":   true,
		"gameagent/backend/internal/storage": true,
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
				t.Errorf("%s imports %q: turn may depend only on world, wire, model and storage", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
