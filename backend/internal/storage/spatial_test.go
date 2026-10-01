package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLoadEntityPositionsRejectsMissingSourceEvent(t *testing.T) {
	ctx := context.Background()
	store, err := OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InTx(ctx, func(tx *WorldTx) error {
		return tx.SetEntityLocation(ctx, PositionWrite{EntityID: "player", LocationID: "office", SourceEventID: "missing", UpdatedTurn: 0})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.LoadEntityPositions(ctx); err == nil {
		t.Fatal("orphaned position source was accepted")
	}
}
