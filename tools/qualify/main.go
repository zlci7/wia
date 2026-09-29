// Command qualify rewrites references to a set of moved symbols so they point at
// a different package, and adds the import that makes them resolve.
//
// It works on the syntax tree. A text replacement of a name like Run would also
// hit the verb Run in method names and comments, and a name like Character is
// sometimes a local variable; the tool therefore replaces an identifier only when
// it is a whole expression referring to the moved declaration.
//
// It rewrites expressions in place rather than rebuilding statements, so the only
// node types it has to enumerate are those that can hold an expression. A missed
// type fails the build, which is the point: nothing is silently skipped.
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
	"strconv"
	"strings"
)

func main() {
	dir := flag.String("dir", "", "package directory to rewrite")
	targetPath := flag.String("path", "", "import path of the package that now owns the symbols")
	alias := flag.String("alias", "", "import alias (defaults to the package name)")
	names := flag.String("names", "", "comma-separated symbol names that moved")
	renames := flag.String("renames", "", "comma-separated old=new pairs for symbols that also got a new name")
	vars := flag.String("vars", "", "comma-separated old=new pairs for local variables to rename")
	drop := flag.String("drop", "", "comma-separated type names to delete from -file")
	file := flag.String("file", "", "single file to edit with -drop")
	from := flag.String("from", "", "rewrite selectors of this import path instead of bare identifiers")
	tests := flag.Bool("tests", false, "rewrite test files too")
	check := flag.Bool("check", false, "report what would change without writing")
	flag.Parse()
	if *drop != "" {
		if *file == "" {
			fmt.Fprintln(os.Stderr, "qualify: -drop needs -file")
			os.Exit(2)
		}
		dropDeclarations(*file, *drop)
		return
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "qualify: -dir is required")
		os.Exit(2)
	}
	if *vars != "" {
		renameLocals(*dir, *vars, *tests, *check)
		return
	}
	if *targetPath == "" || *names == "" {
		fmt.Fprintln(os.Stderr, "qualify: -path and -names are required")
		os.Exit(2)
	}
	moved := map[string]bool{}
	for _, name := range strings.Split(*names, ",") {
		moved[strings.TrimSpace(name)] = true
	}
	// A symbol that is unexported in the old package needs a new name in the new
	// one, because the two packages are no longer the same scope.
	renamed := map[string]string{}
	for _, pair := range strings.Split(*renames, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "qualify: bad rename %q\n", pair)
			os.Exit(2)
		}
		renamed[parts[0]] = parts[1]
	}
	pkgName := filepath.Base(*targetPath)
	if *alias != "" {
		pkgName = *alias
	}

	entries, err := os.ReadDir(*dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	// Which composite literals name a struct of this package? That needs the type
	// names of the whole package: ContextScope is declared in one file and used in
	// another, and its keys are field names in either case.
	packageTypeNames := map[string]bool{}
	if *from == "" {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") {
				continue
			}
			parsed, err := parser.ParseFile(fset, filepath.Join(*dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				continue
			}
			for _, decl := range parsed.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name != nil {
						packageTypeNames[typeSpec.Name.Name] = true
					}
				}
			}
		}
	}
	type result struct {
		file  string
		count int
	}
	var results []result
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") != *tests {
			continue
		}
		path := filepath.Join(*dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		count := 0
		if *from != "" {
			source := importLocalName(file, *from)
			if source == "" {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || !moved[sel.Sel.Name] {
					return true
				}
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == source {
					sel.X = ast.NewIdent(pkgName)
					count++
				}
				return true
			})
		} else {
			fileLevelTypes := collectFileLevelTypes(file)
			literalFields := map[*ast.Ident]bool{}
			ast.Inspect(file, func(n ast.Node) bool {
				rewriteExpressions(n, moved, renamed, pkgName, fileLevelTypes, packageTypeNames, literalFields, &count)
				return true
			})
		}
		if count == 0 {
			continue
		}
		// Both modes leave the file referring to a package it does not import yet.
		addImport(file, *targetPath, *alias)
		results = append(results, result{name, count})
		if *check {
			continue
		}
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, file); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			panic(err)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].file < results[j].file })
	total := 0
	for _, r := range results {
		fmt.Printf("  %-34s %4d\n", r.file, r.count)
		total += r.count
	}
	fmt.Printf("%d files, %d references\n", len(results), total)
}

