package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func TestInitialConcernsAreSnapshotDataAndPrivate(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "关切", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	for _, c := range s.Characters {
		initial, _ := story.CharacterByID(lanternDefinition(), c.EntityID)
		if c.InitialConcerns == "" || c.InitialConcerns != initial.InitialConcerns {
			t.Fatal("initial concern not copied")
		}
	}
	public, _ := json.Marshal(wiaworld.PublicCharacterViews(s.Characters))
	for _, c := range s.Characters {
		if strings.Contains(string(public), c.InitialConcerns) {
			t.Fatal("private concern in public projection")
		}
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	db, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Database().Close()
	if _, err = db.Database().Exec("DELETE FROM meta WHERE key LIKE 'initial_concerns:%'"); err != nil {
		t.Fatal(err)
	}
	legacy := readContextSnapshot(t, a, w.WorldID)
	for _, c := range legacy.Characters {
		if c.InitialConcerns != "" {
			t.Fatal("legacy world acquired latest definition concern")
		}
	}
}
