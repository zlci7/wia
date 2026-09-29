// Command importfix repairs import blocks that were rebuilt by a tool with a
// whitelist: it puts the standard library back into its own group, keeps
// side-effect imports, sorts both groups, and deletes nothing.
//
// A whitelist-based rewriter is useful for adding an import and wrong for repairing
// a file, because it drops what it does not recognise — including registrations such
// as image/jpeg or a SQL driver, which are never referenced by name and whose
// absence shows up only at run time. This tool never removes an import.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	path := flag.String("file", "", "file whose import block should be repaired")
	flag.Parse()
	if *path == "" {
		fmt.Fprintln(os.Stderr, "importfix: -file is required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		panic(err)
	}
	text := string(raw)
	start := strings.Index(text, "\nimport (\n")
	if start < 0 {
		fmt.Printf("%s: no grouped import block\n", *path)
		return
	}
	open := start + len("\nimport (")
	end := strings.Index(text[open:], "\n)\n")
	if end < 0 {
		panic("unterminated import block")
	}
	end += open
	body := text[open:end]

	std, module := []string{}, []string{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// A quoted path decides the group: a domain in the first segment means an
		// external module. A single-word first segment such as "modernc" does not, so
		// the check looks for a dot in that segment rather than a dot anywhere.
		quote := strings.Index(trimmed, `"`)
		if quote < 0 {
			continue
		}
		path := strings.Trim(trimmed[quote:], `"`)
		path = strings.TrimPrefix(path, "_ ")
		first := strings.SplitN(path, "/", 2)[0]
		target := &std
		if strings.Contains(first, ".") || strings.HasPrefix(path, "gameagent/") {
			target = &module
		}
		*target = append(*target, trimmed)
	}
	sort.Strings(std)
	sort.Strings(module)
	var rebuilt bytes.Buffer
	for _, line := range std {
		rebuilt.WriteString("\t" + line + "\n")
	}
	if len(module) > 0 {
		rebuilt.WriteString("\n")
		for _, line := range module {
			rebuilt.WriteString("\t" + line + "\n")
		}
	}
	out := text[:open] + "\n" + rebuilt.String() + text[end:]
	if out == text {
		fmt.Printf("%s: already grouped\n", *path)
		return
	}
	if err := os.WriteFile(*path, []byte(out), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("%s: %d standard, %d module\n", *path, len(std), len(module))
}
