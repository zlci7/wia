package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	wiaworld "gameagent/backend/internal/world"
)

func TestOrderedEffectIndexUpgradePreservesHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "world.db")
	store, err := OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TestingDB().Exec(`
 DROP INDEX idx_state_change_order;
 DROP INDEX idx_item_transfer_order;
 CREATE UNIQUE INDEX idx_state_change_effect ON state_changes(source_event_id,entity_id,state_id);
 CREATE UNIQUE INDEX idx_item_transfer_effect ON item_transfers(source_event_id,instance_id);
 INSERT INTO state_changes VALUES('old-state','player','strain','{"type":"integer"}','{"type":"integer","integer":1}','event',1,1);
 INSERT INTO item_transfers VALUES('old-item','token','player','','npc:a','','event',1,2);
 `); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.TestingDB().Exec(`
 INSERT INTO state_changes VALUES('next-state','player','strain','{"type":"integer","integer":1}','{"type":"integer","integer":2}','event',1,3);
 INSERT INTO item_transfers VALUES('next-item','token','npc:a','','npc:b','','event',1,4);
 `); err != nil {
		t.Fatal(err)
	}
	var states, items int
	if err = store.TestingDB().QueryRow(`SELECT (SELECT COUNT(*) FROM state_changes),(SELECT COUNT(*) FROM item_transfers)`).Scan(&states, &items); err != nil {
		t.Fatal(err)
	}
	if states != 2 || items != 2 {
		t.Fatalf("state/item history=%d/%d", states, items)
	}
	if _, err = store.TestingDB().Exec(`INSERT INTO state_changes VALUES('duplicate','player','strain','{}','{}','event',1,3)`); err == nil {
		t.Fatal("duplicate effect order accepted")
	}
	for _, input := range []string{"first", "second"} {
		if _, _, err = store.PrepareActionResolution(ctx, ActionResolutionRecord{InputID: input, RuleID: "risk", Roll: 1, Target: 50, ModifiersJSON: "[]", CreatedAt: "now"}); err != nil {
			t.Fatal(err)
		}
		if err = store.InTx(ctx, func(tx *WorldTx) error {
			return tx.SettleActionResolution(ctx, input, "risk", "succeeded", "rule:"+input)
		}); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := store.LoadStructuredFactSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(sources, "rule:first") || !slices.Contains(sources, "rule:second") {
		t.Fatalf("correction sources=%v", sources)
	}
}

