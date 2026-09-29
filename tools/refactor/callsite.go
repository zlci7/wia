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
	"strconv"
	"strings"
)

func runCallsite(args []string) {
	dir := flag.String("dir", "", "package directory to scan")
	names := flag.String("names", "", "comma-separated function names to look for")
	flag.CommandLine.Parse(args)
	if *dir == "" || *names == "" {
		fmt.Fprintln(os.Stderr, "callsite: -dir and -names are required")
		os.Exit(2)
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(*names, ",") {
		wanted[strings.TrimSpace(name)] = true
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	type row struct {
		file                            string
		line                            int
		fn, receiver, firstArg, literal string
	}
	var rows []row
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(*dir, entry.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			panic(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !wanted[sel.Sel.Name] {
				return true
			}
			row := row{file: entry.Name(), line: fset.Position(call.Pos()).Line, fn: sel.Sel.Name}
			row.receiver = exprText(sel.X)
			if len(call.Args) > 0 {
				row.firstArg = exprText(call.Args[0])
			}
			// Remember whether any argument is the raw handle, and where.
			for _, arg := range call.Args {
				text := exprText(arg)
				if strings.Contains(text, "Database()") || text == "db" || text == "tx" {
					row.literal += text + " "
				}
			}
			rows = append(rows, row)
			return true
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].file != rows[j].file {
			return rows[i].file < rows[j].file
		}
		return rows[i].line < rows[j].line
	})
	for _, r := range rows {
		fmt.Printf("%s:%d %s recv=%s arg0=%s handles=%s\n", r.file, r.line, r.fn, r.receiver, r.firstArg, strings.TrimSpace(r.literal))
	}
	fmt.Printf("%d call sites\n", len(rows))
}

func exprText(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && len(e.Args) == 0 {
			return exprText(sel.X) + "." + sel.Sel.Name + "()"
		}
		return "call(...)"
	case *ast.SelectorExpr:
		return exprText(e.X) + "." + e.Sel.Name
	case *ast.BasicLit:
		value, err := strconv.Unquote(e.Value)
		if err != nil {
			return e.Value
		}
		return value
	}
	return "expr"
}
