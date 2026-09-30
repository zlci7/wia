package storage

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// databaseCallBaseline is how many uses of the temporary escape hatch exist in
// app when this count was taken.
//
// The escape hatch exists because part of the migration is staged: some call sites
// still carry a raw *sql.DB through their own signatures, and they move to memory,
// corrections, suggestions and promotion when those packages exist. Both numbers are
// allowed to fall and must never rise — a new call is new coupling in the direction
// the refactor is removing, and nothing else in the build would notice.
const (
	databaseProductionBaseline = 23
	databaseTestBaseline       = 103
)

// TestDatabaseEscapeHatchDoesNotSpread counts `Database()` in the narrative package.
//
// It reads the source rather than the compiled package because there is no API to
// observe from the outside: the point is to catch the call as it is written.
func TestDatabaseEscapeHatchDoesNotSpread(t *testing.T) {
	dir := filepath.Join("..", "app")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("app is not present: %v", err)
	}
	fset := token.NewFileSet()
	production, tests := 0, 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		path := filepath.Join(dir, name)
		if _, err := parser.ParseFile(fset, path, nil, 0); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		count := strings.Count(string(mustRead(t, path)), ".Database()")
		if count == 0 {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			tests += count
		} else {
			production += count
		}
	}
	if production > databaseProductionBaseline {
		t.Errorf("Database() is used %d times in production code, above the baseline of %d: "+
			"a new call adds coupling the migration is removing, so route the query through a named "+
			"function or method on WorldStore instead", production, databaseProductionBaseline)
	}
	if tests > databaseTestBaseline {
		t.Errorf("Database() is used %d times in tests, above the baseline of %d: "+
			"seed and assert through the storage package instead of a new raw query",
			tests, databaseTestBaseline)
	}
	t.Logf("Database(): %d production (baseline %d), %d tests (baseline %d)",
		production, databaseProductionBaseline, tests, databaseTestBaseline)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
