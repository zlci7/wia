package story

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestStoryIsOnlyADataContract keeps this package from growing into an engine.
//
// story exists to be a shape: the frozen definition a world is started from. The
// moment it gains a service, a loader, a repository or a turn helper, it becomes a
// second application service and the boundary it was created for is gone.
//
// It may name domain vocabulary from world and progression rules from plot. It may
// not reach for content — the external file format is compiled into these types by
// content, not the other way around — nor for turn, storage, model or the application.
func TestStoryIsOnlyADataContract(t *testing.T) {
	allowed := map[string]bool{
		"gameagent/backend/internal/world": true,
		"gameagent/backend/internal/plot":  true,
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
				t.Errorf("%s imports %q: story may depend only on world and plot, so that it stays a definition and not an engine", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
