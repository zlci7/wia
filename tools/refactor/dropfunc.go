// dropfunc deletes whole function declarations by name.
//
// It deletes by ast.Decl, not by counting braces: a function's body cannot be found by
// looking for the closing brace at column zero, and a rewrite bounded that way is a
// silent partial edit waiting to happen.
//
// A declaration is more than its body. The doc comment, the comments inside the body and
// a trailing comment on the closing line all belong to it, and the parser keeps them in
// file.Comments as well as on the declaration. Removing only the FuncDecl leaves those
// groups behind as orphans — three comments with nothing to describe — which is exactly
// the kind of debris a later reader has to reverse-engineer. So the removal covers the
// declaration's whole source span, and the result is written with go/format so what
// lands on disk is already in gofmt form.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func runDropFunc(args []string) {
	fs := flag.NewFlagSet("dropfunc", flag.ExitOnError)
	dir := fs.String("dir", "", "package directory")
	names := fs.String("names", "", "comma-separated function names")
	fs.Parse(args)
	if *dir == "" || *names == "" {
		fmt.Fprintln(os.Stderr, "dropfunc: -dir and -names are required")
		os.Exit(2)
	}
	wanted := map[string]bool{}
	for _, n := range strings.Split(*names, ",") {
		wanted[strings.TrimSpace(n)] = true
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(*dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		kept := make([]ast.Decl, 0, len(file.Decls))
		var spans []span
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !wanted[fn.Name.Name] {
				kept = append(kept, decl)
				continue
			}
			start := fn.Pos()
			if fn.Doc != nil {
				start = fn.Doc.Pos()
			}
			spans = append(spans, span{start: start, end: fn.End(), endLine: fset.Position(fn.End()).Line})
			fmt.Printf("  %s: dropped %s\n", entry.Name(), fn.Name.Name)
		}
		if len(spans) == 0 {
			continue
		}
		file.Decls = kept
		file.Comments = commentsOutside(fset, file.Comments, spans)
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, file); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			panic(err)
		}
	}
}

// span is the source range one removed declaration owned. endLine catches a trailing
// comment on the closing line, which sits just past fn.End() and would otherwise survive
// as an orphan.
type span struct {
	start   token.Pos
	end     token.Pos
	endLine int
}

// commentsOutside keeps every comment group the removal does not own. A group is removed
// when it lies entirely inside a removed declaration, or when it begins on the closing
// line of one.
func commentsOutside(fset *token.FileSet, groups []*ast.CommentGroup, spans []span) []*ast.CommentGroup {
	kept := make([]*ast.CommentGroup, 0, len(groups))
	for _, group := range groups {
		remove := false
		for _, s := range spans {
			if s.start <= group.Pos() && group.End() <= s.end {
				remove = true
				break
			}
			if fset.Position(group.Pos()).Line == s.endLine {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, group)
		}
	}
	return kept
}
