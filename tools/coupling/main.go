// Command coupling measures how deeply each file of a Go package depends on
// private symbols of the same package (App fields and methods, unexported
// package-level declarations in other files).
//
// It is a structure-migration instrument for R3: it decides the order in which
// responsibilities can be extracted into their own package, by measuring what an
// extraction would actually have to export.
//
// It is deliberately not part of the module graph: tools/coupling/ carries its
// own go.mod, so it is excluded from go build ./... and go test ./....
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

type fileInfo struct {
	name    string
	lines   int
	funcs   int
	methods int
	// imports of this file, by local name
	imports map[string]string
}

type declInfo struct {
	name  string
	file  string
	kind  string // func, method, type, const, var, field
	owner string // App, or the receiver type
}

func main() {
	dir := flag.String("dir", "", "package directory to analyze")
	out := flag.String("out", "", "write the full report to this file")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "coupling: -dir is required")
		os.Exit(2)
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "coupling:", err)
		os.Exit(1)
	}
	var files []*ast.File
	infos := map[string]*fileInfo{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(*dir, name)
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			fmt.Fprintf(os.Stderr, "coupling: parse %s: %v\n", name, err)
			os.Exit(1)
		}
		files = append(files, parsed)
		data, _ := os.ReadFile(path)
		info := &fileInfo{name: name, imports: map[string]string{}}
		info.lines = strings.Count(string(data), "\n")
		for _, spec := range parsed.Imports {
			p := strings.Trim(spec.Path.Value, `"`)
			local := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				local = p[i+1:]
			}
			if spec.Name != nil {
				local = spec.Name.Name
			}
			info.imports[local] = p
		}
		infos[name] = info
	}

	decls := map[string]*declInfo{}
	add := func(name, file, kind, owner string) {
		if name == "" || name == "_" {
			return
		}
		if _, ok := decls[name]; !ok {
			decls[name] = &declInfo{name: name, file: file, kind: kind, owner: owner}
		}
	}
	appMembers := map[string]string{} // member name -> "field" | "method"

	// pass 1: declarations
	for _, parsed := range files {
		name := filepath.Base(fset.Position(parsed.Pos()).Filename)
		info := infos[name]
		for _, decl := range parsed.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				recv := receiverType(d.Recv)
				if recv == "" {
					info.funcs++
					add(d.Name.Name, name, "func", "")
				} else {
					info.methods++
					add(recv+"."+d.Name.Name, name, "method", recv)
					if recv == "App" {
						appMembers[d.Name.Name] = "method"
					}
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						add(s.Name.Name, name, "type", "")
						if st, ok := s.Type.(*ast.StructType); ok && s.Name.Name == "App" {
							for _, field := range st.Fields.List {
								for _, n := range field.Names {
									if n.Name == "_" {
										continue
									}
									appMembers[n.Name] = "field"
									add("App."+n.Name, name, "field", "App")
								}
							}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							kind := "var"
							if d.Tok == token.CONST {
								kind = "const"
							}
							add(n.Name, name, kind, "")
						}
					}
				}
			}
		}
	}

	type edge struct {
		from, to string
		count    int
	}
	edges := map[string]*edge{}
	uses := map[string]map[string]int{}    // file -> declaration -> count
	appUses := map[string]map[string]int{} // file -> "field:x"/"method:y" -> count
	importUses := map[string]map[string]int{}

	for _, parsed := range files {
		name := filepath.Base(fset.Position(parsed.Pos()).Filename)
		local := localScopes(parsed)
		ast.Inspect(parsed, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Ident:
				if node.Name == "_" || local[node.Name] {
					return true
				}
				if _, isDecl := decls[node.Name]; !isDecl {
					return true
				}
				record(uses, name, node.Name)
				if def, ok := decls[node.Name]; ok && !ast.IsExported(node.Name) && def.file != name {
					key := name + "\x00" + def.file
					if edges[key] == nil {
						edges[key] = &edge{from: name, to: def.file}
					}
					edges[key].count++
				}
			case *ast.SelectorExpr:
				base, ok := node.X.(*ast.Ident)
				if !ok {
					return true
				}
				if _, isImport := infos[name].imports[base.Name]; isImport {
					record(importUses, name, base.Name+"."+node.Sel.Name)
					return true
				}
				if base.Name == "a" {
					if kind, isMember := appMembers[node.Sel.Name]; isMember {
						record(appUses, name, kind+":"+node.Sel.Name)
					}
				}
			}
			return true
		})
	}

	var report strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&report, format, args...) }

	names := make([]string, 0, len(infos))
	for name := range infos {
		names = append(names, name)
	}
	sort.Strings(names)

	w("# Coupling report: %s\n\n", *dir)
	w("Files (non-test): %d. App members declared: %d. Package-level declarations: %d.\n\n", len(names), len(appMembers), len(decls))

	w("## Per-file coupling\n\n")
	w("| file | lines | funcs | App fields used | App methods used | private refs out | private refs in | fan-out |\n")
	w("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	type row struct {
		name                          string
		lines, funcs                  int
		fields, methods               int
		outPrivate, inPrivate, fanout int
	}
	rows := make([]row, 0, len(names))
	for _, name := range names {
		info := infos[name]
		r := row{name: name, lines: info.lines, funcs: info.funcs}
		for member := range appUses[name] {
			if strings.HasPrefix(member, "field:") {
				r.fields++
			} else {
				r.methods++
			}
		}
		targets := map[string]bool{}
		for _, e := range edges {
			if e.from != name {
				continue
			}
			r.outPrivate += e.count
			targets[e.to] = true
		}
		r.fanout = len(targets)
		for _, e := range edges {
			if e.to == name {
				r.inPrivate += e.count
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for _, r := range rows {
		w("| %s | %d | %d | %d | %d | %d | %d | %d |\n",
			r.name, r.lines, r.funcs, r.fields, r.methods, r.outPrivate, r.inPrivate, r.fanout)
	}

	w("\n## Cross-file private dependencies (out -> in)\n\n")
	keys := make([]string, 0, len(edges))
	for key := range edges {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if edges[keys[i]].from != edges[keys[j]].from {
			return edges[keys[i]].from < edges[keys[j]].from
		}
		return edges[keys[i]].to < edges[keys[j]].to
	})
	for _, key := range keys {
		e := edges[key]
		members := make([]string, 0, len(uses[e.from]))
		for member := range uses[e.from] {
			def := decls[member]
			if def != nil && !ast.IsExported(member) && def.file == e.to {
				members = append(members, member)
			}
		}
		sort.Strings(members)
		w("- %s -> %s (%d refs): %s\n", e.from, e.to, e.count, strings.Join(members, ", "))
	}

	w("\n## App member usage per file\n\n")
	for _, name := range names {
		members := make([]string, 0, len(appUses[name]))
		for member := range appUses[name] {
			members = append(members, member)
		}
		sort.Strings(members)
		w("- %s (%d): %s\n", name, len(members), strings.Join(members, ", "))
	}

	w("\n## App members and where they are used\n\n")
	memberNames := make([]string, 0, len(appMembers))
	for member := range appMembers {
		memberNames = append(memberNames, member)
	}
	sort.Strings(memberNames)
	for _, member := range memberNames {
		users := []string{}
		for _, name := range names {
			if _, ok := appUses[name][appMembers[member]+":"+member]; ok {
				users = append(users, name)
			}
		}
		sort.Strings(users)
		w("- %s %s <- %s\n", appMembers[member], member, strings.Join(users, ", "))
	}

	w("\n## External imports per file\n\n")
	for _, name := range names {
		imports := make([]string, 0, len(infos[name].imports))
		for _, p := range infos[name].imports {
			imports = append(imports, p)
		}
		sort.Strings(imports)
		w("- %s: %s\n", name, strings.Join(imports, ", "))
	}

	// A member nobody uses leaves a trailing space before the newline, which
	// git diff --check rejects; the report is a repository file.
	output := trimTrailingSpaces(report.String())

	if *out != "" {
		if err := os.WriteFile(*out, []byte(output), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "coupling:", err)
			os.Exit(1)
		}
	}
	fmt.Print(output)
}

