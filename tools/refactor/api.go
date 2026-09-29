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
	"sort"
	"strings"
)

func runAPI(args []string) {
	target := flag.String("target", "", "package directory holding the declarations")
	callers := flag.String("callers", "", "directory with the call sites")
	names := flag.String("names", "", "comma-separated function names")
	receiver := flag.String("receiver", "WorldStore", "receiver type")
	flag.CommandLine.Parse(args)
	if *target == "" || *callers == "" || *names == "" {
		fmt.Fprintln(os.Stderr, "api: -target, -callers and -names are required")
		os.Exit(2)
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(*names, ",") {
		wanted[strings.TrimSpace(name)] = true
	}

	fset := token.NewFileSet()
	converted := map[string]bool{}
	blocked := map[string][]string{}

	// Pass 1: every call site. A call is converted only when its handle argument is
	// a `.Database()` expression.
	for _, dir := range []string{*callers, *target} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			panic(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				panic(err)
			}
			changed := false
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !wanted[sel.Sel.Name] {
					return true
				}
				if len(call.Args) < 2 {
					return true
				}
				handle, ok := call.Args[1].(*ast.CallExpr)
				if !ok {
					blocked[sel.Sel.Name] = append(blocked[sel.Sel.Name], fmt.Sprintf("%s:%d (arg is %T)", entry.Name(), fset.Position(call.Pos()).Line, call.Args[1]))
					return true
				}
				handleSel, ok := handle.Fun.(*ast.SelectorExpr)
				if !ok || handleSel.Sel.Name != "Database" {
					blocked[sel.Sel.Name] = append(blocked[sel.Sel.Name], fmt.Sprintf("%s:%d (handle is not .Database())", entry.Name(), fset.Position(call.Pos()).Line))
					return true
				}
				owner := handleSel.X
				call.Args = append([]ast.Expr{call.Args[0]}, call.Args[2:]...)
				call.Fun = &ast.SelectorExpr{X: owner, Sel: ast.NewIdent(sel.Sel.Name)}
				converted[sel.Sel.Name] = true
				changed = true
				return true
			})
			if changed {
				if err := write(fset, path, file); err != nil {
					panic(err)
				}
			}
		}
	}

	// Pass 2: the declarations that every call site supports.
	entries, err := os.ReadDir(*target)
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(*target, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		changed := false
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !wanted[fn.Name.Name] || !converted[fn.Name.Name] {
				continue
			}
			if len(blocked[fn.Name.Name]) > 0 {
				continue
			}
			params := fn.Type.Params.List
			handleIndex := -1
			for i, field := range params {
				if star, ok := field.Type.(*ast.StarExpr); ok {
					if sel, ok := star.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "DB" {
						handleIndex = i
					}
				}
			}
			if handleIndex < 0 {
				continue
			}
			handle := params[handleIndex].Names
			fn.Recv = &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("s")}, Type: &ast.StarExpr{X: ast.NewIdent(*receiver)}}}}
			fn.Type.Params.List = append(params[:handleIndex], params[handleIndex+1:]...)
			if len(handle) == 1 {
				renameInBody(fn.Body, handle[0].Name, "s.db")
			}
			fmt.Printf("  converted %s into a method\n", fn.Name.Name)
			changed = true
		}
		if changed {
			if err := write(fset, path, file); err != nil {
				panic(err)
			}
		}
	}

	var names2 []string
	for name := range converted {
		names2 = append(names2, name)
	}
	sort.Strings(names2)
	fmt.Printf("converted: %s\n", strings.Join(names2, ", "))
	if len(blocked) > 0 {
		var keys []string
		for key := range blocked {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Printf("left as a function: %s (%d call sites, %s)\n", key, len(blocked[key]), blocked[key][0])
		}
	}
}

// renameInBody replaces uses of the old handle parameter with the receiver field.
func renameInBody(body *ast.BlockStmt, from, to string) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name != from {
			return true
		}
		ident.Name = to
		return true
	})
}

func write(fset *token.FileSet, path string, file *ast.File) error {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
