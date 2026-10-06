package turn

import (
	"testing"

	wiaworld "gameagent/backend/internal/world"
)

func TestResolvedEffectsConsumeExplicitGrantsWithoutNPCDecisions(t *testing.T) {
	snapshot, output, host := mechanicsFixture()
	effects := host.mechanics(snapshot, output)
	output.Decisions = nil
	output.Events[0].Stage = 12
	if err := applyMechanicEffects(snapshot, &output, effects); err != nil {
		t.Fatal(err)
	}
	if len(output.RelationshipChanges) != 1 || len(output.StateChanges) != 2 || len(output.ItemTransfers) != 1 {
		t.Fatal("resolved grants lost their effects")
	}

	snapshot, output, host = mechanicsFixture()
	effects = host.mechanics(snapshot, output)
	effects.relationshipAuthority = nil
	if err := applyMechanicEffects(snapshot, &output, effects); err == nil {
		t.Fatal("relationship applied without the subject's source grant")
	}
	if output.States["player"]["ritual_stability"].Value.Integer != 60 || output.Items["token-1"].HolderID != "player" {
		t.Fatal("rejected group leaked partial effects")
	}
}

func TestResolvedMovementUsesBoundResultAtAnyStage(t *testing.T) {
	snapshot := spatialFixture()
	action := wiaworld.Event{EventID: "scene:attempt:7", ActorID: "player", Stage: 12, EventType: "player_action_intent"}
	sources := map[string]mechanicSource{action.EventID: {action: action, status: "succeeded", resultID: "scene:attempt:7:result:1"}}
	positions, changes, _, err := applyMovements(snapshot, []movementResult{{EntityID: "player", From: "office", To: "street", Route: []string{"office", "street"}, ActionID: action.EventID}}, sources)
	if err != nil || positions["player"] != "street" || changes[0].SourceEventID != "scene:attempt:7:result:1" {
		t.Fatalf("resolved movement: %v %v %v", positions, changes, err)
	}
	sources[action.EventID] = mechanicSource{action: action, status: "failed", resultID: "scene:attempt:7:result:1"}
	if _, _, _, err := applyMovements(snapshot, []movementResult{{EntityID: "player", From: "office", To: "street", Route: []string{"office", "street"}, ActionID: action.EventID}}, sources); err == nil {
		t.Fatal("failed resolved action moved player")
	}
}
