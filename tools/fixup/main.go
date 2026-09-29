// Command fixup performs the mechanical part of step 1 of R3: the primitives that
// moved into backend/internal/wire are rewritten to that package, their duplicate
// definitions are deleted, and the imports of every touched file are corrected.
//
// It works on the syntax tree instead of on text. An earlier text-based version
// deleted neighbouring functions because a brace at column zero is not a reliable
// end-of-function marker, and a PowerShell pipeline silently re-encoded the files
// to the system code page. Parsing avoids both: a declaration is removed by
// removing its ast.Decl, and printing always writes UTF-8.
//
// Output is standard gofmt formatting from go/printer, so an untouched file is
// not rewritten and a touched one only changes where it had to.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"

	"strconv"
	"strings"
)

const wirePath = "gameagent/backend/internal/wire"

// name in storyapp, replacement expression after the move
var moved = map[string]string{
	"cleanText":   "wire.Clean",
	"nowText":     "wire.NowText",
	"marshalJSON": "wire.MarshalJSON",
	"boolInt":     "wire.BoolInt",
	"newID":       "wire.NewID",
}

func main() {
	tests := flag.Bool("tests", false, "rewrite test files too (they define none of the moved functions)")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "fixup: package directory is required")
		os.Exit(2)
	}
	dir := flag.Arg(0)
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	rewritten := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		isTest := strings.HasSuffix(name, "_test.go")
		if isTest != *tests {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		usesWire := false
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for name, replacement := range moved {
				if countCalls(fn.Body, name) == 0 {
					continue
				}
				replaceIdentifier(fn.Body, name, replacement)
				usesWire = true
			}
		}
		if !usesWire {
			continue
		}
		file.Decls = removeDuplicates(fset, file, file.Decls)
		addImport(fset, file)
		pruneImports(fset, file)
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, file); err != nil {
			panic(err)
		}
		// go/printer ends the file with exactly one newline; keep that.
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			panic(err)
		}
		rewritten++
	}
	fmt.Printf("rewrote %d files\n", rewritten)
}

// countCalls reports how many times name is called inside a body.
func countCalls(body *ast.BlockStmt, name string) int {
	count := 0
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == name {
			count++
		}
		return true
	})
	return count
}

// removeDuplicates drops the declarations whose bodies now live in wire.
func removeDuplicates(fset *token.FileSet, file *ast.File, decls []ast.Decl) []ast.Decl {
	kept := make([]ast.Decl, 0, len(decls))
	for _, decl := range decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok {
			if _, isMoved := moved[fn.Name.Name]; isMoved {
				fmt.Printf("  removed %s.%s (line %d)\n", file.Name.Name, fn.Name.Name, fset.Position(fn.Pos()).Line)
				continue
			}
		}
		kept = append(kept, decl)
	}
	return kept
}

// replaceIdentifier rewrites every call to name inside a body to replacement.
// The identifier is only replaced where it is a function being called, so a field
// or a local variable of the same name is left alone.
func replaceIdentifier(body *ast.BlockStmt, name, replacement string) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != name {
			return true
		}
		call.Fun = selectorFor(replacement)
		return true
	})
}

// selectorFor turns "wire.Clean" into the selector expression wire.Clean.
func selectorFor(replacement string) ast.Expr {
	parts := strings.SplitN(replacement, ".", 2)
	return &ast.SelectorExpr{X: ast.NewIdent(parts[0]), Sel: ast.NewIdent(parts[1])}
}

// addImport adds the wire import when the file does not import it yet.
func addImport(fset *token.FileSet, file *ast.File) {
	for _, spec := range file.Imports {
		if path, err := strconv.Unquote(spec.Path.Value); err == nil && path == wirePath {
			return
		}
	}
	spec := &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(wirePath)}}
	if file.Imports == nil {
		file.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{spec}}}, file.Decls...)
		return
	}
	// Reuse the existing import declaration: append the spec and let go/printer
	// sort it into place with the module imports.
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		gen.Specs = append(gen.Specs, spec)
		file.Imports = append(file.Imports, spec)
		return
	}
	_ = fset
}

// pruneImports removes imports that no longer appear in the file. Side-effect and
// dot imports are kept unconditionally: their effect is not a name reference.
func pruneImports(fset *token.FileSet, file *ast.File) {
	used := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Obj == nil {
			used[ident.Name] = true
		}
		return true
	})
	var keptSpecs []*ast.ImportSpec
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		specs := gen.Specs[:0]
		for _, spec := range gen.Specs {
			imp := spec.(*ast.ImportSpec)
			path, _ := strconv.Unquote(imp.Path.Value)
			if imp.Name != nil && (imp.Name.Name == "_" || imp.Name.Name == ".") {
				specs = append(specs, spec)
				keptSpecs = append(keptSpecs, imp)
				continue
			}
			local := ""
			if imp.Name != nil {
				local = imp.Name.Name
			} else {
				parts := strings.Split(path, "/")
				local = parts[len(parts)-1]
			}
			if path == wirePath || used[local] {
				specs = append(specs, spec)
				keptSpecs = append(keptSpecs, imp)
			}
		}
		gen.Specs = specs
	}
	file.Imports = keptSpecs
}
