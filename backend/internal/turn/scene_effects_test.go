package turn

import (
	"slices"
	"testing"

	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func scenePlayerAction(id, content string) sceneBeat {
	return sceneBeat{LocalID: id, Kind: "action_result", ActorID: "player", Basis: []string{"input:0"}, Content: content, Recipients: []string{"player"}, Bystanders: []string{}, Projections: outcomeProjectionFixture(content, []string{"player"}), Status: sceneString("succeeded"), Attempt: &sceneAttempt{Content: content, InputFragmentIndex: sceneIndex(0)}}
}

func TestSceneCashAndItemEffectsAreAtomicAndUseWholeTurnBudget(t *testing.T) {
	s := sceneContextFixture()
	s.Definition.Capabilities["state"] = 1
	low, high := 0, 100
	s.Definition.StateDefinitions = []story.StateDefinition{{ID: "cash", Type: "integer", Scope: "player", Minimum: &low, Maximum: &high, UpdatePolicy: story.StateUpdatePolicy{Kind: "bounded_proposal", MaxChangePerTurn: 8}}}
	s.States = map[string]map[string]wiaworld.EntityState{"player": {"cash": {EntityID: "player", StateID: "cash", Value: wiaworld.StateValue{Type: "integer", Integer: 5}, Version: 1, SourceEvent: "opening"}}}
	run := wiaworld.Run{RunID: "purchase", Input: "我购买工具并给办事员一点报酬。"}
	a := scenePlayerAction("b1", "购买工具。")
	a.Effects.StateEffects = []sceneStateEffect{{EntityID: "player", StateID: "cash", Delta: -6}}
	a.Effects.ItemTransfers = []sceneItemTransfer{{InstanceID: "unique-tool", FromLocationID: "workplace", ToHolderID: "player"}}
	if out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, a)); err == nil || len(out.ItemTransfers) != 0 || s.States["player"]["cash"].Value.Integer != 5 || s.Items["unique-tool"].LocationID != "workplace" {
		t.Fatal("insufficient cash produced a partial purchase", err)
	}
	a.Effects.StateEffects[0].Delta = -3
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, a))
	if err != nil || out.States["player"]["cash"].Value.Integer != 2 || out.Items["unique-tool"].HolderID != "player" {
		t.Fatal("valid purchase failed", err)
	}
	b := scenePlayerAction("b2", "返还款项。")
	b.Effects.StateEffects = []sceneStateEffect{{EntityID: "player", StateID: "cash", Delta: 6}}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, a, b)); err == nil {
		t.Fatal("per-node state budget replaced the whole-turn budget")
	}
}

func TestSceneRelationshipDependenciesAndCumulativeBudget(t *testing.T) {
	s := sceneContextFixture()
	s.Definition.Capabilities["relations"] = 1
	s.Definition.RelationDefinitions = []story.RelationDefinition{{ID: "trust", Minimum: -100, Maximum: 100, MaxChangePerTurn: 5}}
	s.Relationships = []wiaworld.Relationship{{SubjectID: "npc:a", TargetID: "player", RelationType: "trust", Value: 0, Version: 1, SourceEvent: "opening"}}
	run := wiaworld.Run{RunID: "relation", Input: "我愿意帮助核对。"}
	a := sceneDialogue("b1", "npc:a", "谢谢。", "input:0")
	a.Effects.RelationshipEffects = []sceneRelation{{SubjectID: "npc:a", TargetID: "player", RelationType: "trust", Delta: 3, Basis: []string{"input:0"}}}
	b := sceneDialogue("b2", "npc:a", "我们可以合作。", "beat:b1")
	b.Effects.RelationshipEffects = []sceneRelation{{SubjectID: "npc:a", TargetID: "player", RelationType: "trust", Delta: 3, Basis: []string{"beat:b1", scenePersonalID("npc:a", "same-record")}}}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, a, b)); err == nil || s.Relationships[0].Value != 0 {
		t.Fatal("whole-turn relation budget escaped")
	}
	b.Effects.RelationshipEffects[0].Delta = 2
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, a, b))
	if err != nil || out.Relationships[0].Value != 5 {
		t.Fatal("valid relation effects failed", err)
	}
	last := out.RelationshipChanges[1].SourceEventID
	event, ok := EventByID(out.Events, last)
	if !ok || len(event.BasisEventIDs) != 2 || !slices.Contains(event.BasisEventIDs, "npc:a:heard") {
		t.Fatalf("complete relation dependency lost: %+v", event)
	}
}
