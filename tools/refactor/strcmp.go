// strcmp compares the string literals of two revisions of this module.
//
// A refactor that changes what a prompt says is a behaviour change even when it compiles
// and every test passes, because the tests that would notice are the ones nobody wrote.
// Comparing the literals themselves is the only check that does not depend on someone
// having anticipated the change: it reports every piece of text that differs, in either
// direction, so an intended rename can be confirmed as the only difference.
//
// It reads the working tree and a directory of files extracted from another revision,
// and reports literals present in one and not the other.
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

func runStrCmp(args []string) {
	before := flag.String("before", "", "directory holding the earlier revision's files")
	after := flag.String("after", "", "directory holding the current files")
	flag.CommandLine.Parse(args)
	if *before == "" || *after == "" {
		flag.Usage()
		os.Exit(2)
	}
	old := literals(*before)
	now := literals(*after)
	for _, text := range sortedKeys(old, now) {
		oldCount, inOld := old[text]
		nowCount, inNow := now[text]
		if inOld && inNow {
			if oldCount != nowCount {
				fmt.Printf("CHANGED count %d -> %d: %s\n", oldCount, nowCount, preview(text))
			}
			continue
		}
		if inOld {
			fmt.Printf("REMOVED: %s\n", preview(text))
			continue
		}
		fmt.Printf("ADDED:   %s\n", preview(text))
	}
}

// literals collects every string literal in a directory, with how often it appears.
// Comments are not literals and are deliberately not compared: a comment may be rewritten
// freely when code moves, and comparing them would bury a prompt change in noise.
func literals(dir string) map[string]int {
	out := map[string]int{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", entry.Name(), err)
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			out[lit.Value]++
			return true
		})
	}
	return out
}

func sortedKeys(a, b map[string]int) []string {
	seen := map[string]bool{}
	for key := range a {
		seen[key] = true
	}
	for key := range b {
		seen[key] = true
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func preview(text string) string {
	if len(text) <= 120 {
		return text
	}
	return text[:120] + "..."
}
