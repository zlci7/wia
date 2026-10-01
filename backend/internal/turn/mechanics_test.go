package turn

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func mechanicsFixture() (Snapshot, Output, hostResult) {
	minimum, maximum := 0, 100
	definition := story.Definition{
		Capabilities:        map[string]int{"spatial": 1, "state": 1, "relations": 1, "items": 1},
		StateDefinitions:    []story.StateDefinition{{ID: "ritual_stability", Name: "仪式稳定度", Type: "integer", Minimum: &minimum, Maximum: &maximum, Default: wiaworld.StateValue{Type: "integer", Integer: 60}, Scope: "all", Projection: "self", Knowledge: "owner", UpdatePolicy: story.StateUpdatePolicy{Kind: "bounded_proposal", MaxChangePerTurn: 10}}},
		RelationDefinitions: []story.RelationDefinition{{ID: "confidence", Name: "确信", Minimum: -100, Maximum: 100, Default: 0, MaxChangePerTurn: 8, Projection: "hidden"}},
		ItemDefinitions:     []story.ItemDefinition{{ID: "brass-token", Name: "黄铜凭证", Projection: "holder"}},
		Locations:           []story.Location{{ID: "hall", Kind: "place", Name: "大厅", Connections: []string{}}},
	}
	state := func(entity string) wiaworld.EntityState {
		return wiaworld.EntityState{EntityID: entity, StateID: "ritual_stability", Value: wiaworld.StateValue{Type: "integer", Integer: 60}, SourceEvent: "opening", Version: 1}
	}
	relationSource := "prior:result"
	snapshot := Snapshot{Definition: definition, Summary: wiaworld.WorldSummary{TurnSeq: 2}, Characters: []wiaworld.Character{{EntityID: "npc:warden", Name: "守门人", InScene: true}}, Positions: map[string]string{"player": "hall", "npc:warden": "hall"}, States: map[string]map[string]wiaworld.EntityState{"player": {"ritual_stability": state("player")}, "npc:warden": {"ritual_stability": state("npc:warden")}}, Relationships: []wiaworld.Relationship{{SubjectID: "player", TargetID: "npc:warden", RelationType: "confidence", SourceEvent: "opening", Version: 1}, {SubjectID: "npc:warden", TargetID: "player", RelationType: "confidence", SourceEvent: "opening", Version: 1}}, Items: map[string]wiaworld.ItemInstance{"token-1": {InstanceID: "token-1", DefinitionID: "brass-token", HolderID: "player", SourceEvent: "opening", Version: 1}}, Sources: map[string]SourceMetadata{relationSource: {ID: relationSource, Actor: "player", Kind: "player_action_result"}}, Perceptions: map[string][]wiaworld.Perception{"npc:warden": {{RecipientID: "npc:warden", SourceEventID: relationSource, SourceType: "action_succeeded"}}}}
	action := wiaworld.Event{EventID: "run:player-action", EventType: "player_action_intent", ActorID: "player", TargetID: "npc:warden", RunID: "run", Stage: 2}
	output := Output{Events: []wiaworld.Event{action}, States: cloneStates(snapshot.States), Relationships: append([]wiaworld.Relationship(nil), snapshot.Relationships...), Items: cloneItems(snapshot.Items), Decisions: map[string]NPCDecision{"npc:warden": {RelationshipProposals: []relationshipProposal{{TargetID: "player", RelationType: "confidence", Delta: 4, SourceID: relationSource}}}}}
	host := hostResult{Outcomes: []hostActionResult{{ActionID: action.EventID, Status: "succeeded", Content: "仪式完成，守门人接过凭证。", Recipients: []string{"player", "npc:warden"}}}, StateEffects: []stateEffect{{EntityID: "player", StateID: "ritual_stability", Delta: -6, ActionID: action.EventID}, {EntityID: "npc:warden", StateID: "ritual_stability", Delta: -3, ActionID: action.EventID}}, RelationshipEffects: []relationshipEffect{{SubjectID: "npc:warden", TargetID: "player", RelationType: "confidence", Delta: 4, ProposalSourceID: relationSource}}, ItemTransfers: []itemTransferEffect{{InstanceID: "token-1", FromHolderID: "player", ToHolderID: "npc:warden", ActionID: action.EventID}}}
	return snapshot, output, host
}

