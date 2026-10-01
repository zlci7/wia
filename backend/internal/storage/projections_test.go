package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPersonalProjectionPreservesFactAuthorizationWithoutRootText(t *testing.T) {
	ctx := context.Background()
	store, err := OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	err = store.InTx(ctx, func(tx *WorldTx) error {
		for i, event := range []EventWrite{{EventID: "fact", EventType: "player_action_result", Content: "作者真相"}, {EventID: "fact:projection:player", EventType: "action_perceived", TargetID: "player", Content: "玩家只看见表面变化"}} {
			event.Seq = int64(i + 1)
			event.CreatedAt = "now"
			if err := tx.InsertEvent(ctx, event); err != nil {
				return err
			}
		}
		if err := tx.InsertEventDependency(ctx, "fact:projection:player", "fact"); err != nil {
			return err
		}
		return tx.InsertPerceptionIfAbsent(ctx, PerceptionWrite{RecipientID: "player", SourceEventID: "fact:projection:player", SourceType: "action_succeeded", Content: "玩家只看见表面变化", CreatedAt: "now"})
	})
	if err != nil {
		t.Fatal(err)
	}
	known, err := store.LoadPerceivedSources(ctx, []string{"fact"})
	if err != nil || !known["player"]["fact"] || known["npc:foreign"]["fact"] {
		t.Fatalf("authorization=%v err=%v", known, err)
	}
	perceptions, err := store.LoadPerceptions(ctx, "player", 20)
	if err != nil || len(perceptions) != 1 || perceptions[0].Content != "玩家只看见表面变化" {
		t.Fatal("author text replaced personal projection")
	}
}

func TestDialogueHistoryKeepsPlayerPrivateScopeAndExcludesOtherListeners(t *testing.T) {
	ctx := context.Background()
	store, err := OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	err = store.InTx(ctx, func(tx *WorldTx) error {
		if err := tx.InsertRun(ctx, RunWrite{RunID: "r", RequestKey: "key", Status: "completed", CreatedAt: "now", UpdatedAt: "now"}); err != nil {
			return err
		}
		for i, event := range []EventWrite{{EventID: "private-other", EventType: "npc_dialogue", ActorID: "npc:a", TargetID: "npc:b", SourceType: "speech_private", Content: "只给乙听"}, {EventID: "private-player", EventType: "npc_dialogue", ActorID: "npc:a", TargetID: "player", SourceType: "speech_private", Content: "只给玩家听"}, {EventID: "public-player", EventType: "npc_dialogue", ActorID: "npc:b", TargetID: "player", SourceType: "speech_public", Content: "公开回答"}} {
			event.Seq = int64(i + 1)
			event.RunID = "r"
			event.CreatedAt = "now"
			if err := tx.InsertEvent(ctx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dialogue, err := store.LoadDialogue(ctx)
	if err != nil || len(dialogue) != 2 || dialogue[0].EventID != "private-player" || dialogue[0].SourceType != "private" || dialogue[1].SourceType != "public" {
		t.Fatalf("dialogue=%+v err=%v", dialogue, err)
	}
}
