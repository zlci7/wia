// Command move takes named top-level declarations out of one Go file and appends
// them to another, verbatim.
//
// Transcribing declarations by hand is how a migration acquires silent mistakes: a
// copied schema string that looks right but is not. This program copies the exact
// source bytes of each declaration, so a moved function is the same function. The
// destination file's package clause and imports are the operator's responsibility;
// goimports and the compiler settle the rest.
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
	"regexp"
	"strings"
)

func main() {
	from := flag.String("from", "", "file to take the declarations from")
	to := flag.String("to", "", "file to append them to")
	names := flag.String("names", "", "comma-separated declaration names")
	rename := flag.String("rename", "", "comma-separated old=new pairs applied inside the moved text")
	flag.Parse()
	if *from == "" || *to == "" || *names == "" {
		fmt.Fprintln(os.Stderr, "move: -from, -to and -names are required")
		os.Exit(2)
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(*names, ",") {
		wanted[strings.TrimSpace(name)] = true
	}
	// A declaration that becomes exported in its new package is renamed here, so
	// the whole move 鈥?definition and every use inside the moved body 鈥?is one
	// step rather than a move followed by a rename.
	renames := map[string]string{}
	for _, pair := range strings.Split(*rename, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "move: bad -rename entry "+pair)
			os.Exit(2)
		}
		renames[parts[0]] = parts[1]
	}

	fset := token.NewFileSet()
	source, err := parser.ParseFile(fset, *from, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	sourceBytes, err := os.ReadFile(*from)
	if err != nil {
		panic(err)
	}

	var moved []string
	for _, decl := range source.Decls {
		for _, name := range declaredNames(decl) {
			if !wanted[name] {
				continue
			}
			start := fset.Position(decl.Pos()).Offset
			end := fset.Position(decl.End()).Offset
			if doc := declarationDoc(decl); doc != nil {
				start = fset.Position(doc.Pos()).Offset
			}
			moved = append(moved, applyRenames(string(sourceBytes[start:end]), renames))
			break
		}
	}
	if len(moved) == 0 {
		fmt.Fprintln(os.Stderr, "move: nothing matched")
		os.Exit(1)
	}

	target, err := os.ReadFile(*to)
	if err != nil {
		panic(err)
	}
	// The declarations go after the import block, not at the end of the file:
	// appending to the raw bytes puts them before the package clause when the target
	// is a fresh file, which is not valid Go. An earlier version did exactly that.
	targetFile, err := parser.ParseFile(fset, *to, target, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	splice := 0
	for _, decl := range targetFile.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT && gen.End() > token.Pos(splice) {
			splice = fset.Position(gen.End()).Offset
		}
	}
	if splice == 0 {
		splice = fset.Position(targetFile.Name.End()).Offset
	}
	var body bytes.Buffer
	for _, text := range moved {
		body.WriteString("\n\n")
		body.WriteString(text)
	}
	var out bytes.Buffer
	out.Write(target[:splice])
	out.Write(body.Bytes())
	out.Write(target[splice:])
	// go/printer is not used here on purpose: the text is copied byte for byte.
	if err := os.WriteFile(*to, out.Bytes(), 0o644); err != nil {
		panic(err)
	}

	// Remove the moved declarations from the source by rewriting its AST.
	kept := make([]ast.Decl, 0, len(source.Decls))
	for _, decl := range source.Decls {
		drop := false
		for _, name := range declaredNames(decl) {
			if wanted[name] {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, decl)
		}
	}
	source.Decls = kept
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, source); err != nil {
		panic(err)
	}
	if err := os.WriteFile(*from, buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("moved %d declarations: %s\n", len(moved), strings.Join(declaredNamesOf(source.Decls), ", "))
}

// applyRenames replaces whole identifiers inside moved text. A word boundary keeps
// worldStore from also matching inside another name that merely contains it.
func applyRenames(text string, renames map[string]string) string {
	if len(renames) == 0 {
		return text
	}
	pattern := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	return pattern.ReplaceAllStringFunc(text, func(word string) string {
		if replacement, ok := renames[word]; ok {
			return replacement
		}
		return word
	})
}

// declarationDoc returns the comment block that documents a declaration, so it
// travels with the declaration instead of being left behind.
func declarationDoc(decl ast.Decl) *ast.CommentGroup {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return d.Doc
	case *ast.GenDecl:
		return d.Doc
	}
	return nil
}

func declaredNames(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return []string{d.Name.Name}
	case *ast.GenDecl:
		var names []string
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, name := range s.Names {
					names = append(names, name.Name)
				}
			}
		}
		return names
	}
	return nil
}

func declaredNamesOf(decls []ast.Decl) []string {
	var names []string
	for _, decl := range decls {
		names = append(names, declaredNames(decl)...)
	}
	return names
}
