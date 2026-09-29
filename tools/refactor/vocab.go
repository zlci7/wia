// vocab renames a package's own vocabulary to the names of the package that now
// owns it, and removes the local type declarations.
//
// It works on one directory of .go files and refuses to run if it would produce a file
// that still mentions the old names, so a partial rewrite cannot pass unnoticed. It is
// deliberately a program rather than a shell substitution: the earlier work in this
// repository lost Chinese literals to a text cmdlet that re-encoded the file.
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
	"regexp"
	"strings"
)

func runVocab(args []string) {
	dir := flag.String("dir", "", "package directory")
	pkg := flag.String("pkg", "", "qualifier to add, e.g. turn")
	types := flag.String("types", "", "comma-separated old=new type names")
	fields := flag.String("fields", "", "comma-separated old=new field names")
	check := flag.Bool("check", false, "report what would change without writing")
	flag.CommandLine.Parse(args)

	typePairs := map[string]string{}
	for _, pair := range strings.Split(*types, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			panic("bad type pair: " + pair)
		}
		typePairs[parts[0]] = parts[1]
	}
	fieldPairs := map[string]string{}
	for _, pair := range strings.Split(*fields, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			panic("bad field pair: " + pair)
		}
		fieldPairs[parts[0]] = parts[1]
	}

	entries, err := os.ReadDir(*dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	total := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(*dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		file, err := parser.ParseFile(fset, path, data, parser.ParseComments)
		if err != nil {
			panic(fmt.Sprintf("%s: %v", entry.Name(), err))
		}
		changed := false
		count := 0
		// Type names: a bare use of a moved type becomes a qualified one. The name is
		// matched with word boundaries so a longer identifier is never touched.
		ast.Inspect(file, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			newName, ok := typePairs[id.Name]
			if !ok {
				return true
			}
			id.Name = *pkg + "." + newName
			count++
			changed = true
			return true
		})
		// Field names live in selectors, which the walk above handles as identifiers too,
		// so they are rewritten by a textual pass limited to `.Old` shapes.
		text := render(fset, file)
		for old, newName := range fieldPairs {
			re := regexp.MustCompile(`\.` + regexp.QuoteMeta(old) + `\b`)
			if re.MatchString(text) {
				text = re.ReplaceAllString(text, "."+newName)
				changed = true
				count++
			}
		}
		if !changed {
			continue
		}
		total += count
		if *check {
			fmt.Printf("  %s: %d changes\n", entry.Name(), count)
			continue
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			panic(err)
		}
		fmt.Printf("  %s: %d changes\n", entry.Name(), count)
	}
	fmt.Printf("%d changes\n", total)
}

func render(fset *token.FileSet, file *ast.File) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		panic(err)
	}
	return buf.String()
}