func TestMechanicEffectsUseAuthorDefinitionsAndApplyAsOneGroup(t *testing.T) {
	snapshot, output, host := mechanicsFixture()
	if err := applyMechanicEffects(snapshot, &output, host); err != nil {
		t.Fatal(err)
	}
	if got := output.States["player"]["ritual_stability"].Value.Integer; got != 54 {
		t.Fatalf("state=%d", got)
	}
	if got := output.States["npc:warden"]["ritual_stability"].Value.Integer; got != 57 {
		t.Fatalf("npc state=%d", got)
	}
	if got := output.Relationships[1].Value; got != 4 {
		t.Fatalf("relationship=%d", got)
	}
	if got := output.Items["token-1"].HolderID; got != "npc:warden" {
		t.Fatalf("holder=%q", got)
	}
	if len(output.StateChanges) != 2 || len(output.RelationshipChanges) != 1 || len(output.ItemTransfers) != 1 {
		t.Fatalf("changes=%d/%d/%d", len(output.StateChanges), len(output.RelationshipChanges), len(output.ItemTransfers))
	}
	snapshot.States, snapshot.Relationships, snapshot.Items = output.States, output.Relationships, output.Items
	context := MechanicsContext(snapshot, "npc:warden")
	for _, want := range []string{"ritual_stability", "57", "confidence", "token-1"} {
		if !strings.Contains(context, want) {
			t.Fatalf("next decision context missing %q: %s", want, context)
		}
	}
}

func TestMechanicEffectsRejectWholeGroupWhenOneCandidateIsInvalid(t *testing.T) {
	snapshot, output, host := mechanicsFixture()
	host.ItemTransfers[0].FromHolderID = "npc:warden"
	if err := applyMechanicEffects(snapshot, &output, host); err == nil {
		t.Fatal("invalid transfer was accepted")
	}
	if got := output.States["player"]["ritual_stability"].Value.Integer; got != 60 {
		t.Fatalf("partial state mutation=%d", got)
	}
	if got := output.Relationships[1].Value; got != 0 {
		t.Fatalf("partial relationship mutation=%d", got)
	}
	if got := output.Items["token-1"].HolderID; got != "player" {
		t.Fatalf("partial item mutation=%q", got)
	}
	if len(output.StateChanges)+len(output.RelationshipChanges)+len(output.ItemTransfers) != 0 {
		t.Fatal("partial changes escaped rejected group")
	}
}

