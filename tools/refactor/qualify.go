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

func runQualify(args []string) {
	dir := flag.String("dir", "", "package directory to rewrite")
	targetPath := flag.String("path", "", "import path of the package that now owns the symbols")
	alias := flag.String("alias", "", "import alias (defaults to the package name)")
	names := flag.String("names", "", "comma-separated symbol names that moved")
	renames := flag.String("renames", "", "comma-separated old=new pairs for symbols that also got a new name")
	vars := flag.String("vars", "", "comma-separated old=new pairs for local variables to rename")
	drop := flag.String("drop", "", "comma-separated type names to delete from -file")
	file := flag.String("file", "", "single file to edit with -drop")
	from := flag.String("from", "", "rewrite selectors of this import path instead of bare identifiers")
	only := flag.String("only-dirs", "", "comma-separated directory suffixes; rewrite only these callers")
	tests := flag.Bool("tests", false, "rewrite test files too")
	check := flag.Bool("check", false, "report what would change without writing")
	flag.CommandLine.Parse(args)
	// A bare name can be declared in two packages at once. When only one of them
	// moves, restricting the callers keeps the other package's own references — and
	// its own declarations — out of the rewrite.
	onlyDirs := []string{}
	for _, item := range strings.Split(*only, ",") {
		if item = strings.TrimSpace(item); item != "" {
			onlyDirs = append(onlyDirs, item)
		}
	}
	allowed := func(path string) bool {
		if len(onlyDirs) == 0 {
			return true
		}
		for _, suffix := range onlyDirs {
			if strings.HasSuffix(filepath.ToSlash(path), suffix) {
				return true
			}
		}
		return false
	}

	if *drop != "" {
		if *dir == "" {
			fail("-drop needs -dir")
		}
		dropDeclarations(*dir, *drop, *file, *tests)
		return
	}
	if *dir == "" {
		fail("-dir is required")
	}
	if *vars != "" {
		renameLocals(*dir, *vars, *tests, *check)
		return
	}
	if *targetPath == "" || *names == "" {
		fail("-path and -names are required")
	}
	moved := map[string]bool{}
	for _, name := range strings.Split(*names, ",") {
		moved[strings.TrimSpace(name)] = true
	}
	renamed := map[string]string{}
	for _, pair := range strings.Split(*renames, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			fail("bad rename " + pair)
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
	// A struct literal's key is a field name only when the literal's type is
	// declared in this package: ContextScope is declared in one file and used in
	// another, so the set has to come from the whole package.
	packageTypes := map[string]bool{}
	if *from == "" {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			parsed, err := parser.ParseFile(fset, filepath.Join(*dir, entry.Name()), nil, parser.SkipObjectResolution)
			if err != nil {
				continue
			}
			for _, decl := range parsed.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name != nil {
						packageTypes[ts.Name.Name] = true
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
		if !allowed(*dir) {
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
			count = qualifyFile(file, moved, renamed, pkgName, packageTypes)
		}
		if count == 0 {
			continue
		}
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

// qualifyFile rewrites the identifiers of one file that refer to a moved symbol.
//
// The pass is deliberately two-step. Collecting first means the decision to
// rewrite is taken from the declaration an identifier resolves to, before any
// replacement has changed the tree; rewriting second means every replacement
// happens at the parent that owns the child.
func qualifyFile(file *ast.File, moved map[string]bool, renamed map[string]string, pkgName string, packageTypes map[string]bool) int {
	declared, packageLevel := fileDeclaredNames(file)
	protected := protectedFields(file, packageTypes)
	targets := map[*ast.Ident]string{}
	collect(file, func(expr ast.Expr) {
		id, ok := expr.(*ast.Ident)
		if !ok {
			return
		}
		if declared[id] || protected[id] {
			return
		}
		newName, isRenamed := renamed[id.Name]
		if !moved[id.Name] && !isRenamed {
			return
		}
		// The name belongs to this package only when it resolves to a declaration
		// here. Anything else is a local that happens to share the spelling.
		if id.Obj != nil && !packageLevel[id.Obj] {
			return
		}
		if !isRenamed {
			newName = id.Name
		}
		targets[id] = newName
	})
	if len(targets) == 0 {
		return 0
	}
	count := 0
	rewriteChildren(file, func(child ast.Expr) ast.Expr {
		id, ok := child.(*ast.Ident)
		if !ok {
			return child
		}
		newName, ok := targets[id]
		if !ok {
			return child
		}
		count++
		return &ast.SelectorExpr{X: ast.NewIdent(pkgName), Sel: ast.NewIdent(newName)}
	})
	// A moved function's own declaration name is rewritten in place: the body is
	// deleted afterwards, but leaving the old name would leave a second definition.
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if newName, ok := renamed[fn.Name.Name]; ok && moved[fn.Name.Name] {
				fn.Name.Name = newName
			}
		}
	}
	return count
}

// declaredNames returns two sets. The first holds the identifier nodes that are
// themselves declarations (a function's own name, a parameter, a field, a local),
// which are never rewritten. The second holds the objects of the package-level
// declarations, so a use of one of them can be told apart from a local variable
// that happens to share its spelling.
func fileDeclaredNames(file *ast.File) (map[ast.Node]bool, map[*ast.Object]bool) {
	nodes := map[ast.Node]bool{}
	objects := map[*ast.Object]bool{}
	declare := func(id *ast.Ident) {
		if id == nil {
			return
		}
		nodes[id] = true
		if id.Obj != nil {
			objects[id.Obj] = true
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			declare(d.Name)
			addParamNames(nodes, d.Recv)
			addParamNames(nodes, d.Type.Params)
			addParamNames(nodes, d.Type.Results)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					declare(s.Name)
				case *ast.ValueSpec:
					for _, name := range s.Names {
						declare(name)
					}
				}
			}
		}
	}
	// Inside a function body, a name that introduces something is also protected.
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Field:
			for _, name := range node.Names {
				nodes[name] = true
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					nodes[id] = true
				}
			}
		case *ast.RangeStmt:
			if id, ok := node.Key.(*ast.Ident); ok {
				nodes[id] = true
			}
			if id, ok := node.Value.(*ast.Ident); ok {
				nodes[id] = true
			}
		}
		return true
	})
	return nodes, objects
}

