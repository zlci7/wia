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
	"sort"
	"strconv"
	"strings"
)

// Packages this repository actually uses. Adding one here is cheaper than a wrong
// import line, and a missing entry only costs a clear error from the compiler.
var stdlib = []string{
	"bytes", "context", "crypto/rand", "crypto/sha256", "database/sql",
	"encoding/base64", "encoding/hex", "encoding/json", "errors", "flag", "fmt",
	"go/ast", "go/parser", "go/printer", "go/token", "image", "io", "io/fs",
	"log", "net", "net/http", "net/url", "os", "path", "path/filepath",
	"reflect", "regexp", "sort", "strconv", "strings", "sync", "testing",
	"testing/fstest", "time", "unicode", "unicode/utf8",
}

func runImports(args []string) {
	path := flag.String("file", "", "file to fix")
	module := flag.String("module", "", "comma-separated path=localname pairs for module imports")
	keep := flag.String("keep", "", "comma-separated import paths to keep unchanged")
	flag.CommandLine.Parse(args)
	if *path == "" {
		fmt.Fprintln(os.Stderr, "imports: -file is required")
		os.Exit(2)
	}
	// An import this tool does not model must be kept rather than dropped: it
	// rebuilds the block from what it knows, so anything outside that is a deletion
	// waiting to happen. The first version of this tool quietly removed embed, zip,
	// slices and jsonschema from the package it ran on.
	kept := map[string]bool{}
	for _, item := range strings.Split(*keep, ",") {
		if item = strings.TrimSpace(item); item != "" {
			kept[item] = true
		}
	}
	modulePaths := map[string]string{}
	for _, pair := range strings.Split(*module, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "imports: bad -module entry "+pair)
			os.Exit(2)
		}
		modulePaths[parts[0]] = parts[1]
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, *path, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}

	// Which names does the file reference as a package?
	used := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Obj == nil {
			used[id.Name] = true
		}
		return true
	})
	// A blank import is a side effect, not a name.
	for _, spec := range file.Imports {
		if spec.Name != nil && (spec.Name.Name == "_" || spec.Name.Name == ".") {
			used["_"+mustUnquote(spec.Path.Value)] = true
		}
	}

	var lines []string
	for _, name := range stdlib {
		// The local name of an import is its last path element: database/sql is
		// referenced as sql.
		if used[base(name)] {
			lines = append(lines, "\t"+strconv.Quote(name))
		}
	}
	var modules []string
	for path, local := range modulePaths {
		if used[local] {
			line := "\t"
			if base(path) != local {
				line += local + " "
			}
			line += strconv.Quote(path)
			modules = append(modules, line)
		}
	}
	sort.Strings(modules)
	for _, spec := range file.Imports {
		value := mustUnquote(spec.Path.Value)
		switch {
		case spec.Name != nil && spec.Name.Name == "_":
			modules = append(modules, "\t"+spec.Path.Value)
		case spec.Name != nil && spec.Name.Name == ".":
			modules = append(modules, "\t. "+spec.Path.Value)
		case kept[value] && spec.Name == nil:
			// A kept import has no name reference in the file, so it needs the
			// blank identifier to stay legal.
			modules = append(modules, "\t_ "+spec.Path.Value)
		case kept[value]:
			modules = append(modules, "\t"+spec.Name.Name+" "+spec.Path.Value)
		}
	}

	body := append([]string{}, lines...)
	if len(lines) > 0 && len(modules) > 0 {
		body = append(body, "")
	}
	body = append(body, modules...)

	text, err := os.ReadFile(*path)
	if err != nil {
		panic(err)
	}
	source := strings.ReplaceAll(string(text), "\r\n", "\n")
	start := strings.Index(source, "\nimport (")
	var out string
	if start < 0 {
		// Insert an import block after the package clause.
		end := strings.Index(source, "\n")
		out = source[:end] + "\n\nimport (\n" + strings.Join(body, "\n") + "\n)" + source[end:]
	} else {
		end := strings.Index(source[start:], "\n)")
		if end < 0 {
			fmt.Fprintln(os.Stderr, "imports: unterminated import block")
			os.Exit(1)
		}
		out = source[:start] + "\nimport (\n" + strings.Join(body, "\n") + "\n)" + source[start+end+2:]
	}
	if err := os.WriteFile(*path, []byte(strings.ReplaceAll(out, "\n", "\r\n")), 0o644); err != nil {
		panic(err)
	}
	// Print through the printer once so an empty block becomes a clean file.
	formatted, err := parser.ParseFile(fset, *path, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, formatted); err != nil {
		panic(err)
	}
	if err := os.WriteFile(*path, buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("fixed %s: %d std, %d module\n", *path, len(lines), len(modules))
}

func base(path string) string {
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

func mustUnquote(value string) string {
	text, err := strconv.Unquote(value)
	if err != nil {
		return value
	}
	return text
}