// dropDeclarations deletes named declarations from one file. It is used once the
// type that moved has its new home, so the stale copy cannot stay behind as a
// second definition of the same concept.
func dropDeclarations(path, names string) {
	wanted := map[string]bool{}
	for _, name := range strings.Split(names, ",") {
		wanted[strings.TrimSpace(name)] = true
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	kept := make([]ast.Decl, 0, len(file.Decls))
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			kept = append(kept, decl)
			continue
		}
		specs := make([]ast.Spec, 0, len(gen.Specs))
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if ok && typeSpec.Name != nil && wanted[typeSpec.Name.Name] {
				fmt.Printf("  removed type %s (line %d)\n", typeSpec.Name.Name, fset.Position(typeSpec.Pos()).Line)
				continue
			}
			specs = append(specs, spec)
		}
		if len(specs) == 0 {
			continue
		}
		gen.Specs = specs
		kept = append(kept, gen)
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

// renameLocals renames a short variable that has to change because the new
// package name would otherwise be shadowed where it is used.
//
// It renames by ast.Object rather than by text: the object is the declaration the
// identifier resolves to, so only the uses of this particular variable change and
// a comment or an unrelated name of the same word is untouched.
func renameLocals(dir, pairs string, tests, check bool) {
	mapping := map[string]string{}
	for _, pair := range strings.Split(pairs, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "qualify: bad -vars entry %q\n", pair)
			os.Exit(2)
		}
		mapping[parts[0]] = parts[1]
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	total := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") != tests {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		// Find the declarations to rename and remember their objects.
		targets := map[*ast.Object]string{}
		ast.Inspect(file, func(n ast.Node) bool {
			// Only the declaring assignment matters: a later `x = ...` reuses the
			// same ast.Object, so renaming the object covers both forms.
			assign, isAssign := n.(*ast.AssignStmt)
			if !isAssign || assign.Tok != token.DEFINE || len(assign.Lhs) != len(assign.Rhs) {
				return true
			}
			for _, lhs := range assign.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Obj == nil {
					continue
				}
				replacement, wanted := mapping[ident.Name]
				if wanted {
					targets[ident.Obj] = replacement
				}
			}
			return true
		})
		if len(targets) == 0 {
			continue
		}
		count := 0
		ast.Inspect(file, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok || ident.Obj == nil {
				return true
			}
			if replacement, ok := targets[ident.Obj]; ok {
				ident.Name = replacement
				count++
			}
			return true
		})
		total += count
		fmt.Printf("  %-34s %4d\n", name, count)
		if check {
			continue
		}
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, file); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			panic(err)
		}
	}
	fmt.Printf("%d uses renamed\n", total)
}

// collectFileLevelTypes returns the ast.Object of every type declared in this
// file. An identifier bound to one of these objects is the type itself; an
// identifier bound to anything else is a local variable that shares its spelling
// and must stay as it is.
func collectFileLevelTypes(file *ast.File) map[*ast.Object]bool {
	objects := map[*ast.Object]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name == nil || spec.Name.Obj == nil {
			return true
		}
		objects[spec.Name.Obj] = true
		return true
	})
	return objects
}

// literalTypeName returns the name of a composite literal's type, when that type
// is written as a simple name.
func literalTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return literalTypeName(t.X)
	case *ast.ArrayType:
		return literalTypeName(t.Elt)
	}
	return ""
}