func addParamNames(out map[ast.Node]bool, list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		for _, name := range field.Names {
			out[name] = true
		}
	}
}

// protectedFields returns the struct literal keys of the file. In a literal whose
// type this package declares, a bare key is a field name: both the keyed form
// `Run: ...` and the embedded form `BehaviorPolicies: ...`.
func protectedFields(file *ast.File, packageTypes map[string]bool) map[ast.Node]bool {
	out := map[ast.Node]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		literal, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		name, ok := literalTypeName(literal.Type)
		if !ok || !packageTypes[name] {
			return true
		}
		for _, element := range literal.Elts {
			switch item := element.(type) {
			case *ast.KeyValueExpr:
				if id, ok := item.Key.(*ast.Ident); ok {
					out[id] = true
				}
			case *ast.Ident:
				out[item] = true
			}
		}
		return true
	})
	return out
}

// collect visits every expression node in the file.
func collect(file *ast.File, visit func(ast.Expr)) {
	ast.Inspect(file, func(n ast.Node) bool {
		if expr, ok := n.(ast.Expr); ok {
			// A selector's field name is not an expression of this package.
			if sel, ok := expr.(*ast.SelectorExpr); ok {
				_ = sel
			}
			visit(expr)
		}
		return true
	})
}

// rewriteChildren replaces expressions in place at their parents. Every position
// that can hold an expression is listed here and nowhere else.
func rewriteChildren(file *ast.File, transform func(ast.Expr) ast.Expr) {
	list := func(items *[]ast.Expr) {
		for i, item := range *items {
			(*items)[i] = transform(item)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ArrayType:
			node.Elt = transform(node.Elt)
		case *ast.MapType:
			node.Key = transform(node.Key)
			node.Value = transform(node.Value)
		case *ast.StarExpr:
			node.X = transform(node.X)
		case *ast.ChanType:
			node.Value = transform(node.Value)
		case *ast.Ellipsis:
			node.Elt = transform(node.Elt)
		case *ast.TypeAssertExpr:
			node.Type = transform(node.Type)
		case *ast.CompositeLit:
			if node.Type != nil {
				node.Type = transform(node.Type)
			}
			list(&node.Elts)
		case *ast.CallExpr:
			node.Fun = transform(node.Fun)
			list(&node.Args)
		case *ast.KeyValueExpr:
			node.Key = transform(node.Key)
			node.Value = transform(node.Value)
		case *ast.ReturnStmt:
			list(&node.Results)
		case *ast.AssignStmt:
			list(&node.Rhs)
		case *ast.BinaryExpr:
			node.X = transform(node.X)
			node.Y = transform(node.Y)
		case *ast.UnaryExpr:
			node.X = transform(node.X)
		case *ast.ParenExpr:
			node.X = transform(node.X)
		case *ast.SliceExpr:
			node.X = transform(node.X)
			node.Low = transform(node.Low)
			node.High = transform(node.High)
			node.Max = transform(node.Max)
		case *ast.IndexExpr:
			node.X = transform(node.X)
			node.Index = transform(node.Index)
		case *ast.ExprStmt:
			node.X = transform(node.X)
		case *ast.ValueSpec:
			node.Type = transform(node.Type)
			list(&node.Values)
		case *ast.SendStmt:
			node.Value = transform(node.Value)
		case *ast.RangeStmt:
			node.X = transform(node.X)
		case *ast.Field:
			node.Type = transform(node.Type)
		case *ast.CaseClause:
			list(&node.List)
		case *ast.SwitchStmt:
			node.Tag = transform(node.Tag)
		case *ast.IfStmt:
			node.Cond = transform(node.Cond)
		case *ast.ForStmt:
			node.Cond = transform(node.Cond)
		case *ast.FuncDecl:
			if node.Recv != nil {
				for _, field := range node.Recv.List {
					field.Type = transform(field.Type)
				}
			}
			if node.Type.Params != nil {
				for _, field := range node.Type.Params.List {
					field.Type = transform(field.Type)
				}
			}
			if node.Type.Results != nil {
				for _, field := range node.Type.Results.List {
					field.Type = transform(field.Type)
				}
			}
		}
		return true
	})
}

