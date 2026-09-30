package content

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestContentDoesNotDependOnTheApplication guards the direction content is meant to
// have: it is a tool that prepares material, not part of the story engine.
//
// It may validate a pack against plot's rules (plot is the authority on what a legal
// story line is) and compile it into story and world domain values. It may not import
// app or turn: the engine reads what the tool produced, not the other way around, and the
// architecture requires that deleting the whole content tool still leaves a playable
// game.
func TestContentDoesNotDependOnTheApplication(t *testing.T) {
	allowed := map[string]bool{
		"gameagent/backend/internal/world": true,
		"gameagent/backend/internal/plot":  true,
		"gameagent/backend/internal/story": true,
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
				continue // standard library
			}
			if !allowed[path] {
				t.Errorf("%s imports %q: content may depend only on story, world, plot and wire; the engine reads what it produces, not the reverse", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