func trimTrailingSpaces(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func record(m map[string]map[string]int, file, key string) {
	if m[file] == nil {
		m[file] = map[string]int{}
	}
	m[file][key]++
}

func receiverType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// localScopes collects every name declared inside the file (parameters, locals,
// receivers, labels), so that a reference count never mistakes a local variable
// for a package-level declaration of the same name.
func localScopes(file *ast.File) map[string]bool {
	local := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Recv != nil {
				for _, field := range node.Recv.List {
					for _, name := range field.Names {
						local[name.Name] = true
					}
				}
			}
			if node.Type.Params != nil {
				for _, field := range node.Type.Params.List {
					for _, name := range field.Names {
						local[name.Name] = true
					}
				}
			}
		case *ast.FuncLit:
			if node.Type.Params != nil {
				for _, field := range node.Type.Params.List {
					for _, name := range field.Names {
						local[name.Name] = true
					}
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					local[id.Name] = true
				}
			}
		case *ast.ValueSpec:
			for _, name := range node.Names {
				local[name.Name] = true
			}
		case *ast.RangeStmt:
			if id, ok := node.Key.(*ast.Ident); ok {
				local[id.Name] = true
			}
			if id, ok := node.Value.(*ast.Ident); ok {
				local[id.Name] = true
			}
		case *ast.TypeSwitchStmt:
			if assign, ok := node.Assign.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						local[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return local
}
