// Command refactor dispatches the mechanical tools used by the R3 module split.
//
// The tools are their own module. That keeps them out of the application build:
// `go build ./...` and `go test ./...` at the repository root never see them. Run them
// from this directory:
//
//	cd tools/refactor
//	go run . <command> [flags]
//
// They were separate modules while they were being written; they are one module now
// because thirteen go.mod files in a repository is a cost with no benefit — every one
// of them has the same dependency situation (standard library only) and the same
// lifetime (the migration).
//
// The command table below is the single list of subcommands: dispatch and usage are
// both derived from it, so a new tool cannot be added without appearing in the help.
// That matters because the help is where the next reader learns which verification
// tools exist, and a help that silently omits the verification tools is worse than no
// help at all.
//
// Every tool prints what it changed and refuses to guess when a shape does not match,
// because a silent partial rewrite is worse than a failed one.
package main

import (
	"fmt"
	"os"
)

// command is one subcommand: its name, its one-line description, and its entry point.
type command struct {
	name string
	help string
	run  func(args []string)
}

var commands = []command{
	{"move", "move declarations between packages, byte for byte", runMove},
	{"qualify", "requalify references after a symbol changes package", runQualify},
	{"imports", "rebuild a file's import block from a module list", runImports},
	{"importfix", "regroup a file's imports without deleting any", runImportFix},
	{"api", "turn a free function taking a *sql.DB into a method at both ends", runAPI},
	{"callsite", "list calls to named functions with receiver and first argument", runCallsite},
	{"dropfunc", "delete whole function declarations by name", runDropFunc},
	{"txcensus", "report which functions open a transaction and what they call", runTxCensus},
	{"txnwrap", "rewrite a BeginTx/Commit skeleton into a storage.InTx closure", runTxWrap},
	{"vocab", "rename a package's vocabulary to the package that now owns it", runVocab},
	{"cutlines", "delete a line range from a file the AST tools can no longer parse", runCutLines},
	{"importers", "report which packages import a chosen set of packages", runImporters},
	{"unqualify", "strip a package's own qualifier and export its declarations", runUnqualify},
	{"cmpfunc", "compare one declaration's bytes between two files", runCmpFunc},
	{"strcmp", "compare every string literal of two revisions of the module", runStrCmp},
	{"closure", "report the transitive dependency set of named functions", runClosure},
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name, args := os.Args[1], os.Args[2:]
	if name == "-h" || name == "--help" || name == "help" {
		usage()
		return
	}
	for _, cmd := range commands {
		if cmd.name == name {
			cmd.run(args)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "refactor: unknown command %q\n\n", name)
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, `refactor dispatches the mechanical tools used by the R3 module split.

It is a separate module, so it is not part of the root build. Run it from its own
directory:

  cd tools/refactor
  go run . <command> [flags]

commands:
`)
	for _, cmd := range commands {
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", cmd.name, cmd.help)
	}
	fmt.Fprint(os.Stderr, "\nrun a command with -h for its own flags.\n")
}
