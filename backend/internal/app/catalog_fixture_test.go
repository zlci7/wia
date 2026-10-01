package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func installTestPacks(t *testing.T, dataRoot string) {
	t.Helper()
	root := filepath.Join(dataRoot, "story-app", "story-packs")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"mist-embers", "lantern-dusk", "orbital-repair"} {
		if err := os.Rename(packFixture(t, id), filepath.Join(root, id)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFreshCatalogProvidesMistEmbersInitialStory(t *testing.T) {
	a, err := Open(context.Background(), Options{DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	games := a.Games()
	if len(games) != 1 || games[0].ID != "mist-embers" || games[0].Revision != "mist-embers.pack.v6" || len(a.PackIssues()) != 0 {
		t.Fatalf("initial catalog: games=%v issues=%v", games, a.PackIssues())
	}
	w, err := a.CreateWorld(context.Background(), "initial", "", "", "", false)
	if err != nil || w.GameID != "mist-embers" {
		t.Fatalf("initial local creation: world=%+v err=%v", w, err)
	}
}
