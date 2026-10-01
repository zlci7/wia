package storyapi

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func apiStoryPacks(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, source := range []string{filepath.Join("..", "content", "packs"), filepath.Join("..", "content", "testdata", "legacy-packs")} {
		err := fs.WalkDir(os.DirFS(source), ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			target := filepath.Join(root, filepath.FromSlash(name))
			if entry.IsDir() {
				return os.MkdirAll(target, 0755)
			}
			data, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return root
}
