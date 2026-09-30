package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAppendMemorySourceReportsInsertRepeatAndContentConflict(t *testing.T) {
	ctx := context.Background()
	store, err := OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record := MemorySourceWrite{Scope: "npc:a", ID: "source:1", EventID: "event:1", RunID: "run:1", Actor: "player", Kind: "perception:test", Content: "first", CreatedAt: "now"}
	changed := record
	changed.Content = "different"
	var results []MemorySourceAppendResult
	err = store.InTx(ctx, func(tx *WorldTx) error {
		for _, candidate := range []MemorySourceWrite{record, record, changed} {
			result, appendErr := tx.AppendMemorySource(ctx, candidate)
			if appendErr != nil {
				return appendErr
			}
			results = append(results, result)
		}
		second := record
		second.ID = "source:2"
		second.Content = "second"
		result, appendErr := tx.AppendMemorySource(ctx, second)
		results = append(results, result)
		return appendErr
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []MemorySourceAppendResult{MemorySourceInserted, MemorySourceUnchanged, MemorySourceContentConflict, MemorySourceInserted}
	if len(results) != len(want) {
		t.Fatalf("results = %v, want %v", results, want)
	}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("results[%d] = %v, want %v", i, results[i], want[i])
		}
	}
	stored, err := store.LoadMemorySources(ctx, record.Scope, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[0].Seq != 1 || stored[0].Content != "first" || stored[1].Seq != 2 || stored[1].Content != "second" {
		t.Fatalf("stored sources = %+v", stored)
	}
	identities, err := store.LoadMemorySourceIdentities(ctx, record.Scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0] != (MemorySourceIdentity{Scope: record.Scope, Seq: 1, ID: record.ID}) {
		t.Fatalf("source identities = %+v", identities)
	}
}