func TestMechanicEffectsEnforceBudgetExperienceAndOwnership(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*hostResult)
		code   string
	}{
		{"state budget", func(h *hostResult) { h.StateEffects[0].Delta = -11 }, "state_budget_exceeded"},
		{"item ownership", func(h *hostResult) { h.ItemTransfers[0].FromHolderID = "npc:warden" }, "item_transfer_invalid"},
		{"minimum integer delta", func(h *hostResult) { h.StateEffects[0].Delta = math.MinInt }, "state_budget_exceeded"},
		{"proposal source", func(h *hostResult) { h.RelationshipEffects[0].ProposalSourceID = "unseen" }, "relationship_effect_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, output, host := mechanicsFixture()
			test.mutate(&host)
			err := applyMechanicEffects(snapshot, &output, host)
			if err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestMechanicEffectsEnforceCumulativeBudgetFailedSourcesAndReferences(t *testing.T) {
	t.Run("cumulative state budget", func(t *testing.T) {
		snapshot, output, host := mechanicsFixture()
		second := wiaworld.Event{EventID: "run:second", EventType: "player_action_intent", ActorID: "player", TargetID: "npc:warden", RunID: "run", Stage: 2}
		output.Events = append(output.Events, second)
		host.Outcomes = append(host.Outcomes, hostActionResult{ActionID: second.EventID, Status: "succeeded", Recipients: []string{"player", "npc:warden"}})
		host.StateEffects[0].Delta = -6
		host.StateEffects = append(host.StateEffects, stateEffect{EntityID: "player", StateID: "ritual_stability", Delta: -5, ActionID: second.EventID})
		if err := applyMechanicEffects(snapshot, &output, host); err == nil || !strings.Contains(err.Error(), "state_budget_exceeded") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("failed source", func(t *testing.T) {
		snapshot, output, host := mechanicsFixture()
		host.Outcomes[0].Status = "failed"
		if err := applyMechanicEffects(snapshot, &output, host); err == nil || !strings.Contains(err.Error(), "state_effect_invalid") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("relationship without personal experience", func(t *testing.T) {
		snapshot, output, host := mechanicsFixture()
		snapshot.Perceptions["npc:warden"] = nil
		if err := applyMechanicEffects(snapshot, &output, host); err == nil || !strings.Contains(err.Error(), "relationship_effect_invalid") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("unknown recipient", func(t *testing.T) {
		snapshot, output, host := mechanicsFixture()
		host.ItemTransfers[0].ToHolderID = "npc:unknown"
		if err := applyMechanicEffects(snapshot, &output, host); err == nil || !strings.Contains(err.Error(), "item_transfer_invalid") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestMechanicEffectsContinueOneTurnWorkingStateAcrossStages(t *testing.T) {
	t.Run("settled proposal combination is removed before coordination", func(t *testing.T) {
		snapshot, output, _ := mechanicsFixture()
		snapshot.AppliedRelationshipSources = map[string]bool{"prior:result\x00npc:warden\x00player\x00confidence": true}
		if options := relationshipProposalOptions(snapshot, "npc:warden"); len(options) != 0 {
			t.Fatalf("settled proposal remained available: %+v", options)
		}
		decision := output.Decisions["npc:warden"]
		if err := validateRelationshipProposals(snapshot, "npc:warden", &decision); err == nil {
			t.Fatal("settled proposal passed NPC decision validation")
		}
	})

	t.Run("relationship events keep unique IDs and proposals settle once", func(t *testing.T) {
		snapshot, output, host := mechanicsFixture()
		host.StateEffects = nil
		host.ItemTransfers = nil
		if err := applyMechanicEffects(snapshot, &output, host); err != nil {
			t.Fatal(err)
		}
		secondSource := "prior:result:2"
		snapshot.Sources[secondSource] = SourceMetadata{ID: secondSource, Actor: "player", Kind: "player_action_result"}
		snapshot.Perceptions["npc:warden"] = append(snapshot.Perceptions["npc:warden"], wiaworld.Perception{RecipientID: "npc:warden", SourceEventID: secondSource, SourceType: "action_succeeded"})
		output.Decisions["npc:warden"] = NPCDecision{RelationshipProposals: []relationshipProposal{{TargetID: "player", RelationType: "confidence", Delta: 3, SourceID: secondSource}}}
		second := hostResult{RelationshipEffects: []relationshipEffect{{SubjectID: "npc:warden", TargetID: "player", RelationType: "confidence", Delta: 3, ProposalSourceID: secondSource}}}
		if err := applyMechanicEffects(snapshot, &output, second); err != nil {
			t.Fatal(err)
		}
		if len(output.RelationshipChanges) != 2 || output.Relationships[1].Value != 7 {
			t.Fatalf("relationships=%+v changes=%+v", output.Relationships, output.RelationshipChanges)
		}
		if output.RelationshipChanges[0].SourceEventID != "run:relationship-effect:1" || output.RelationshipChanges[1].SourceEventID != "run:relationship-effect:2" {
			t.Fatalf("relationship event IDs=%q/%q", output.RelationshipChanges[0].SourceEventID, output.RelationshipChanges[1].SourceEventID)
		}
		if err := applyMechanicEffects(snapshot, &output, second); err == nil || !strings.Contains(err.Error(), "relationship_effect_invalid") {
			t.Fatalf("repeated proposal error=%v", err)
		}
		if len(output.RelationshipChanges) != 2 || output.Relationships[1].Value != 7 {
			t.Fatalf("rejected repeat changed working state: relationships=%+v changes=%+v", output.Relationships, output.RelationshipChanges)
		}
	})

	t.Run("state budget spans coordinator stages", func(t *testing.T) {
		snapshot, output, host := mechanicsFixture()
		host.StateEffects = host.StateEffects[:1]
		host.StateEffects[0].Delta = -6
		host.RelationshipEffects = nil
		host.ItemTransfers = nil
		if err := applyMechanicEffects(snapshot, &output, host); err != nil {
			t.Fatal(err)
		}
		secondAction := wiaworld.Event{EventID: "run:plot-action", EventType: "npc_action_intent", ActorID: "player", TargetID: "npc:warden", RunID: "run", Stage: 5}
		output.Events = append(output.Events, secondAction)
		second := hostResult{Outcomes: []hostActionResult{{ActionID: secondAction.EventID, Status: "succeeded", Recipients: []string{"player", "npc:warden"}}}, StateEffects: []stateEffect{{EntityID: "player", StateID: "ritual_stability", Delta: -5, ActionID: secondAction.EventID}}}
		if err := applyMechanicEffects(snapshot, &output, second); err == nil || !strings.Contains(err.Error(), "state_budget_exceeded") {
			t.Fatalf("second-stage budget error=%v", err)
		}
		if got := output.States["player"]["ritual_stability"].Value.Integer; got != 54 || len(output.StateChanges) != 1 {
			t.Fatalf("rejected second stage changed state=%d changes=%d", got, len(output.StateChanges))
		}
	})
}

func TestPlayerProjectionDoesNotLeakHiddenNPCStateOrRelationships(t *testing.T) {
	snapshot, _, _ := mechanicsFixture()
	snapshot.Definition.StateDefinitions[0].Projection = "hidden"
	if got := PlayerStateProjection(snapshot); len(got) != 0 {
		t.Fatalf("hidden states leaked: %+v", got)
	}
	if strings.Contains(MechanicsContext(snapshot, "player"), "confidence") {
		t.Fatal("private relationship leaked to player context")
	}
}

func TestStateProjectionAndKnowledgeCombinations(t *testing.T) {
	min, max := 0, 10
	definitions := []story.StateDefinition{
		{ID: "self_owned", Name: "自有", Type: "integer", Minimum: &min, Maximum: &max, Scope: "all", Projection: "self", Knowledge: "owner"},
		{ID: "public_known", Name: "公开", Type: "integer", Minimum: &min, Maximum: &max, Scope: "all", Projection: "public", Knowledge: "public"},
		{ID: "hidden_owned", Name: "私有", Type: "integer", Minimum: &min, Maximum: &max, Scope: "all", Projection: "hidden", Knowledge: "owner"},
		{ID: "host_fact", Name: "主持事实", Type: "integer", Minimum: &min, Maximum: &max, Scope: "all", Projection: "hidden", Knowledge: "host_only"},
		{ID: "hidden_public_knowledge", Name: "界面隐藏但可观察", Type: "integer", Minimum: &min, Maximum: &max, Scope: "all", Projection: "hidden", Knowledge: "public"},
	}
	values := func(entity string) map[string]wiaworld.EntityState {
		result := map[string]wiaworld.EntityState{}
		for _, definition := range definitions {
			result[definition.ID] = wiaworld.EntityState{EntityID: entity, StateID: definition.ID, Value: wiaworld.StateValue{Type: "integer", Integer: 1}, SourceEvent: "opening", Version: 1}
		}
		return result
	}
	snapshot := Snapshot{Definition: story.Definition{StateDefinitions: definitions}, Characters: []wiaworld.Character{{EntityID: "npc:a"}}, Positions: map[string]string{"player": "room", "npc:a": "room"}, States: map[string]map[string]wiaworld.EntityState{"player": values("player"), "npc:a": values("npc:a")}, Perceptions: map[string][]wiaworld.Perception{}}
	player := MechanicsContext(snapshot, "player")
	for _, want := range []string{"self_owned", "public_known"} {
		if !strings.Contains(player, want) {
			t.Fatalf("player missing %s: %s", want, player)
		}
	}
	for _, forbidden := range []string{"hidden_owned", "host_fact", "hidden_public_knowledge"} {
		if strings.Contains(player, forbidden) {
			t.Fatalf("player leaked %s: %s", forbidden, player)
		}
	}
	npc := MechanicsContext(snapshot, "npc:a")
	for _, want := range []string{"self_owned", "public_known", "hidden_owned"} {
		if !strings.Contains(npc, want) {
			t.Fatalf("npc missing %s: %s", want, npc)
		}
	}
	if strings.Contains(npc, "host_fact") {
		t.Fatalf("host-only state leaked: %s", npc)
	}
	if !strings.Contains(npc, "hidden_public_knowledge") {
		t.Fatalf("NPC knowledge incorrectly depended on player projection: %s", npc)
	}
	public := PlayerStateProjection(snapshot)
	foundNPCPublic := false
	for _, state := range public {
		if state.EntityID == "npc:a" && state.StateID == "public_known" {
			foundNPCPublic = true
		}
	}
	if !foundNPCPublic {
		t.Fatalf("co-located public NPC state missing: %+v", public)
	}
	beforePerceptions := append([]wiaworld.Perception(nil), snapshot.Perceptions["player"]...)
	_ = PlayerStateProjection(snapshot)
	_ = PlayerItemProjection(snapshot)
	if !reflect.DeepEqual(beforePerceptions, snapshot.Perceptions["player"]) {
		t.Fatal("reading player projections granted a perception")
	}
	snapshot.Positions["npc:a"] = "other-room"
	if moved := MechanicsContext(snapshot, "player"); strings.Contains(moved, `"entity_id":"npc:a"`) {
		t.Fatalf("remote NPC exact state remained visible: %s", moved)
	}
}

func TestPlayerProjectionRequiresSourceAndFiltersItemPolicies(t *testing.T) {
	minimum, maximum := 0, 10
	snapshot := Snapshot{
		Definition: story.Definition{
			StateDefinitions: []story.StateDefinition{{ID: "signal", Name: "信号", Type: "integer", Minimum: &minimum, Maximum: &maximum, Scope: "npc", Projection: "public", Knowledge: "public"}},
			ItemDefinitions:  []story.ItemDefinition{{ID: "owned", Name: "自有物", Projection: "holder"}, {ID: "public", Name: "公开物", Projection: "public"}, {ID: "secret", Name: "秘密物", Projection: "hidden"}},
		},
		Characters:  []wiaworld.Character{{EntityID: "npc:a"}},
		Positions:   map[string]string{"player": "room", "npc:a": "room"},
		States:      map[string]map[string]wiaworld.EntityState{"npc:a": {"signal": {EntityID: "npc:a", StateID: "signal", Value: wiaworld.StateValue{Type: "integer", Integer: 7}, SourceEvent: "unseen-result", Version: 1}}},
		Perceptions: map[string][]wiaworld.Perception{"player": {}},
		Items: map[string]wiaworld.ItemInstance{
			"owned":    {InstanceID: "owned", DefinitionID: "owned", HolderID: "player", SourceEvent: "opening", Version: 1},
			"public":   {InstanceID: "public", DefinitionID: "public", LocationID: "room", SourceEvent: "opening", Version: 1},
			"npc-held": {InstanceID: "npc-held", DefinitionID: "public", HolderID: "npc:a", SourceEvent: "unseen-result", Version: 1},
			"secret":   {InstanceID: "secret", DefinitionID: "secret", HolderID: "player", SourceEvent: "opening", Version: 1},
		},
	}
	if states := PlayerStateProjection(snapshot); len(states) != 0 {
		t.Fatalf("moving into range revealed an unperceived exact value: %+v", states)
	}
	items := PlayerItemProjection(snapshot)
	if len(items) != 2 || items[0].InstanceID != "owned" || items[1].InstanceID != "public" {
		t.Fatalf("item projection=%+v", items)
	}
	snapshot.PerceivedSources = map[string]map[string]bool{"player": {"unseen-result": true}}
	if states := PlayerStateProjection(snapshot); len(states) != 1 || states[0].StateID != "signal" {
		t.Fatalf("durably perceived state missing: %+v", states)
	}
	items = PlayerItemProjection(snapshot)
	if len(items) != 3 || items[0].InstanceID != "npc-held" {
		t.Fatalf("durably perceived item missing: %+v", items)
	}
}
