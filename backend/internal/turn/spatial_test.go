package turn

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
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

func TestAddressedSpeechRetainsItsMovementAttempt(t *testing.T) {
	snapshot := spatialFixture()
	snapshot.Summary.Clock = "第 1 日 09:00"
	snapshot.SceneViews = []SceneView{{Recipient: "player", Content: "office"}, {Recipient: "npc:reporter", Content: "office"}, {Recipient: "npc:watcher", Content: "clinic"}}
	run := wiaworld.Run{RunID: "run", Input: "我拒绝委托，然后告别记者，走到街上。"}
	intent := TurnIntent{IntentType: "speak", AddresseeID: "npc:reporter", Visibility: "public"}
	output := Output{SceneVersion: 1, Positions: clonePositions(snapshot.Positions)}
	notePlayerAction(&output, run, intent)
	host := hostResult{TimeMinutes: 5, Scene: "street", Outcomes: []hostActionResult{{ActionID: "run:player-action", Status: "succeeded", Content: "玩家拒绝委托，离开办公室抵达街上。", Projections: outcomeProjectionFixture("玩家拒绝委托，离开办公室抵达街上。", []string{"player", "npc:reporter"}), Recipients: []string{"player", "npc:reporter"}}}, Movements: []movementResult{{EntityID: "player", From: "office", To: "street", Route: []string{"office", "street"}, ActionID: "run:player-action"}}, SceneUpdates: []sceneUpdate{{Content: "street", SourceIDs: []string{"run:player-action"}, Recipients: []string{"player"}}}}
	resolved, err := prepareCoordination(snapshot, run, intent, output, host)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.output.Positions["player"] != "street" || len(resolved.output.SceneCharacters) != 0 || len(resolved.output.PositionChanges) != 1 {
		t.Fatalf("spoken input lost its physical consequence: %+v", resolved.output)
	}
	if resolved.output.Events[0].Content != run.Input {
		t.Fatal("complete input was not retained")
	}
}

func TestPrivatePlayerResolutionPreservesRecipientBoundary(t *testing.T) {
	snapshot := spatialFixture()
	run := wiaworld.Run{RunID: "run", Input: "我私下告诉记者一个秘密。"}
	intent := TurnIntent{IntentType: "speak", AddresseeID: "npc:reporter", Visibility: "private"}
	output := Output{SceneVersion: 1}
	notePlayerAction(&output, run, intent)
	host := hostResult{Outcomes: []hostActionResult{{ActionID: "run:player-action", Recipients: []string{"npc:watcher"}}}}
	if _, err := prepareCoordination(snapshot, run, intent, output, host); err == nil || !strings.Contains(err.Error(), "private_player_outcome_recipient") {
		t.Fatalf("private resolution was allowed outside its recipient scope: %v", err)
	}
}

func TestWorldEventUsesCurrentPositionsRatherThanRetainedSceneText(t *testing.T) {
	snapshot := spatialFixture()
	snapshot.Plot = &plot.Definition{Revision: "revision"}
	snapshot.SceneViews = []SceneView{{Recipient: "npc:reporter", Content: "旧情境：记者还在事务所。"}}
	output := Output{Positions: clonePositions(snapshot.Positions), SceneViews: snapshot.SceneViews}
	output.Positions["npc:reporter"] = "clinic"
	context := worldEventSpatialContext(snapshot, output)
	if !strings.Contains(context, wire.MarshalJSON(output.Positions)) || !strings.Contains(context, "不能为了让其目击而将其移到事件地点") {
		t.Fatal("world event omitted the current position and decision contract")
	}
	material := composePlot(snapshot, wiaworld.Run{}, plot.Node{}, &output)
	if !strings.Contains(material.Required, context) {
		t.Fatal("authored event omitted the shared spatial contract")
	}
	snapshot.Definition.Capabilities = nil
	if worldEventSpatialContext(snapshot, output) != "" {
		t.Fatal("legacy event acquired spatial semantics")
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
	positions, changes, scene, err := applyMovements(snapshot, host.Movements, mechanicSources(events, host.Outcomes))
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
			_, _, _, err := applyMovements(snapshot, []movementResult{tc.movement}, mechanicSources(events, []hostActionResult{{ActionID: "move-player", Status: "succeeded"}}))
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