// literalTypeName returns the name of a composite literal's type.
func literalTypeName(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, true
	case *ast.StarExpr:
		return literalTypeName(t.X)
	case *ast.ArrayType:
		return literalTypeName(t.Elt)
	}
	return "", false
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "qualify: "+message)
	os.Exit(2)
}

// renameLocals renames a short variable that has to change because the new package
// name would otherwise be shadowed where it is used.
func renameLocals(dir, pairs string, tests, check bool) {
	mapping := map[string]string{}
	for _, pair := range strings.Split(pairs, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			fail("bad -vars entry " + pair)
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
		targets := map[*ast.Object]string{}
		ast.Inspect(file, func(n ast.Node) bool {
			assign, isAssign := n.(*ast.AssignStmt)
			if !isAssign || assign.Tok != token.DEFINE || len(assign.Lhs) != len(assign.Rhs) {
				return true
			}
			for _, lhs := range assign.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Obj == nil {
					continue
				}
				if replacement, wanted := mapping[ident.Name]; wanted {
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

// dropDeclarations deletes named declarations, so a function that moved cannot
// stay behind as a second definition of the same concept.
//
// A function whose name also got exported in its new package is named here by its
// new name: that is what it is called in the file after the references were
// qualified.
func dropDeclarations(dir, names, onlyFile string, tests bool) {
	wanted := map[string]bool{}
	for _, name := range strings.Split(names, ",") {
		wanted[strings.TrimSpace(name)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if onlyFile != "" && name != onlyFile {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			panic(err)
		}
		kept := make([]ast.Decl, 0, len(file.Decls))
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if isFunc && wanted[fn.Name.Name] {
				fmt.Printf("  removed func %s (%s:%d)\n", fn.Name.Name, name, fset.Position(fn.Pos()).Line)
				removed++
				continue
			}
			if gen, isGen := decl.(*ast.GenDecl); isGen {
				specs := make([]ast.Spec, 0, len(gen.Specs))
				for _, spec := range gen.Specs {
					typeSpec, isType := spec.(*ast.TypeSpec)
					if isType && typeSpec.Name != nil && wanted[typeSpec.Name.Name] {
						fmt.Printf("  removed type %s (%s:%d)\n", typeSpec.Name.Name, name, fset.Position(typeSpec.Pos()).Line)
						removed++
						continue
					}
					specs = append(specs, spec)
				}
				if len(specs) == 0 {
					continue
				}
				gen.Specs = specs
			}
			kept = append(kept, decl)
		}
		if len(kept) == len(file.Decls) {
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
	fmt.Printf("%d declarations removed\n", removed)
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
