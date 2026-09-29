// Command txto replaces the raw *sql.Tx parameter with the storage transaction type
// throughout one package.
//
// It rewrites three things together, because doing them separately leaves the package
// uncompilable in between and the mistakes are then hard to tell apart:
//
//	storage.MetaGetTx(ctx, tx, ...)  ->  tx.GetMeta(ctx, ...)
//	storage.MetaSetTx(ctx, tx, ...)  ->  tx.SetMeta(ctx, ...)
//	* *sql.Tx parameters and variables -> *storage.WorldTx
//
// It reports every file it touches and what it changed, so the result can be reviewed
// against the caller list rather than trusted.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	dir := flag.String("dir", "", "package directory")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "txto: -dir is required")
		os.Exit(2)
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		panic(err)
	}
	// The call rewrites are anchored on the argument list, so a nested call inside the
	// arguments is not silently swallowed: only up to the first closing paren of the
	// moved arguments is consumed.
	getCall := regexp.MustCompile(`storage\.MetaGetTx\((\w+), (\w+), `)
	setCall := regexp.MustCompile(`storage\.MetaSetTx\((\w+), (\w+), `)
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
		// The signature is MetaGetTx(ctx, tx, key): the context comes first and the
		// handle second, so the rewrite moves the handle out front and keeps the context.
		for _, match := range getCall.FindAllStringSubmatch(text, -1) {
			if match[2] != "tx" {
				fmt.Printf("  %s: skipped a MetaGetTx whose handle is not tx (%s)\n", entry.Name(), match[2])
			}
		}
		text = getCall.ReplaceAllStringFunc(text, func(s string) string {
			m := getCall.FindStringSubmatch(s)
			if m[2] != "tx" {
				return s
			}
			return m[2] + ".GetMeta(" + m[1] + ", "
		})
		text = setCall.ReplaceAllStringFunc(text, func(s string) string {
			m := setCall.FindStringSubmatch(s)
			if m[2] != "tx" {
				return s
			}
			return m[2] + ".SetMeta(" + m[1] + ", "
		})
		text = strings.ReplaceAll(text, "tx *sql.Tx", "tx *storage.WorldTx")
		text = strings.ReplaceAll(text, "var tx *sql.Tx", "var tx *storage.WorldTx")
		if text == original {
			continue
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			panic(err)
		}
		fmt.Printf("  %s: rewritten\n", entry.Name())
	}
}
