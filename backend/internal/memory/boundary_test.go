package memory

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMemoryOwnsOnlyMemoryPersistence keeps the memory owner from growing into a
// second application or model layer.
//
// The package holds data a character's memory is made of, the rules over it, and the
// translation from storage records. It may name world vocabulary — a
// correction asks which events it affects, and the world package is where the canonical
// identifier helper lives, so reimplementing that check here would be a second
// implementation of something that already exists. It may also name wire, which is this
// module's shared JSON primitive: standard library only, no domain meaning, and no
// dependencies of its own, so it cannot let storage, model, story or the application back
// in. Storage is allowed because it owns the database primitives that memory translates;
// model, story, turn and the application remain outside this package.
func TestMemoryOwnsOnlyMemoryPersistence(t *testing.T) {
	allowed := map[string]bool{
		"gameagent/backend/internal/storage": true,
		"gameagent/backend/internal/world":   true,
		"gameagent/backend/internal/wire":    true,
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
				t.Errorf("%s imports %q: memory may depend only on storage, world and wire among this module's packages", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
