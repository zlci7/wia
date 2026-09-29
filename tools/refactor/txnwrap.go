package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func runTxWrap(args []string) {
	path := flag.String("file", "", "file holding the function")
	fn := flag.String("func", "", "function name")
	outer := flag.String("outer", "", "outer return value names, comma separated (may be empty)")
	decls := flag.String("decls", "", "declarations to hoist before the closure, comma separated")
	debug := flag.Bool("debug", false, "print depth decisions")
	flag.CommandLine.Parse(args)

	raw, err := os.ReadFile(*path)
	if err != nil {
		panic(err)
	}
	text := string(raw)
	lines := strings.Split(text, "\n")

	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func "+*fn+"(") {
			start = i
			break
		}
	}
	if start < 0 {
		panic("function not found: " + *fn)
	}
	// Find the BeginTx block that begins this function's transaction.
	begin := -1
	for i := start; i < len(lines); i++ {
		if strings.Contains(lines[i], "BeginTx(ctx, nil)") {
			begin = i
			break
		}
	}
	if begin < 0 {
		panic("BeginTx not found after " + *fn)
	}
	// The skeleton is: tx, err := ...; if err != nil { return ... } ; defer tx.Rollback()
	end := begin
	for ; end < len(lines); end++ {
		if strings.Contains(lines[end], "defer tx.Rollback()") {
			break
		}
	}
	// Find the matching commit block and the final return.
	commit := -1
	for i := end; i < len(lines); i++ {
		if strings.Contains(lines[i], "tx.Commit()") {
			commit = i
			break
		}
	}
	if commit < 0 {
		panic("Commit not found")
	}
	// The commit is normally an `if err := ...; err != nil {` guard on one line, so the
	// skeleton ends here and the function's own final return follows the closing brace.
	finalReturn := commit + 3
	if !strings.HasPrefix(strings.TrimSpace(lines[finalReturn]), "return ") {
		panic("unexpected shape around commit")
	}
	closeBrace := finalReturn + 1 // the function's closing brace
	if strings.TrimSpace(lines[closeBrace]) != "}" {
		panic("unexpected function end")
	}
	body := append([]string{}, lines[end+1:commit]...)
	// Rewrite depth-1 returns: the closure body's own statements. A return inside a
	// nested function literal belongs to that literal and keeps its value list, so the
	// running depth has to be tracked rather than assumed.
	depth := 1
	hoisted := map[string]bool{}
	for _, name := range strings.Split(*decls, ",") {
		if name = strings.TrimSpace(name); name != "" {
			hoisted[name] = true
		}
	}
	outBody := make([]string, 0, len(body))
	for _, line := range body {
		trimmed := strings.TrimSpace(line)
		opens := strings.Count(line, "{")
		closes := strings.Count(line, "}")
		atDepthOne := depth == 1
		if *debug {
			fmt.Printf("depth=%d one=%v line=%q\n", depth, atDepthOne, trimmed)
		}
		depth += opens - closes
		if atDepthOne || depthTwoReturn(line) {
			// The original declarations are replaced by the hoisted ones.
			if strings.HasPrefix(trimmed, "var ") && strings.HasSuffix(trimmed, " int64") && !strings.Contains(trimmed, "=") {
				names := strings.Split(strings.TrimSuffix(strings.TrimPrefix(trimmed, "var "), " int64"), ",")
				matches := 0
				for _, name := range names {
					if hoisted[strings.TrimSpace(name)] {
						matches++
					}
				}
				if matches == len(names) {
					continue
				}
			}
			if strings.Contains(line, "return 0, ") {
				line = strings.Replace(line, "return 0, ", "return ", 1)
			}
			for name := range hoisted {
				if strings.Contains(line, name+" := ") {
					line = strings.Replace(line, name+" := ", name+" = ", 1)
				}
			}
		}
		outBody = append(outBody, line)
	}

	outerValues := strings.TrimSpace(*outer)
	outerReturn := "return nil"
	if outerValues != "" {
		outerReturn = "return " + outerValues + ", nil"
	}
	var built []string
	built = append(built, lines[:begin]...)
	// The hoisted declarations go before the closure, because the value the transaction
	// produces is returned to the caller after it commits.
	for _, name := range strings.Split(*decls, ",") {
		if name = strings.TrimSpace(name); name != "" {
			built = append(built, "\tvar "+name+" int64")
		}
	}
	built = append(built, "\tif err := store.InTx(ctx, func(tx *storage.WorldTx) error {")
	built = append(built, outBody...)
	built = append(built, "\t\t"+outerReturn)
	built = append(built, "\t}); err != nil {")
	built = append(built, "\t\treturn "+zeroValues(*outer)+", err")
	built = append(built, "\t}")
	built = append(built, lines[finalReturn:]...)

	if err := os.WriteFile(*path, []byte(strings.Join(built, "\n")), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("rewrote %s: transaction now runs through InTx\n", *fn)
}

func zeroValues(outer string) string {
	if strings.TrimSpace(outer) == "" {
		return "0"
	}
	parts := strings.Split(outer, ",")
	zeros := make([]string, 0, len(parts))
	for range parts {
		zeros = append(zeros, "0")
	}
	return strings.Join(zeros, ", ")
}

// depthTwoReturn recognises the closure's own returns inside its single-statement
// guards, such as `if err != nil { return 0, err }` written on one line. Those belong
// to the closure and lose the outer value list; a return inside a nested function
// literal does not, and is never written on the same line as its opening brace.
func depthTwoReturn(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasSuffix(trimmed, "}") {
		return false
	}
	return strings.Contains(trimmed, "return 0, ") && strings.HasPrefix(trimmed, "if ")
}
