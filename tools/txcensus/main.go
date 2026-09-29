// Command fnbody reports, for each top-level function in a package, whether its own
// body begins a world transaction and how many transaction-scoped storage calls it
// makes.
//
// A file-level search is not enough: a file with one transaction makes every function
// in it look like it owns one. Only the body matters, because only the body has to be
// restructured.
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

func main() {
	dir := flag.String("dir", "", "package directory")
	flag.Parse()
	entries, err := os.ReadDir(*dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	type row struct {
		name           string
		file           string
		begin          bool
		metaTx         int
		execOrQuery    int
		carriesSQLTx   bool
		carriesWorldTx bool
	}
	var rows []row
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(*dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			panic(err)
		}
		for _, decl := range file.Decls {
			fnDecl, ok := decl.(*ast.FuncDecl)
			if !ok || fnDecl.Body == nil {
				continue
			}
			var buf bytes.Buffer
			printer.Fprint(&buf, fset, fnDecl.Body)
			body := buf.String()
			item := row{name: fnDecl.Name.Name, file: entry.Name()}
			item.begin = strings.Contains(body, "BeginTx")
			item.metaTx = strings.Count(body, "MetaGetTx") + strings.Count(body, "MetaSetTx")
			item.execOrQuery = strings.Count(body, "tx.ExecContext") + strings.Count(body, "tx.QueryRowContext") + strings.Count(body, "tx.QueryContext")
			if fnDecl.Type.Params != nil {
				for _, param := range fnDecl.Type.Params.List {
					var b bytes.Buffer
					printer.Fprint(&b, fset, param.Type)
					switch b.String() {
					case "*sql.Tx":
						item.carriesSQLTx = true
					case "*storage.WorldTx":
						item.carriesWorldTx = true
					}
				}
			}
			if item.begin || item.metaTx > 0 || item.carriesSQLTx || item.carriesWorldTx {
				rows = append(rows, item)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].file != rows[j].file {
			return rows[i].file < rows[j].file
		}
		return rows[i].name < rows[j].name
	})
	for _, item := range rows {
		flags := []string{}
		if item.begin {
			flags = append(flags, "begins")
		}
		if item.carriesSQLTx {
			flags = append(flags, "param:*sql.Tx")
		}
		if item.carriesWorldTx {
			flags = append(flags, "param:*WorldTx")
		}
		fmt.Printf("%-24s %-20s %-24s metaTx=%d raw=%d\n", item.name, item.file, strings.Join(flags, ","), item.metaTx, item.execOrQuery)
	}
	fmt.Printf("%d functions\n", len(rows))
}
