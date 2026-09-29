// unqualify removes a package's own qualifier from the files of that package,
// and renames declarations to their exported form.
//
// Moving declarations into a package leaves them referring to themselves by the name
// they had in the package they came from — turn.Snapshot inside package turn, which is
// not a thing. This is the mechanical half of a move; the other half is deciding what the
// names should be.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func runUnqualify(args []string) {
	dir := flag.String("dir", "", "package directory")
	pkg := flag.String("pkg", "", "qualifier to strip, e.g. turn")
	exports := flag.String("exports", "", "comma-separated old=New pairs to export")
	flag.CommandLine.Parse(args)
	if *dir == "" || *pkg == "" {
		flag.Usage()
		os.Exit(2)
	}
	pairs := [][2]string{}
	for _, pair := range strings.Split(*exports, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			panic("bad export pair: " + pair)
		}
		pairs = append(pairs, [2]string{parts[0], parts[1]})
	}
	// Longer names first, so Snapshot is not renamed inside SnapshotError.
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i][0]) > len(pairs[j][0]) })

	entries, err := os.ReadDir(*dir)
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(*dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		text := string(data)
		original := text
		self := regexp.MustCompile(`\b` + regexp.QuoteMeta(*pkg) + `\.`)
		text = self.ReplaceAllString(text, "")
		for _, pair := range pairs {
			text = regexp.MustCompile(`\b`+regexp.QuoteMeta(pair[0])+`\b`).ReplaceAllString(text, pair[1])
		}
		if text == original {
			continue
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			panic(err)
		}
		fmt.Printf("  %s: rewritten\n", entry.Name())
	}
}
