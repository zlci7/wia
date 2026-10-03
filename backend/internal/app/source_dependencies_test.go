package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

func TestCommittedCausalChainProtectsCurrentFactsAcrossCopyRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w, err := a.CreateWorld(ctx, "causal-chain", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := readContextSnapshot(t, a, w.WorldID)
	path, _, err := a.worldRecord(ctx, w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	run := wiaworld.Run{RunID: "causal-run"}
	if err = store.InTx(ctx, func(tx *storage.WorldTx) error {
		return tx.InsertRun(ctx, storage.RunWrite{RunID: run.RunID, RequestKey: "causal-run", Status: "running"})
	}); err != nil {
		t.Fatal(err)
	}
	events := []wiaworld.Event{
		{EventID: "old-clue", EventType: "external_fact", ActorID: "world", Content: "An earlier clue."},
		{EventID: "world-result", EventType: "plot_result", BasisEventIDs: []string{"old-clue"}},
		{EventID: "own-projection", EventType: "plot_perceived", TargetID: "npc:clockmaker", ProjectionParentID: "world-result"},
		{EventID: "npc-decision", EventType: "npc_action_intent", ActorID: "npc:clockmaker", BasisEventIDs: []string{"own-projection"}},
		{EventID: "movement-result", EventType: "npc_action_result", ActorID: "npc:clockmaker", ProjectionParentID: "npc-decision"},
	}
	changes := []turn.PositionChange{{EntityID: "npc:clockmaker", To: "abandoned-clinic", SourceEventID: "movement-result", PreviousSourceEventID: "opening"}}
	_, err = commitTurn(ctx, store, run, "A turn was settled.", events, nil, nil, snapshot.Summary.Clock, snapshot.Summary.Scene, snapshot.SceneLocation, snapshot.SceneVersion, nil, snapshot.SceneViews, changes, nil, nil, nil, nil, nil, snapshot.OpenProgress)
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	status, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := a.SaveAs(ctx, w.WorldID, "causal-copy", "copy-causal", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Open(ctx, Options{DataRoot: root, UserID: LocalUserID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, worldID := range []string{w.WorldID, copy.TargetWorldID} {
		path, _, err := a.worldRecord(ctx, worldID)
		if err != nil {
			t.Fatal(err)
		}
		store, err := storage.OpenWorldDB(path)
		if err != nil {
			t.Fatal(err)
		}
		dependents, err := store.LoadCorrectionDependents(ctx, "old-clue")
		store.Close()
		if err != nil || !slices.Contains(dependents, "movement-result") {
			t.Fatalf("causal graph lost after copy/restart: %v %v", dependents, err)
		}
		_, err = a.Correct(ctx, worldID, memory.CorrectionRequest{RequestKey: "correct-causal", ExpectedEpoch: snapshot.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: "old-clue", Replacement: "The clue was mistaken."})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("consumed causal ancestor was corrected: %v", err)
		}
	}
}
