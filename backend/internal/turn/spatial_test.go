package turn

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func spatialFixture() Snapshot {
	return Snapshot{
		Definition: story.Definition{
			Capabilities: map[string]int{"spatial": 1},
			Locations: []story.Location{
				{ID: "office", Kind: "place", Connections: []string{"street"}, Public: true},
				{ID: "street", Kind: "place", Connections: []string{"office", "clinic"}, Public: true},
				{ID: "clinic", Kind: "place", Connections: []string{"street"}, Public: true},
			},
			BystanderRefs: []story.Bystander{{BystanderID: "bystander:clerk", Name: "书记员"}, {BystanderID: "bystander:patient", Name: "候诊者"}},
		},
		SceneLocation:   "office",
		Positions:       map[string]string{"player": "office", "npc:reporter": "office", "npc:watcher": "clinic", "bystander:clerk": "office", "bystander:patient": "clinic"},
		PositionSources: map[string]string{"player": "opening", "npc:reporter": "opening", "npc:watcher": "opening", "bystander:clerk": "opening", "bystander:patient": "opening"},
		Characters:      []wiaworld.Character{{EntityID: "npc:reporter", InScene: true}, {EntityID: "npc:watcher"}},
		Bystanders:      []string{"书记员"},
		BystanderRefs:   []story.Bystander{{BystanderID: "bystander:clerk", Name: "书记员"}},
	}
}

func TestMovementRequiresActorSceneUpdate(t *testing.T) {
	host := hostResult{
		Movements:    []movementResult{{EntityID: "player", ActionID: "move-player"}},
		SceneUpdates: []sceneUpdate{{Recipients: []string{"player"}, SourceIDs: []string{"other-action"}}},
	}
	if err := validateMovementSceneUpdates(host); err == nil || !strings.Contains(err.Error(), "movement_scene_update_missing") {
		t.Fatalf("error = %v", err)
	}
	host.SceneUpdates[0].SourceIDs = []string{"move-player"}
	if err := validateMovementSceneUpdates(host); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityManifestRejectsUnsupportedAndMismatchedVersions(t *testing.T) {
	if err := validateCapabilityManifest(context.Background(), nil, story.Definition{Capabilities: map[string]int{"spatial": 2}}); err == nil || !strings.Contains(err.Error(), "unsupported frozen capability manifest") {
		t.Fatalf("unsupported capability error = %v", err)
	}
	store, err := storage.OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.InTx(context.Background(), func(tx *storage.WorldTx) error {
		return tx.SetMeta(context.Background(), "capability_manifest", `{"spatial":2}`)
	}); err != nil {
		t.Fatal(err)
	}
	if err = validateCapabilityManifest(context.Background(), store, story.Definition{Capabilities: map[string]int{"spatial": 1}}); err == nil || !strings.Contains(err.Error(), "does not match frozen definition") {
		t.Fatalf("mismatched capability error = %v", err)
	}
}

func TestApplyMovementsUsesActionOwnershipAndDirectedRoutes(t *testing.T) {
	snapshot := spatialFixture()
	events := []wiaworld.Event{
		{EventID: "move-player", EventType: "player_action_intent", ActorID: "player"},
		{EventID: "move-reporter", EventType: "npc_action_intent", ActorID: "npc:reporter"},
	}
	host := hostResult{
		Outcomes: []hostActionResult{{ActionID: "move-player", Status: "succeeded"}, {ActionID: "move-reporter", Status: "partial"}},
		Movements: []movementResult{
			{EntityID: "player", From: "office", To: "clinic", Route: []string{"office", "street", "clinic"}, ActionID: "move-player"},
			{EntityID: "npc:reporter", From: "office", To: "street", Route: []string{"office", "street"}, ActionID: "move-reporter"},
		},
	}
	positions, changes, scene, err := applyMovements(snapshot, events, host)
	if err != nil {
		t.Fatal(err)
	}
	if positions["player"] != "clinic" || positions["npc:reporter"] != "street" || len(changes) != 2 {
		t.Fatalf("movement state = %+v, changes = %+v", positions, changes)
	}
	if len(scene) != 1 || scene[0] != "npc:watcher" {
		t.Fatalf("scene roster must be derived from positions: %v", scene)
	}
}

func TestApplyMovementsRejectsClaimedFollowingAndIllegalRoutes(t *testing.T) {
	snapshot := spatialFixture()
	events := []wiaworld.Event{{EventID: "move-player", EventType: "player_action_intent", ActorID: "player"}}
	for _, tc := range []struct {
		name     string
		movement movementResult
		want     string
	}{
		{"player cannot move npc", movementResult{EntityID: "npc:reporter", From: "office", To: "street", Route: []string{"office", "street"}, ActionID: "move-player"}, "movement_entity_invalid"},
		{"route cannot skip edge", movementResult{EntityID: "player", From: "office", To: "clinic", Route: []string{"office", "clinic"}, ActionID: "move-player"}, "movement_route_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := applyMovements(snapshot, events, hostResult{Outcomes: []hostActionResult{{ActionID: "move-player", Status: "succeeded"}}, Movements: []movementResult{tc.movement}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestApplySpatialOutputChangesTheNextStageView(t *testing.T) {
	snapshot := spatialFixture()
	if err := applySpatialOutput(&snapshot, Output{SceneLocation: "clinic", Positions: map[string]string{"player": "clinic", "npc:reporter": "office", "npc:watcher": "clinic", "bystander:clerk": "office", "bystander:patient": "clinic"}}); err != nil {
		t.Fatal(err)
	}
	if snapshot.Characters[0].InScene || !snapshot.Characters[1].InScene {
		t.Fatalf("next stage roster was not derived from new positions: %+v", snapshot.Characters)
	}
	if len(snapshot.Bystanders) != 1 || snapshot.BystanderRefs[0].BystanderID != "bystander:patient" {
		t.Fatalf("next-stage bystanders were not derived from destination: %v %+v", snapshot.Bystanders, snapshot.BystanderRefs)
	}
}

func TestSpatialOutcomeAudienceUsesEachActionsLocation(t *testing.T) {
	snapshot := spatialFixture()
	events := []wiaworld.Event{
		{EventID: "move-player", EventType: "player_action_intent", ActorID: "player"},
		{EventID: "reporter-action", EventType: "npc_action_intent", ActorID: "npc:reporter"},
	}
	finalPositions := clonePositions(snapshot.Positions)
	finalPositions["player"] = "clinic"
	valid := hostResult{
		Outcomes: []hostActionResult{
			{ActionID: "move-player", Status: "succeeded", Recipients: []string{"npc:reporter", "npc:watcher"}, Bystanders: []string{"bystander:clerk", "bystander:patient"}},
			{ActionID: "reporter-action", Status: "succeeded", Recipients: []string{"npc:reporter"}},
		},
		Movements: []movementResult{{EntityID: "player", From: "office", To: "clinic", Route: []string{"office", "street", "clinic"}, ActionID: "move-player"}},
	}
	if err := validateSpatialOutcomeAudiences(snapshot, finalPositions, events, valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Outcomes = append([]hostActionResult(nil), valid.Outcomes...)
	invalid.Outcomes[1].Recipients = []string{"npc:watcher"}
	if err := validateSpatialOutcomeAudiences(snapshot, finalPositions, events, invalid); err == nil || !strings.Contains(err.Error(), "action_outcome_spatial_recipient") {
		t.Fatalf("off-location result recipient was accepted: %v", err)
	}
}
