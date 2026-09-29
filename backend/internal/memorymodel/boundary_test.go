package memorymodel

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMemoryModelDependsOnNothingButTheStandardLibrary keeps these types pure.
//
// They are data a character's memory is made of, and nothing more: no database, no
// model, no story. The package is deliberately separate from the legacy memory
// package while that one still exists, so an import of it here would be the first
// step towards putting the narrative types back into the runtime they are being
// extracted from.
func TestMemoryModelDependsOnNothingButTheStandardLibrary(t *testing.T) {
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
			path := strings.Trim(spec.Path.Value, `"`)
			if strings.Contains(path, ".") || strings.Contains(path, "gameagent/") {
				t.Errorf("%s imports %q: these types may depend only on the standard library", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
