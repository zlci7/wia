package turn

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTurnHasOneContextBuildCallSite keeps request assembly on one path. Stage code
// produces Material; ContextGenerator is the single runtime caller that turns that
// material into a model request through ContextComposer.Build.
func TestTurnHasOneContextBuildCallSite(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var calls []token.Position
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Build" {
				calls = append(calls, fset.Position(call.Pos()))
			}
			return true
		})
	}
	if len(calls) != 1 || filepath.Base(calls[0].Filename) != "generation.go" {
		t.Fatalf("ContextComposer.Build call sites = %v, want generation.go only", calls)
	}
}
