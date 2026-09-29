// Command refactor dispatches the mechanical tools used by the R3 module split.
//
// Each subcommand is one tool that performs a bounded, reviewable source rewrite:
// moving declarations between packages, requalifying references, repairing import
// blocks, and taking inventories before a migration. They were separate modules while
// they were being written; they are one module now because thirteen go.mod files in a
// repository is a cost with no benefit — every one of them has the same dependency
// situation (standard library only) and the same lifetime (the migration).
//
// Usage:
//
//	go run ./tools/refactor <command> [flags]
//
// Commands:
//
//	move       move declarations between packages, byte for byte
//	qualify    requalify references after a symbol changes package
//	imports    rebuild a file's import block from a module list
//	importfix  regroup a file's imports without deleting any
//	api        turn a free function taking a *sql.DB into a method at both ends
//	callsite   list calls to named functions with receiver and first argument
//	dropfunc   delete whole function declarations by name
//	txcensus   report which functions open a transaction and what they call
//	txnwrap    rewrite a BeginTx/Commit skeleton into a storage.InTx closure
//
// Every tool prints what it changed and refuses to guess when a shape does not match,
// because a silent partial rewrite is worse than a failed one.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	command, args := os.Args[1], os.Args[2:]
	switch command {
	case "move":
		runMove(args)
	case "qualify":
		runQualify(args)
	case "imports":
		runImports(args)
	case "importfix":
		runImportFix(args)
	case "api":
		runAPI(args)
	case "callsite":
		runCallsite(args)
	case "dropfunc":
		runDropFunc(args)
	case "txcensus":
		runTxCensus(args)
	case "txnwrap":
		runTxWrap(args)
	case "vocab":
		runVocab(args)
	case "cutlines":
		runCutLines(args)
	case "importers":
		runImporters(args)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "refactor: unknown command %q\n\n", command)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `refactor dispatches the mechanical tools used by the R3 module split.

usage: go run ./tools/refactor <command> [flags]

commands:
  move       move declarations between packages, byte for byte
  qualify    requalify references after a symbol changes package
  imports    rebuild a file's import block from a module list
  importfix  regroup a file's imports without deleting any
  api        turn a free function taking a *sql.DB into a method at both ends
  callsite   list calls to named functions with receiver and first argument
  dropfunc   delete whole function declarations by name
  txcensus   report which functions open a transaction and what they call
  txnwrap    rewrite a BeginTx/Commit skeleton into a storage.InTx closure
  vocab      rename a package's vocabulary to the package that now owns it

run a command with -h for its own flags.
`)
}
