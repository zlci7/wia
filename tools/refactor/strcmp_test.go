package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiteralsWalksNestedPackages(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "one", "two")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("make nested package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "prompt.go"), []byte("package two\nconst prompt = `nested prompt`\n"), 0o644); err != nil {
		t.Fatalf("write nested source: %v", err)
	}

	got := literals(root)
	if got["`nested prompt`"] != 1 {
		t.Fatalf("nested literal count = %d, want 1", got["`nested prompt`"])
	}
}

func TestLiteralsRejectsAnEmptyTree(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("literals accepted a tree with no Go files")
		}
	}()
	literals(t.TempDir())
}
