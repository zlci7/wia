package story_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRuntimeDefinitionNamesNoPackType is the check that the split actually happened.
//
// story and content are separate packages because a pack is a file format and this is
// the definition a world runs from. Moving the definition here while it still named
// content.PackLocation, content.PackBystander or content.GameSummary would not have
// split anything: the import would be gone but the coupling would remain, and every
// future change to the pack format would still reach the engine. The two shapes are
// allowed to look identical today; they are not allowed to be the same type.
func TestRuntimeDefinitionNamesNoPackType(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
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
		path := filepath.Join(dir, name)
		if _, err := parser.ParseFile(fset, path, nil, 0); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(source), "content.") {
			t.Errorf("%s refers to a content type: the running definition must use its own shapes, "+
				"or the pack format keeps reaching into the engine", name)
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
