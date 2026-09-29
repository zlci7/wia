// closure reports the transitive dependency set of named functions within a
// package directory.
//
// It answers "how much comes with this" before a move is attempted, which is the question
// that decides whether a unit is self-contained. Measuring by reading the file has been
// wrong twice in this work: a function that looks local calls something three files away,
// and the move then drags in a cluster nobody intended to touch.
//
// Only calls to identifiers defined in the same directory are followed; everything else is
// reported as external, because an external call is what makes a function movable.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runClosure(args []string) {
	dir := flag.String("dir", "", "package directory")
	roots := flag.String("roots", "", "comma-separated function names to start from")
	flag.CommandLine.Parse(args)
	if *dir == "" || *roots == "" {
		flag.Usage()
		os.Exit(2)
	}

	fset := token.NewFileSet()
	// Every function declared in the directory, with the names it calls.
	declared := map[string][]string{}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(*dir, entry.Name()), nil, 0)
		if err != nil {
			panic(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var calls []string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok {
					calls = append(calls, id.Name)
				}
				return true
			})
			declared[fn.Name.Name] = calls
		}
	}

	// Walk the closure.
	seen := map[string]bool{}
	external := map[string]bool{}
	var order []string
	var visit func(name string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		calls, ok := declared[name]
		if !ok {
			external[name] = true
			return
		}
		seen[name] = true
		order = append(order, name)
		for _, callee := range calls {
			visit(callee)
		}
	}
	for _, root := range strings.Split(*roots, ",") {
		visit(strings.TrimSpace(root))
	}
	sort.Strings(order)
	fmt.Printf("closure of %s: %d functions in the package\n", *roots, len(order))
	for _, name := range order {
		fmt.Printf("  %s\n", name)
	}
	var names []string
	for name := range external {
		// Only identifiers that look like this package's own helpers matter; a call to a
		// method or to another package arrives as a selector and is not visited, so what
		// remains here is either a same-package helper the walk missed or a builtin.
		if len(name) > 0 && name[0] >= 'a' && name[0] <= 'z' {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	fmt.Printf("unresolved lowercase calls: %s\n", strings.Join(names, ", "))
}
