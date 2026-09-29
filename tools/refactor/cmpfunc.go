// cmpfunc compares one function's bytes between two files.
//
// It exists because a shell on Windows cannot be trusted to show this repository's
// Chinese text: reading a UTF-8 file through the wrong code page renders it as mojibake
// while the bytes are fine, and the reverse is also possible. Comparing bytes in Go
// answers the only question that matters after a move — is this the same text, or did
// something rewrite it?
//
// The comparison normalizes nothing but the function's own name and its package
// qualifier, because those legitimately change when a declaration moves.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func runCmpFunc(args []string) {
	left := flag.String("left", "", "file holding the original")
	right := flag.String("right", "", "file holding the moved copy")
	name := flag.String("name", "", "function name in the original file")
	newName := flag.String("new-name", "", "function name in the moved file")
	oldQualifier := flag.String("old-qualifier", "", "package qualifier the original used, e.g. turn.")
	renamedTypes := flag.String("renamed-types", "", "comma-separated old=new pairs the move renamed")
	flag.CommandLine.Parse(args)

	original, err := body(*left, *name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "left:", err)
		os.Exit(2)
	}
	moved, err := body(*right, *newName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "right:", err)
		os.Exit(2)
	}
	// The names are normalized only on the declaration line. Replacing them throughout the
	// body would rewrite any identifier that merely contains the function name —
	// BehaviorPolicy inside wiaworld.BehaviorPolicyVersion, for instance — and report a
	// difference that is not there.
	original = renameDeclaration(original, *name)
	moved = renameDeclaration(moved, *newName)
	original = strings.ReplaceAll(original, *oldQualifier, "")
	// A rename applied on both sides is not a difference in the code that moved. Naming
	// the renames keeps the check meaningful: anything else that differs is still shown.
	for _, pair := range strings.Split(*renamedTypes, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			panic("bad renamed type pair: " + pair)
		}
		original = strings.ReplaceAll(original, parts[0], parts[1])
	}
	if original == moved {
		fmt.Printf("%s: identical (%d bytes)\n", *name, len(moved))
		return
	}
	fmt.Printf("%s: DIFFERENT\n", *name)
	olines := strings.Split(original, "\n")
	mlines := strings.Split(moved, "\n")
	for i := 0; i < len(olines) || i < len(mlines); i++ {
		var a, b string
		if i < len(olines) {
			a = olines[i]
		}
		if i < len(mlines) {
			b = mlines[i]
		}
		if a != b {
			fmt.Printf("  line %d\n    original: %s\n    moved:    %s\n", i+1, a, b)
		}
	}
}

// body returns the text of one function, from its declaration to its closing brace.
func body(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(data)
	// A byte order mark is not part of the source; a shell that wrote the file may have
	// added one, and it would otherwise sit in front of the first declaration.
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	start := strings.Index(text, "func "+name+"(")
	if start < 0 {
		start = strings.Index(text, "const "+name+" =")
	}
	if start < 0 {
		return "", fmt.Errorf("func %s not found", name)
	}
	// A constant has no braces; it ends at the end of its declaration, which may run over
	// several lines of concatenated strings.
	if text[start] == 'c' {
		end := strings.Index(text[start:], "\n\n")
		if end < 0 {
			return text[start:], nil
		}
		return text[start : start+end], nil
	}
	depth := 0
	for index := start; index < len(text); index++ {
		switch text[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : index+1], nil
			}
		}
	}
	return "", fmt.Errorf("func %s has no closing brace", name)
}

// renameDeclaration replaces a declaration's own name on its first line only.
func renameDeclaration(text, name string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return text
	}
	lines[0] = strings.Replace(lines[0], "func "+name+"(", "func NAME(", 1)
	lines[0] = strings.Replace(lines[0], "const "+name+" =", "const NAME =", 1)
	return strings.Join(lines, "\n")
}
