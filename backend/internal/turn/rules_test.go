package turn

import (
	"context"
	"path/filepath"
	"testing"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func ruleTestSnapshot() Snapshot {
	minimum, maximum := 0, 100
	return Snapshot{
		Summary: wiaworld.WorldSummary{TurnSeq: 4},
		Definition: story.Definition{
			StateDefinitions: []story.StateDefinition{{
				ID: "strain", Name: "负荷", Type: "integer", Minimum: &minimum, Maximum: &maximum,
				Default: wiaworld.StateValue{Type: "integer"}, Scope: "player", Projection: "self", Knowledge: "owner",
				UpdatePolicy: story.StateUpdatePolicy{Kind: "rule_only"},
			}},
			ActionRules: []story.ActionRule{{
				ID: "risk", Name: "风险行动", Guidance: "测试",
				Conditions: []plot.FactCondition{{Kind: "location_is", EntityID: "actor", LocationID: "room"}},
				Risk: &story.RiskRule{BaseTarget: 50, Minimum: 5, Maximum: 95, Modifiers: []story.RiskModifier{{
					Label: "低负荷", Amount: 10, Condition: plot.FactCondition{Kind: "state_at_most", EntityID: "actor", FactID: "strain", Value: 10},
				}}},
				SuccessText: "成功", FailureText: "失败",
				SuccessEffects: []story.RuleEffect{{Kind: "state_delta", EntityID: "actor", StateID: "strain", Delta: 2}},
				FailureEffects: []story.RuleEffect{{Kind: "state_delta", EntityID: "actor", StateID: "strain", Delta: 9}},
			}},
		},
		Positions: map[string]string{"player": "room"},
		States: map[string]map[string]wiaworld.EntityState{
			"player": {"strain": {EntityID: "player", StateID: "strain", Value: wiaworld.StateValue{Type: "integer", Integer: 3}, SourceEvent: "opening", Version: 1}},
		},
		Relationships: []wiaworld.Relationship{}, Items: map[string]wiaworld.ItemInstance{},
	}
}

func TestPreparedRiskReusesRollForStableInput(t *testing.T) {
	store, err := storage.OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := ruleTestSnapshot()
	first, err := prepareActionResolution(context.Background(), store, snapshot, wiaworld.Run{RunID: "run:1", InputID: "input:1"}, "risk")
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepareActionResolution(context.Background(), store, snapshot, wiaworld.Run{RunID: "run:2", InputID: "input:1"}, "risk")
	if err != nil {
		t.Fatal(err)
	}
	if first.Roll < 1 || first.Roll > 100 || first.Target != 60 || second.Roll != first.Roll || !second.Reused || second.ActionID == first.ActionID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestProgramRuleFailureAppliesDeclaredConsequenceOnlyOnce(t *testing.T) {
	snapshot := ruleTestSnapshot()
	output := Output{SceneVersion: 1, States: cloneStates(snapshot.States), Relationships: []wiaworld.Relationship{}, Items: map[string]wiaworld.ItemInstance{}, Events: []wiaworld.Event{{EventID: "run:player-action", EventType: "player_action_intent", ActorID: "player", RunID: "run", Stage: 2}}, ActionResolution: &ActionResolution{RuleID: "risk", RuleName: "风险行动", ActionID: "run:player-action", Roll: 99, Target: 60, Status: "failed", Summary: "失败", Effects: []story.RuleEffect{{Kind: "state_delta", EntityID: "actor", StateID: "strain", Delta: 9}}}}
	host := hostResult{Outcomes: []hostActionResult{{ActionID: "run:player-action", Status: "failed", Content: "失败表现", Projections: outcomeProjectionFixture("失败表现", []string{"player"}), Recipients: []string{"player"}}}}
	if _, err := appendHostOutcomes(&output, wiaworld.Run{RunID: "run"}, nil, nil, host.Outcomes); err != nil {
		t.Fatal(err)
	}
	event, err := applyActionResolution(snapshot, &output, host)
	if err != nil {
		t.Fatal(err)
	}
	if event.SourceType != "rule:risk:failed" || output.States["player"]["strain"].Value.Integer != 12 || len(output.StateChanges) != 1 {
		t.Fatalf("event=%+v state=%+v changes=%d", event, output.States, len(output.StateChanges))
	}
	if err = validateActionResolutionOutcome(output, []hostActionResult{{ActionID: "run:player-action", Status: "succeeded"}}); err == nil {
		t.Fatal("model override of program result was accepted")
	}
}

func TestRuleEffectsRetainRepeatedChangesFromOneResult(t *testing.T) {
	snapshot := ruleTestSnapshot()
	output := Output{States: cloneStates(snapshot.States)}
	effects := []story.RuleEffect{{Kind: "state_delta", EntityID: "actor", StateID: "strain", Delta: 2}, {Kind: "state_delta", EntityID: "actor", StateID: "strain", Delta: 3}}
	if err := applyRuleStateEffects(snapshot, &output, "player", "risk", "action", wiaworld.Event{EventID: "result"}, effects); err != nil {
		t.Fatal(err)
	}
	if output.States["player"]["strain"].Value.Integer != 8 || len(output.StateChanges) != 2 {
		t.Fatalf("output=%+v", output)
	}
	if output.StateChanges[0].Order == output.StateChanges[1].Order || output.StateChanges[1].Before.Value.Integer != 5 {
		t.Fatal("ordered history lost")
	}
}

func TestProgramRuleEffectSaturatesAtDeclaredBounds(t *testing.T) {
	snapshot := ruleTestSnapshot()
	current := snapshot.States["player"]["strain"]
	current.Value.Integer = 96
	snapshot.States["player"]["strain"] = current
	output := Output{SceneVersion: 1, States: cloneStates(snapshot.States), Relationships: []wiaworld.Relationship{}, Items: map[string]wiaworld.ItemInstance{}, Events: []wiaworld.Event{{EventID: "run:player-action", EventType: "player_action_intent", ActorID: "player", RunID: "run", Stage: 2}}, ActionResolution: &ActionResolution{RuleID: "risk", RuleName: "风险行动", ActionID: "run:player-action", Roll: 99, Target: 60, Status: "failed", Summary: "失败", Effects: []story.RuleEffect{{Kind: "state_delta", EntityID: "actor", StateID: "strain", Delta: 9}}}}
	host := hostResult{Outcomes: []hostActionResult{{ActionID: "run:player-action", Status: "failed", Content: "失败表现", Projections: outcomeProjectionFixture("失败表现", []string{"player"}), Recipients: []string{"player"}}}}
	if _, err := appendHostOutcomes(&output, wiaworld.Run{RunID: "run"}, nil, nil, host.Outcomes); err != nil {
		t.Fatal(err)
	}
	if _, err := applyActionResolution(snapshot, &output, host); err != nil {
		t.Fatal(err)
	}
	if got := output.States["player"]["strain"].Value.Integer; got != 100 {
		t.Fatalf("saturated state=%d, want 100", got)
	}
}

func TestStructuredFactsReadWorkingState(t *testing.T) {
	snapshot := ruleTestSnapshot()
	snapshot.Relationships = []wiaworld.Relationship{{SubjectID: "player", TargetID: "npc:a", RelationType: "trust", Value: 4}}
	snapshot.Items = map[string]wiaworld.ItemInstance{"mirror": {InstanceID: "mirror", HolderID: "player"}}
	snapshot.Events = []wiaworld.Event{{EventID: "rule", SourceType: "rule:accept:succeeded"}}
	output := Output{Positions: snapshot.Positions, States: snapshot.States, Relationships: snapshot.Relationships, Items: snapshot.Items}
	conditions := []plot.FactCondition{{Kind: "location_is", EntityID: "player", LocationID: "room"}, {Kind: "state_at_most", EntityID: "player", FactID: "strain", Value: 3}, {Kind: "relation_at_least", EntityID: "player", TargetID: "npc:a", FactID: "trust", Value: 4}, {Kind: "item_held", EntityID: "player", FactID: "mirror"}, {Kind: "rule_result", FactID: "accept", Status: "succeeded"}}
	if met, evidence := evaluateFactConditions(snapshot, output, conditions, ""); !met || len(evidence) != len(conditions) {
		t.Fatalf("met=%t evidence=%v", met, evidence)
	}
	output.States = cloneStates(snapshot.States)
	changed := output.States["player"]["strain"]
	changed.Value.Integer = 8
	output.States["player"]["strain"] = changed
	if met, _ := evaluateFactConditions(snapshot, output, conditions, ""); met {
		t.Fatal("condition ignored current working state")
	}
}

func TestStructuredFactsReadDurableRuleResultsOutsideRecentEvents(t *testing.T) {
	snapshot := ruleTestSnapshot()
	snapshot.Events = nil
	snapshot.RuleResults = map[string]map[string]string{"accept": {"succeeded": "old-rule-event"}}
	output := Output{Positions: snapshot.Positions, States: snapshot.States, Relationships: snapshot.Relationships, Items: snapshot.Items}
	if met, _ := evaluateFactCondition(snapshot, output, plot.FactCondition{Kind: "rule_result", FactID: "accept", Status: "succeeded"}, ""); !met {
		t.Fatal("durable rule result was lost outside the recent event window")
	}
	if met, _ := evaluateFactCondition(snapshot, output, plot.FactCondition{Kind: "rule_result_absent", FactID: "accept", Status: "succeeded"}, ""); met {
		t.Fatal("durable rule result was reported absent")
	}
}
