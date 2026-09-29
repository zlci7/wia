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
	"strings"
)

func runDropFunc(args []string) {
	dir := flag.String("dir", "", "package directory")
	names := flag.String("names", "", "comma-separated function names")
	flag.CommandLine.Parse(args)
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
		dropped := 0
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !wanted[fn.Name.Name] {
				kept = append(kept, decl)
				continue
			}
			dropped++
			fmt.Printf("  %s: dropped %s\n", entry.Name(), fn.Name.Name)
		}
		if dropped == 0 {
			continue
		}
		file.Decls = kept
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, file); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			panic(err)
		}
	}
}
