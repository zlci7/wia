package main

import (
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dropFuncFixture = `package sample

// keepBefore is a function that stays.
func keepBefore() {
	// its own inline comment stays
	_ = 1
}

// dropMe is the function to be removed.
//
// Its doc comment spans two paragraphs.
func dropMe() {
	// an inline comment inside the removed body
	_ = 2
} // a trailing comment on the closing line

// keepAfter is the next function.
func keepAfter() {
	_ = 3
}
`

func writeFixture(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestDropFuncRemovesTheDeclarationWithItsCommentsAndNothingElse pins the four things a
// removal can get wrong: it can leave the doc comment, leave the comments inside the body,
// take a neighbour's comments with it, or write a file that is no longer gofmt-shaped.
func TestDropFuncRemovesTheDeclarationWithItsCommentsAndNothingElse(t *testing.T) {
	path := writeFixture(t, dropFuncFixture)
	runDropFunc([]string{"-dir", filepath.Dir(path), "-names", "dropMe"})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	got := string(raw)

	for _, want := range []string{
		"keepBefore is a function that stays",
		"its own inline comment stays",
		"keepAfter is the next function",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("removing dropMe also removed a neighbour's comment: %q is gone\n%s", want, got)
		}
	}
	for _, gone := range []string{
		"dropMe",
		"dropMe is the function to be removed",
		"Its doc comment spans two paragraphs",
		"an inline comment inside the removed body",
		"a trailing comment on the closing line",
	} {
		if strings.Contains(got, gone) {
			t.Errorf("dropping the declaration left its comment behind: %q survives\n%s", gone, got)
		}
	}
	if _, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments); err != nil {
		t.Errorf("the result does not parse: %v\n%s", err, got)
	}
	canonical, err := format.Source(raw)
	if err != nil {
		t.Fatalf("format result: %v", err)
	}
	if string(canonical) != got {
		t.Errorf("the result is not in gofmt form:\ngot:\n%s\nwant:\n%s", got, canonical)
	}
}

// TestDropFuncLeavesAFileItDoesNotMatchAlone keeps the tool from rewriting a file — and
// from spending a diff — when the requested function is not there.
func TestDropFuncLeavesAFileItDoesNotMatchAlone(t *testing.T) {
	path := writeFixture(t, dropFuncFixture)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	runDropFunc([]string{"-dir", filepath.Dir(path), "-names", "notInThisFile"})
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("a file with no matching function was rewritten")
	}
}
