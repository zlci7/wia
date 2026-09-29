// importers reports which packages import which, for a chosen set of packages.
//
// It answers the question a deletion has to start with: if these packages were removed,
// who would break? The answer is a list of importers, not a guess from file names —
// a package called "context" might be the story's context or the retired runtime's, and
// the only thing that settles it is who imports it.
package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func runImporters(args []string) {
	root := flag.String("root", "", "module root, e.g. backend")
	targets := flag.String("targets", "", "comma-separated package directories of interest")
	flag.CommandLine.Parse(args)
	if *root == "" || *targets == "" {
		fmt.Fprintln(os.Stderr, "imports: -root and -targets are required")
		os.Exit(2)
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(*targets, ",") {
		wanted[strings.TrimSpace(name)] = true
	}

	// Collect every directory under the root that holds Go files, and what it imports.
	internal := filepath.Join(*root, "internal")
	dirs, err := os.ReadDir(internal)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	importers := map[string][]string{}
	exists := map[string]bool{}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		name := dir.Name()
		exists[name] = true
		files, err := os.ReadDir(filepath.Join(internal, name))
		if err != nil {
			continue
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".go") {
				continue
			}
			parsed, err := parser.ParseFile(fset, filepath.Join(internal, name, file.Name()), nil, parser.ImportsOnly)
			if err != nil {
				continue
			}
			for _, spec := range parsed.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					continue
				}
				// "gameagent/backend/internal/xxx" -> "xxx"
				parts := strings.Split(path, "/")
				for index, part := range parts {
					if part == "internal" && index+1 < len(parts) {
						imported := parts[index+1]
						if wanted[imported] {
							importers[imported] = append(importers[imported], name)
						}
					}
				}
			}
		}
	}
	// Non-internal callers too: the command and any protocol packages.
	for _, extra := range []string{"cmd", ""} {
		_ = extra
	}
	scan(fset, filepath.Join(*root, "cmd"), "cmd", wanted, importers)

	for _, target := range strings.Split(*targets, ",") {
		target = strings.TrimSpace(target)
		seen := map[string]bool{}
		var unique []string
		for _, name := range importers[target] {
			if !seen[name] {
				seen[name] = true
				unique = append(unique, name)
			}
		}
		sort.Strings(unique)
		state := ""
		if !exists[target] {
			state = " (missing)"
		}
		fmt.Printf("%-14s%s imported by %d: %s\n", target, state, len(unique), strings.Join(unique, ", "))
	}
}

func scan(fset *token.FileSet, dir, label string, wanted map[string]bool, importers map[string][]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			parts := strings.Split(path, "/")
			for index, part := range parts {
				if part == "internal" && index+1 < len(parts) {
					if imported := parts[index+1]; wanted[imported] {
						importers[imported] = append(importers[imported], label)
					}
				}
			}
		}
	}
}