// rewriteExpressions replaces, on every node that can hold an expression, each
// expression that is a bare reference to a moved symbol.
//
// literalFields holds the struct literal keys of the whole file. It has to be
// shared across the walk: the keys are collected when the composite literal is
// visited, but they are protected when the key node itself is visited later, and
// a per-call map would already be gone by then.
func rewriteExpressions(n ast.Node, moved map[string]bool, renamed map[string]string, pkgName string, fileLevelTypes map[*ast.Object]bool, fileLevelTypeNames map[string]bool, literalFields map[*ast.Ident]bool, count *int) {
	qualify := func(expr ast.Expr) ast.Expr {
		ident, ok := expr.(*ast.Ident)
		if !ok || !moved[ident.Name] {
			return expr
		}
		// A struct literal field name is a key, not an expression: Run in
		// model.TextRequest{Run: ...} names a field and must stay as it is.
		if literalFields[ident] {
			return expr
		}
		// A name that resolves to a file-level type declaration is the type
		// itself. A name that resolves to anything else is a local variable that
		// happens to be spelled the same way, and must stay as it is.
		if ident.Obj != nil && !fileLevelTypes[ident.Obj] {
			return expr
		}
		name := ident.Name
		if replacement, ok := renamed[name]; ok {
			name = replacement
		}
		*count++
		return &ast.SelectorExpr{X: ast.NewIdent(pkgName), Sel: ast.NewIdent(name)}
	}
	qualifyList := func(list []ast.Expr) {
		for i, expr := range list {
			list[i] = qualify(expr)
		}
	}
	// Keys of a literal of a type this package declares are field names, never
	// expressions. Keys of any other literal are expressions (a map key).
	if literal, ok := n.(*ast.CompositeLit); ok {
		if fileLevelTypeNames[literalTypeName(literal.Type)] {
			for _, element := range literal.Elts {
				if kv, ok := element.(*ast.KeyValueExpr); ok {
					if ident, ok := kv.Key.(*ast.Ident); ok {
						literalFields[ident] = true
					}
				}
			}
		}
	}
	switch node := n.(type) {
	case *ast.ArrayType:
		node.Elt = qualify(node.Elt)
	case *ast.MapType:
		node.Key = qualify(node.Key)
		node.Value = qualify(node.Value)
	case *ast.StarExpr:
		node.X = qualify(node.X)
	case *ast.ChanType:
		node.Value = qualify(node.Value)
	case *ast.Ellipsis:
		node.Elt = qualify(node.Elt)
	case *ast.TypeAssertExpr:
		node.Type = qualify(node.Type)
	case *ast.CompositeLit:
		if node.Type != nil {
			node.Type = qualify(node.Type)
		}
	case *ast.CallExpr:
		node.Fun = qualify(node.Fun)
		qualifyList(node.Args)
	case *ast.KeyValueExpr:
		if ident, ok := node.Key.(*ast.Ident); !ok || !literalFields[ident] {
			node.Key = qualify(node.Key)
		}
		node.Value = qualify(node.Value)
	case *ast.ReturnStmt:
		qualifyList(node.Results)
	case *ast.AssignStmt:
		qualifyList(node.Rhs)
	case *ast.BinaryExpr:
		node.X = qualify(node.X)
		node.Y = qualify(node.Y)
	case *ast.UnaryExpr:
		node.X = qualify(node.X)
	case *ast.ParenExpr:
		node.X = qualify(node.X)
	case *ast.SliceExpr:
		node.X = qualify(node.X)
	case *ast.IndexExpr:
		node.X = qualify(node.X)
		node.Index = qualify(node.Index)
	case *ast.ExprStmt:
		node.X = qualify(node.X)
	case *ast.ValueSpec:
		node.Type = qualify(node.Type)
		qualifyList(node.Values)
	case *ast.SendStmt:
		node.Value = qualify(node.Value)
	case *ast.RangeStmt:
		node.X = qualify(node.X)
	case *ast.Field:
		node.Type = qualify(node.Type)
	case *ast.FuncDecl:
		if node.Recv != nil {
			for _, field := range node.Recv.List {
				field.Type = qualify(field.Type)
			}
		}
		if node.Type.Results != nil {
			for _, field := range node.Type.Results.List {
				field.Type = qualify(field.Type)
			}
		}
	}
}

// addImport adds the target package to the file's import block.
func addImport(file *ast.File, path, alias string) {
	for _, spec := range file.Imports {
		if value, err := strconv.Unquote(spec.Path.Value); err == nil && value == path {
			return
		}
	}
	spec := &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(path)}}
	if alias != "" {
		spec.Name = ast.NewIdent(alias)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		gen.Specs = append(gen.Specs, spec)
		file.Imports = append(file.Imports, spec)
		return
	}
}

// importLocalName returns the local name a file uses for an import path.
func importLocalName(file *ast.File, path string) string {
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil || value != path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		parts := strings.Split(value, "/")
		return parts[len(parts)-1]
	}
	return ""
}
