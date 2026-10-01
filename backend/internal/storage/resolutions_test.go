package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestActionResolutionPreparationIsReusableAndSettlesAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	proposed := ActionResolutionRecord{InputID: "input:1", RuleID: "risk", Roll: 37, Target: 55, ModifiersJSON: `[{"label":"ready","amount":5}]`, CreatedAt: "now"}
	first, reused, err := store.PrepareActionResolution(ctx, proposed)
	if err != nil || reused || first.Roll != 37 {
		t.Fatalf("first=%+v reused=%t err=%v", first, reused, err)
	}
	proposed.Roll = 99
	second, reused, err := store.PrepareActionResolution(ctx, proposed)
	if err != nil || !reused || second.Roll != 37 {
		t.Fatalf("second=%+v reused=%t err=%v", second, reused, err)
	}
	if _, _, err = store.PrepareActionResolution(ctx, ActionResolutionRecord{InputID: "input:1", RuleID: "risk", Roll: 37, Target: 45, ModifiersJSON: proposed.ModifiersJSON}); err == nil {
		t.Fatal("changed frozen inputs were accepted")
	}
	if err = store.InTx(ctx, func(tx *WorldTx) error { return tx.SettleActionResolution(ctx, "input:1", "risk", "failed", "event:1") }); err != nil {
		t.Fatal(err)
	}
	record, found, err := store.ReadActionResolution(ctx, "input:1", "risk")
	if err != nil || !found || record.SettledEventID != "event:1" || record.Status != "failed" {
		t.Fatalf("record=%+v found=%t err=%v", record, found, err)
	}
	settled, err := store.LoadSettledActionResults(ctx)
	if err != nil || len(settled) != 1 || settled[0].RuleID != "risk" || settled[0].Status != "failed" || settled[0].EventID != "event:1" {
		t.Fatalf("settled=%+v err=%v", settled, err)
	}
}

func TestOpenWorldDBMigratesSettledActionResolutionStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "world.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`
		CREATE TABLE action_resolutions (
			input_id TEXT NOT NULL, rule_id TEXT NOT NULL, roll INTEGER NOT NULL,
			target INTEGER NOT NULL, modifiers_json TEXT NOT NULL,
			settled_event_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
			PRIMARY KEY(input_id,rule_id)
		);
		CREATE TABLE events (
			seq INTEGER PRIMARY KEY, event_id TEXT NOT NULL UNIQUE, event_type TEXT NOT NULL,
			actor_id TEXT NOT NULL, target_id TEXT NOT NULL, content TEXT NOT NULL,
			run_id TEXT NOT NULL, stage INTEGER NOT NULL, scene_version INTEGER NOT NULL,
			source_type TEXT NOT NULL, created_at TEXT NOT NULL
		);
		INSERT INTO events(seq,event_id,event_type,actor_id,target_id,content,run_id,stage,scene_version,source_type,created_at)
		VALUES(1,'event:legacy','action_result','player','','failed','run:legacy',1,1,'rule:risk:failed','now');
		INSERT INTO action_resolutions(input_id,rule_id,roll,target,modifiers_json,settled_event_id,created_at)
		VALUES('input:legacy','risk',90,55,'[]','event:legacy','now');
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenWorldDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settled, err := store.LoadSettledActionResults(context.Background())
	if err != nil || len(settled) != 1 || settled[0].RuleID != "risk" || settled[0].Status != "failed" {
		t.Fatalf("settled=%+v err=%v", settled, err)
	}
}