func TestMechanicFactsAndHistoryPersistAtomically(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "world.db")
	store, err := OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeState := wiaworld.EntityState{EntityID: "player", StateID: "custom_resource", Value: wiaworld.StateValue{Type: "integer", Integer: 4}, SourceEvent: "opening", Version: 1}
	afterState := beforeState
	afterState.Value.Integer = 7
	afterState.SourceEvent = "result:1"
	afterState.UpdatedTurn = 1
	afterState.Version = 2
	beforeRelation := wiaworld.Relationship{SubjectID: "npc:a", TargetID: "player", RelationType: "confidence", Value: 0, SourceEvent: "opening", Version: 1}
	afterRelation := beforeRelation
	afterRelation.Value = 2
	afterRelation.SourceEvent = "result:1"
	afterRelation.UpdatedTurn = 1
	afterRelation.Version = 2
	beforeItem := wiaworld.ItemInstance{InstanceID: "item-1", DefinitionID: "token", HolderID: "player", SourceEvent: "opening", Version: 1}
	afterItem := beforeItem
	afterItem.HolderID = "npc:a"
	afterItem.SourceEvent = "result:1"
	afterItem.UpdatedTurn = 1
	afterItem.Version = 2
	err = store.InTx(ctx, func(tx *WorldTx) error {
		if err := tx.SetEntityState(ctx, beforeState); err != nil {
			return err
		}
		if err := tx.SetRelationship(ctx, beforeRelation); err != nil {
			return err
		}
		if err := tx.SetItem(ctx, beforeItem); err != nil {
			return err
		}
		if err := tx.InsertStateChange(ctx, StateChangeWrite{ChangeID: "change-state", Before: beforeState, After: afterState, SourceEventID: "result:1", TurnSeq: 1, Order: 1}); err != nil {
			return err
		}
		if err := tx.SetEntityState(ctx, afterState); err != nil {
			return err
		}
		if err := tx.InsertRelationshipChange(ctx, RelationshipChangeWrite{ChangeID: "change-relation", Before: beforeRelation, After: afterRelation, SourceEventID: "result:1", TurnSeq: 1, Order: 2}); err != nil {
			return err
		}
		if err := tx.SetRelationship(ctx, afterRelation); err != nil {
			return err
		}
		if err := tx.InsertItemTransfer(ctx, ItemTransferWrite{TransferID: "transfer-item", Before: beforeItem, After: afterItem, SourceEventID: "result:1", TurnSeq: 1, Order: 3}); err != nil {
			return err
		}
		return tx.SetItem(ctx, afterItem)
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	states, err := store.LoadEntityStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	relations, err := store.LoadRelationships(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.LoadItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if states["player"]["custom_resource"].Value.Integer != 7 || relations[0].Value != 2 || items["item-1"].HolderID != "npc:a" {
		t.Fatalf("facts=%+v %+v %+v", states, relations, items)
	}
	var stateCount, relationCount, itemCount int
	if err := store.TestingDB().QueryRow(`SELECT (SELECT COUNT(*) FROM state_changes),(SELECT COUNT(*) FROM relationship_changes),(SELECT COUNT(*) FROM item_transfers)`).Scan(&stateCount, &relationCount, &itemCount); err != nil {
		t.Fatal(err)
	}
	if stateCount != 1 || relationCount != 1 || itemCount != 1 {
		t.Fatalf("history=%d/%d/%d", stateCount, relationCount, itemCount)
	}
}

func TestOpenWorldDBUpgradesPreMechanicsDatabaseWithoutChangingExistingData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	store, err := OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.InTx(ctx, func(tx *WorldTx) error { return tx.SetMeta(ctx, "legacy_marker", "kept") }); err != nil {
		t.Fatal(err)
	}
	store.Close()
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`DROP TABLE item_transfers; DROP TABLE item_instances; DROP TABLE relationship_changes; DROP TABLE relationships; DROP TABLE state_changes; DROP TABLE entity_states;`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()
	store, err = OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	marker, err := store.MetaGet(ctx, "legacy_marker")
	if err != nil || marker != "kept" {
		t.Fatalf("legacy data changed: marker=%q err=%v", marker, err)
	}
	for _, table := range []string{"entity_states", "state_changes", "relationships", "relationship_changes", "item_instances", "item_transfers"} {
		var found int
		if err := store.TestingDB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil || found != 1 {
			t.Fatalf("table %s was not migrated: found=%d err=%v", table, found, err)
		}
	}
}

func TestOpenWorldDBRollsBackEntireSchemaUpgradeOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken-legacy.db")
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
CREATE TABLE legacy_marker (value TEXT NOT NULL);
INSERT INTO legacy_marker(value) VALUES ('kept');
CREATE TABLE relationship_changes (
  change_id TEXT PRIMARY KEY, subject_id TEXT NOT NULL, target_id TEXT NOT NULL, relation_type TEXT NOT NULL,
  before_value INTEGER NOT NULL, after_value INTEGER NOT NULL, source_event_id TEXT NOT NULL,
  proposal_source_event_id TEXT NOT NULL, turn_seq INTEGER NOT NULL, effect_order INTEGER NOT NULL
);
INSERT INTO relationship_changes VALUES
  ('change-1','npc:a','player','trust',0,1,'result-1','proposal-1',1,1),
  ('change-2','npc:a','player','trust',1,2,'result-2','proposal-1',2,1);`)
	if closeErr := raw.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	if store, openErr := OpenWorldDB(path); openErr == nil {
		store.Close()
		t.Fatal("upgrade with duplicate proposal effects unexpectedly succeeded")
	}

	raw, err = sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var createdTables, changes int
	var marker string
	if err = raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('entity_states','state_changes','relationships','item_instances','item_transfers')`).Scan(&createdTables); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRow(`SELECT COUNT(*) FROM relationship_changes`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if err = raw.QueryRow(`SELECT value FROM legacy_marker`).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if createdTables != 0 || changes != 2 || marker != "kept" {
		t.Fatalf("failed migration changed database: new_tables=%d changes=%d marker=%q", createdTables, changes, marker)
	}
}

