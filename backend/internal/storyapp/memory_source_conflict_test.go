package storyapp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gameagent/backend/internal/storage"
)

func TestIndexMemorySourcesMapsContentConflictToStorageFailure(t *testing.T) {
	ctx := context.Background()
	store, err := storage.OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	err = store.InTx(ctx, func(tx *storage.WorldTx) error {
		if err := tx.InsertEvent(ctx, storage.EventWrite{Seq: 1, EventID: "event:1", EventType: "observed", ActorID: "player", Content: "event", CreatedAt: "now"}); err != nil {
			return err
		}
		if err := tx.InsertPerceptionIfAbsent(ctx, storage.PerceptionWrite{RecipientID: "npc:a", SourceEventID: "event:1", SourceType: "test", Content: "new content", CreatedAt: "now"}); err != nil {
			return err
		}
		_, err := tx.AppendMemorySource(ctx, storage.MemorySourceWrite{Scope: "npc:a", ID: "perception:1", EventID: "event:1", Actor: "player", Kind: "perception:test", Content: "old content", CreatedAt: "now"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = indexMemorySources(ctx, store); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("index conflict error = %v, want ErrStorageUnavailable", err)
	}
	stored, err := store.LoadMemorySources(ctx, "npc:a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Content != "old content" {
		t.Fatalf("conflict changed stored source: %+v", stored)
	}
}
