package app

import (
	"context"
	"errors"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/storage"
)

func TestReadDigestRejectsUnverifiableCoverage(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &scriptedGenerator{})
	world, err := app.createFixtureWorld(ctx, "摘要覆盖", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := app.worldRecord(ctx, world.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InTx(ctx, func(tx *storage.WorldTx) error {
		for _, id := range []string{"source:1", "source:2"} {
			if _, appendErr := tx.AppendMemorySource(ctx, storage.MemorySourceWrite{Scope: "npc:innkeeper", ID: id, EventID: "event", RunID: "run", Actor: "player", Kind: "perception:test", Content: id, CreatedAt: "now"}); appendErr != nil {
				return appendErr
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	conflict, err := store.CompareAndInsertMemoryDigest(ctx, storage.MemoryDigestWrite{Scope: "npc:innkeeper", Revision: 1, Epoch: world.ContextEpoch, Through: 2, ExpectedHead: 2, ExpectedEpoch: world.ContextEpoch, ExpectedRevision: 0, Content: "summary", States: "[]", Sources: `["source:1"]`, CreatedAt: "now"})
	if err != nil || conflict.Epoch || conflict.Revision != 0 || conflict.Head != 2 {
		t.Fatalf("insert corrupt fixture: conflict=%+v err=%v", conflict, err)
	}
	if _, err = memory.ReadDigest(ctx, store, "npc:innkeeper"); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("unverifiable digest was accepted: %v", err)
	}
}