func TestMechanicTransactionRollsBackFactsHistoryEventsAndMeta(t *testing.T) {
	ctx := context.Background()
	store, err := OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	forced := errors.New("forced rollback")
	state := wiaworld.EntityState{EntityID: "player", StateID: "resource", Value: wiaworld.StateValue{Type: "integer", Integer: 1}, SourceEvent: "event:1", Version: 1}
	relation := wiaworld.Relationship{SubjectID: "npc:a", TargetID: "player", RelationType: "trust", Value: 1, SourceEvent: "event:1", Version: 1}
	item := wiaworld.ItemInstance{InstanceID: "item:1", DefinitionID: "token", HolderID: "player", SourceEvent: "event:1", Version: 1}
	err = store.InTx(ctx, func(tx *WorldTx) error {
		if err := tx.InsertEvent(ctx, EventWrite{Seq: 1, EventID: "event:1", EventType: "player_action_result", ActorID: "player", RunID: "run", Stage: 3, SceneVersion: 1, SourceType: "action_succeeded", CreatedAt: "now"}); err != nil {
			return err
		}
		if err := tx.SetEntityState(ctx, state); err != nil {
			return err
		}
		if err := tx.SetRelationship(ctx, relation); err != nil {
			return err
		}
		if err := tx.SetItem(ctx, item); err != nil {
			return err
		}
		if err := tx.InsertStateChange(ctx, StateChangeWrite{ChangeID: "state:1", Before: state, After: state, SourceEventID: "event:1"}); err != nil {
			return err
		}
		if err := tx.InsertRelationshipChange(ctx, RelationshipChangeWrite{ChangeID: "relation:1", Before: relation, After: relation, SourceEventID: "event:1", ProposalSourceEventID: "event:1"}); err != nil {
			return err
		}
		if err := tx.InsertItemTransfer(ctx, ItemTransferWrite{TransferID: "item-transfer:1", Before: item, After: item, SourceEventID: "event:1"}); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "context_epoch", "2"); err != nil {
			return err
		}
		return forced
	})
	if !errors.Is(err, forced) {
		t.Fatalf("rollback error=%v", err)
	}
	var facts, history, events, meta int
	if err := store.TestingDB().QueryRow(`SELECT
 (SELECT COUNT(*) FROM entity_states)+(SELECT COUNT(*) FROM relationships)+(SELECT COUNT(*) FROM item_instances),
 (SELECT COUNT(*) FROM state_changes)+(SELECT COUNT(*) FROM relationship_changes)+(SELECT COUNT(*) FROM item_transfers),
 (SELECT COUNT(*) FROM events),(SELECT COUNT(*) FROM meta WHERE key='context_epoch')`).Scan(&facts, &history, &events, &meta); err != nil {
		t.Fatal(err)
	}
	if facts != 0 || history != 0 || events != 0 || meta != 0 {
		t.Fatalf("rollback left facts=%d history=%d events=%d meta=%d", facts, history, events, meta)
	}
}

func TestLoadPerceivedSourcesIsIndependentFromRecentPerceptionWindow(t *testing.T) {
	ctx := context.Background()
	store, err := OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	err = store.InTx(ctx, func(tx *WorldTx) error {
		if err := tx.InsertEvent(ctx, EventWrite{Seq: 1, EventID: "current-fact", EventType: "player_action_result", ActorID: "player", RunID: "run", Stage: 3, SceneVersion: 1, SourceType: "action_succeeded", CreatedAt: "now"}); err != nil {
			return err
		}
		for _, item := range []PerceptionWrite{
			{RecipientID: "player", SourceEventID: "current-fact", SourceType: "action_succeeded", Content: "已看见", CreatedAt: "now"},
			{RecipientID: "npc:a", SourceEventID: "current-fact", SourceType: "action_succeeded", Content: "也看见", CreatedAt: "now"},
			{RecipientID: "player", SourceEventID: "unrelated", SourceType: "action_succeeded", Content: "无关", CreatedAt: "now"},
		} {
			if err := tx.InsertPerceptionIfAbsent(ctx, item); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	known, err := store.LoadPerceivedSources(ctx, []string{"current-fact"})
	if err != nil {
		t.Fatal(err)
	}
	if !known["player"]["current-fact"] || !known["npc:a"]["current-fact"] || known["player"]["unrelated"] {
		t.Fatalf("known sources=%+v", known)
	}
	if _, err = store.LoadPerceivedSources(ctx, []string{"missing-source"}); err == nil {
		t.Fatal("missing current fact source was accepted")
	}
}

func TestLoadAppliedRelationshipSourcesFiltersToCurrentCandidates(t *testing.T) {
	ctx := context.Background()
	store, err := OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	before := wiaworld.Relationship{SubjectID: "npc:a", TargetID: "player", RelationType: "trust"}
	after := before
	after.Value = 1
	err = store.InTx(ctx, func(tx *WorldTx) error {
		for index, source := range []string{"current-source", "old-source"} {
			if err := tx.InsertRelationshipChange(ctx, RelationshipChangeWrite{ChangeID: "change:" + source, Before: before, After: after, SourceEventID: "result:" + source, ProposalSourceEventID: source, TurnSeq: int64(index + 1)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := store.LoadAppliedRelationshipSources(ctx, []string{"current-source"})
	if err != nil {
		t.Fatal(err)
	}
	if !applied["current-source\x00npc:a\x00player\x00trust"] || applied["old-source\x00npc:a\x00player\x00trust"] {
		t.Fatalf("applied=%+v", applied)
	}
}
