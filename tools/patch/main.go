// Command patch applies exact text replacements listed in a control file.
//
// It treats the source as UTF-8 with CRLF line endings and never re-encodes it:
// an earlier attempt to edit Go source through PowerShell read and wrote the files
// with the system code page, which turned every non-ASCII literal into mojibake
// while leaving the line count unchanged. This program exists so that a precise
// multi-line edit cannot damage the file it edits.
//
// Control file format, one edit per record, blank line between records:
//
//	=== file path
//	--- old
//	<exact lines, CRLF or LF>
//	--- new
//	<exact lines>
//
// Every old block must occur exactly once; the program refuses to write if any
// block is missing or ambiguous, so a partial edit can never be left behind.
package main

import (
	"fmt"
	"os"
	"strings"
)

type edit struct {
	file string
	old  string
	new  string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "patch: control file is required")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	edits, err := parse(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "patch:", err)
		os.Exit(2)
	}
	applied := 0
	for _, item := range edits {
		raw, err := os.ReadFile(item.file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "patch: %v\n", err)
			os.Exit(1)
		}
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		count := strings.Count(text, item.old)
		if count != 1 {
			fmt.Fprintf(os.Stderr, "patch: %s: block occurs %d times, want 1:\n%s\n", item.file, count, firstLine(item.old))
			os.Exit(1)
		}
		text = strings.Replace(text, item.old, item.new, 1)
		if err := os.WriteFile(item.file, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0o644); err != nil {
			panic(err)
		}
		applied++
	}
	fmt.Printf("applied %d edits\n", applied)
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

func parse(text string) ([]edit, error) {
	var edits []edit
	for _, record := range strings.Split(text, "\n=== ") {
		record = strings.TrimPrefix(record, "=== ")
		if strings.TrimSpace(record) == "" {
			continue
		}
		lines := strings.Split(record, "\n")
		item := edit{}
		// first line names the file
		item.file = strings.TrimSpace(lines[0])
		if item.file == "" {
			return nil, fmt.Errorf("record without a file name")
		}
		var section []string
		mode := ""
		for _, line := range lines[1:] {
			switch {
			case line == "--- old":
				mode = "old"
			case line == "--- new":
				if mode == "old" {
					item.old = strings.Join(section, "\n")
				}
				section = nil
				mode = "new"
			default:
				if mode != "" {
					section = append(section, line)
				}
			}
		}
		if mode == "new" {
			item.new = strings.Join(section, "\n")
		} else if mode == "old" {
			item.old = strings.Join(section, "\n")
		}
		if item.old == "" {
			return nil, fmt.Errorf("%s: no --- old block", item.file)
		}
		edits = append(edits, item)
	}
	return edits, nil
}
