// cutlines deletes an inclusive range of lines from a file.
//
// It exists for the narrow case where a rewrite left a declaration syntactically invalid
// — a type whose name was qualified in place — so the AST-based tools can no longer parse
// the file to remove it. The ranges are given explicitly and printed before and after, so
// the edit is reviewable rather than inferred.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func runCutLines(args []string) {
	path := flag.String("file", "", "file to edit")
	ranges := flag.String("ranges", "", "semicolon-separated ranges like 139-144;218-239")
	apply := flag.Bool("apply", false, "write the result; without it, only report")
	flag.CommandLine.Parse(args)
	if *path == "" || *ranges == "" {
		fmt.Fprintln(os.Stderr, "cutlines: -file and -ranges are required")
		os.Exit(2)
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		panic(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	drop := map[int]bool{}
	for _, spec := range strings.Split(*ranges, ";") {
		if strings.TrimSpace(spec) == "" {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(spec), "-", 2)
		if len(parts) != 2 {
			panic("bad range: " + spec)
		}
		from, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			panic(err)
		}
		to, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			panic(err)
		}
		for i := from; i <= to; i++ {
			drop[i] = true
		}
	}
	kept := make([]string, 0, len(lines))
	for index, line := range lines {
		if drop[index+1] {
			fmt.Printf("  drop %d: %s\n", index+1, line)
			continue
		}
		kept = append(kept, line)
	}
	if !*apply {
		fmt.Printf("would remove %d lines; pass -apply to write\n", len(drop))
		return
	}
	if err := os.WriteFile(*path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("removed %d lines\n", len(drop))
}
