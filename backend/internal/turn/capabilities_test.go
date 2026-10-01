package turn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func TestModelStateUpdatesTypedValuesAndFailureConsequences(t *testing.T) {
	for _, value := range []wiaworld.StateValue{{Type: "integer", Integer: 80}, {Type: "boolean", Boolean: true}, {Type: "enum", Enum: "tired"}} {
		t.Run(value.Type, func(t *testing.T) {
			snapshot, output, host := mechanicsFixture()
			definition := &snapshot.Definition.StateDefinitions[0]
			definition.Type, definition.EnumValues, definition.UpdatePolicy = value.Type, []string{"ready", "tired"}, story.StateUpdatePolicy{Kind: "model"}
			current := output.States["player"][definition.ID]
			current.Value = wiaworld.StateValue{Type: value.Type}
			if value.Type == "enum" {
				current.Value.Enum = "ready"
			}
			output.States["player"][definition.ID] = current
			host.RelationshipEffects, host.ItemTransfers = nil, nil
			host.Outcomes[0].Status = "failed"
			host.StateEffects = []stateEffect{{EntityID: "player", StateID: definition.ID, Value: &value, ActionID: host.Outcomes[0].ActionID}}
			if err := applyMechanicEffects(snapshot, &output, host); err != nil {
				t.Fatal(err)
			}
			if got := output.States["player"][definition.ID].Value; got != value {
				t.Fatalf("value=%+v", got)
			}
		})
	}
}

func TestModelStatePreservesAtomicityAndDeclaredTypes(t *testing.T) {
	snapshot, output, host := mechanicsFixture()
	snapshot.Definition.StateDefinitions[0].UpdatePolicy = story.StateUpdatePolicy{Kind: "model"}
	host.StateEffects[1].Value = &wiaworld.StateValue{Type: "boolean", Boolean: true}
	host.StateEffects[1].Delta = 0
	if err := applyMechanicEffects(snapshot, &output, host); err == nil {
		t.Fatal("wrong type accepted")
	}
	if output.States["player"]["ritual_stability"].Value.Integer != 60 || output.Items["token-1"].HolderID != "player" {
		t.Fatal("partial changes escaped")
	}
	host.StateEffects = host.StateEffects[:1]
	host.Outcomes[0].Status = "not_executed"
	if err := applyMechanicEffects(snapshot, &output, host); err == nil {
		t.Fatal("unexecuted source changed state")
	}
}

func TestModelStateUsesResolvedCausalityInsteadOfExplicitTarget(t *testing.T) {
	snapshot, output, host := mechanicsFixture()
	snapshot.Definition.StateDefinitions[0].UpdatePolicy = story.StateUpdatePolicy{Kind: "model"}
	output.Events[0].TargetID = ""
	host.RelationshipEffects, host.ItemTransfers = nil, nil
	host.StateEffects = host.StateEffects[1:]
	if err := applyMechanicEffects(snapshot, &output, host); err != nil {
		t.Fatal(err)
	}
	if output.States["npc:warden"]["ritual_stability"].Value.Integer != 57 {
		t.Fatal("model consequence not applied")
	}
}

func TestItemsFollowModelPlacementAcrossCompoundActions(t *testing.T) {
	snapshot, output, host := mechanicsFixture()
	snapshot.Characters = append(snapshot.Characters, wiaworld.Character{EntityID: "npc:keeper", Name: "Keeper"})
	output.Events[0].ActorID, output.Events[0].TargetID = "npc:warden", ""
	host.StateEffects, host.RelationshipEffects = nil, nil
	host.ItemTransfers = []itemTransferEffect{
		{InstanceID: "token-1", FromHolderID: "player", ToHolderID: "npc:warden", ActionID: host.Outcomes[0].ActionID},
		{InstanceID: "token-1", FromHolderID: "npc:warden", ToHolderID: "npc:keeper", ActionID: host.Outcomes[0].ActionID},
	}
	if err := applyMechanicEffects(snapshot, &output, host); err != nil {
		t.Fatal(err)
	}
	if output.Items["token-1"].HolderID != "npc:keeper" || len(output.ItemTransfers) != 2 {
		t.Fatal("ordered NPC transfer lost")
	}
	snapshot, output, host = mechanicsFixture()
	item := output.Items["token-1"]
	item.HolderID, item.LocationID = "", "hall"
	output.Items[item.InstanceID] = item
	output.Positions = map[string]string{"player": "street", "npc:warden": "hall"}
	host.StateEffects, host.RelationshipEffects = nil, nil
	host.ItemTransfers = []itemTransferEffect{{InstanceID: "token-1", FromLocationID: "hall", ToHolderID: "player", ActionID: host.Outcomes[0].ActionID}}
	if err := applyMechanicEffects(snapshot, &output, host); err != nil {
		t.Fatal(err)
	}
	if output.Items[item.InstanceID].HolderID != "player" {
		t.Fatal("pickup before departure lost")
	}
}

func TestNPCActionRetainsItsChosenTarget(t *testing.T) {
	for _, target := range []string{"npc:keeper", ""} {
		var output Output
		appendNPCDecisionOutput(&output, wiaworld.Run{RunID: "run"}, wiaworld.Character{EntityID: "npc:warden"}, NPCDecision{ActionIntent: "Offer the token", ActionTargetID: target}, nil, "opening", 1, 1)
		if len(output.Events) != 1 || output.Events[0].TargetID != target {
			t.Fatalf("events=%+v", output.Events)
		}
	}
}

func TestNPCItemContextIncludesKnownOtherHolders(t *testing.T) {
	snapshot, _, _ := mechanicsFixture()
	snapshot.Definition.ItemDefinitions[0].Projection = "public"
	item := snapshot.Items["token-1"]
	item.SourceEvent = "prior:result"
	snapshot.Items[item.InstanceID] = item
	if !strings.Contains(MechanicsContext(snapshot, "npc:warden"), "token-1") {
		t.Fatal("known player item omitted")
	}
	snapshot.Perceptions["npc:warden"] = nil
	if strings.Contains(MechanicsContext(snapshot, "npc:warden"), "token-1") {
		t.Fatal("unknown item revealed")
	}
	snapshot.PerceivedSources = map[string]map[string]bool{"npc:warden": {"prior:result": true}}
	snapshot.Positions["player"] = "elsewhere"
	if strings.Contains(MechanicsContext(snapshot, "npc:warden"), "token-1") {
		t.Fatal("remote current item revealed")
	}
}

func TestRelationshipSubjectInterpretsExperienceAboutOtherPeople(t *testing.T) {
	snapshot, output, host := mechanicsFixture()
	snapshot.Sources["prior:result"] = SourceMetadata{ID: "prior:result", Actor: "world"}
	snapshot.Definition.RelationDefinitions[0].MaxChangePerTurn = 0
	decision := output.Decisions["npc:warden"]
	decision.RelationshipProposals[0].Delta = 20
	if err := validateRelationshipProposals(snapshot, "npc:warden", &decision); err != nil {
		t.Fatal(err)
	}
	output.Decisions["npc:warden"] = decision
	host.StateEffects, host.ItemTransfers = nil, nil
	host.RelationshipEffects[0].Delta = 20
	if err := applyMechanicEffects(snapshot, &output, host); err != nil {
		t.Fatal(err)
	}
	if output.Relationships[1].Value != 20 {
		t.Fatal("personal interpretation not applied")
	}
}

func TestOptionalCapabilitiesApplyIndependently(t *testing.T) {
	for _, name := range []string{"none", "state", "items", "relations"} {
		t.Run(name, func(t *testing.T) {
			snapshot, output, host := mechanicsFixture()
			snapshot.Definition.Capabilities = map[string]int{}
			if name != "none" {
				snapshot.Definition.Capabilities[name] = 1
			}
			if name != "state" {
				host.StateEffects = nil
			}
			if name != "items" {
				host.ItemTransfers = nil
			}
			if name != "relations" {
				host.RelationshipEffects = nil
			}
			if err := applyMechanicEffects(snapshot, &output, host); err != nil {
				t.Fatal(err)
			}
		})
	}
	snapshot, output, host := mechanicsFixture()
	snapshot.Definition.Capabilities = nil
	if err := applyMechanicEffects(snapshot, &output, host); err == nil {
		t.Fatal("disabled changes accepted")
	}
}

type offsceneCapabilityGenerator struct {
	t    *testing.T
	seen bool
}

func (g *offsceneCapabilityGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	response, err := (offsceneMovementGenerator{}).GenerateText(ctx, request)
	if err != nil {
		return response, err
	}
	var body map[string]any
	if err = json.Unmarshal([]byte(response.Text), &body); err != nil {
		return response, err
	}
	if strings.Contains(request.System, "重要 NPC") {
		body["relationship_proposals"] = []any{}
	} else {
		if strings.Contains(request.Input, "recipients只能取对应允许集合") || !strings.Contains(request.Input, "有效移动后的到达同场") {
			g.t.Error("spatial coordination contract excludes authorized arrival witnesses")
		}
		for _, needed := range []string{`"npc:clock":"clockshop"`, `"connections":["road"]`, "state_definitions", "strain", "token-1", "from_holder_id", "proposal_source_id", "relationship_proposals"} {
			if !strings.Contains(request.Input, needed) {
				g.t.Errorf("offscene material missing %s", needed)
			}
		}
		g.seen = true
		body["state_effects"], body["relationship_effects"], body["item_transfers"] = []any{}, []any{}, []any{}
	}
	encoded, err := json.Marshal(body)
	response.Text = string(encoded)
	return response, err
}

func TestOffsceneCallReceivesFullCapabilityContract(t *testing.T) {
	min, max := 0, 100
	snapshot := Snapshot{
		Summary:    wiaworld.WorldSummary{WorldID: "world", TurnSeq: 2, Clock: "第 1 日 12:00"},
		Definition: story.Definition{Capabilities: map[string]int{"spatial": 1, "state": 1, "items": 1, "relations": 1}, Locations: []story.Location{{ID: "office", Kind: "place"}, {ID: "clockshop", Kind: "place", Connections: []string{"road"}}, {ID: "road", Kind: "place", Connections: []string{"clinic"}}, {ID: "clinic", Kind: "place"}}, StateDefinitions: []story.StateDefinition{{ID: "strain", Type: "integer", Minimum: &min, Maximum: &max, UpdatePolicy: story.StateUpdatePolicy{Kind: "model"}}}, ItemDefinitions: []story.ItemDefinition{{ID: "token", Projection: "public"}}},
		Characters: []wiaworld.Character{{EntityID: "npc:clock", Name: "钟表匠"}}, Positions: map[string]string{"player": "office", "npc:clock": "clockshop"}, Sources: map[string]SourceMetadata{}, Perceptions: map[string][]wiaworld.Perception{},
		Items: map[string]wiaworld.ItemInstance{"token-1": {InstanceID: "token-1", DefinitionID: "token", HolderID: "npc:clock", SourceEvent: "opening"}},
	}
	output := Output{Clock: snapshot.Summary.Clock, SceneVersion: 1, Positions: clonePositions(snapshot.Positions), Items: cloneItems(snapshot.Items), SceneViews: []SceneView{{Recipient: "npc:clock", Content: "钟表铺", SourceIDs: []string{"opening"}}}}
	resolution := plotResolution{Status: "occurred", Content: "时机到了", SourceIDs: []string{"definition:plot:node"}, Projections: []plotProjection{{Recipient: "npc:clock", Content: "出发"}}, DecisionRequests: []string{"npc:clock"}}
	generator := &offsceneCapabilityGenerator{t: t}
	if _, err := New(&rosterHost{}, Deps{}).publishPlotResolution(context.Background(), generator, snapshot, wiaworld.Run{RunID: "run"}, "plot:node", resolution, &output); err != nil {
		t.Fatal(err)
	}
	if !generator.seen || output.Positions["npc:clock"] != "clinic" {
		t.Fatal("offscene flow not exercised")
	}
}

func TestNarrationReceivesFinalWorldCapabilityProjection(t *testing.T) {
	snapshot, output, _ := mechanicsFixture()
	snapshot.Definition.StateDefinitions[0].Projection = "public"
	snapshot.Definition.ItemDefinitions[0].Projection = "public"
	snapshot.Definition.Capabilities["spatial"] = 1
	snapshot.Definition.Locations = []story.Location{{ID: "hall", Kind: "place", Name: "Hall"}, {ID: "street", Kind: "place", Name: "Street"}}
	output.Positions = map[string]string{"player": "street", "npc:warden": "street"}
	state := output.States["npc:warden"]["ritual_stability"]
	state.Value.Integer, state.SourceEvent = 42, "later:public"
	output.States["npc:warden"]["ritual_stability"] = state
	item := output.Items["token-1"]
	item.HolderID, item.SourceEvent = "npc:warden", "later:public"
	output.Items[item.InstanceID] = item
	output.Perceptions = []wiaworld.Perception{
		{RecipientID: "player", SourceEventID: "later:public", Stage: 6},
		{RecipientID: "npc:warden", SourceEventID: "later:private", Stage: 6},
	}
	output.Clock = "第 1 日 10:00"
	if err := New(&rosterHost{}, Deps{}).resolveSceneResult(context.Background(), nil, &snapshot, wiaworld.Run{RunID: "run"}, hostResult{}, &output); err != nil {
		t.Fatal(err)
	}
	if snapshot.Positions["player"] != "street" || !snapshot.Characters[0].InScene {
		t.Fatal("narration retained the earlier roster or position")
	}
	projection := MechanicsContext(snapshot, "player")
	if !strings.Contains(projection, `"integer":42`) || !strings.Contains(projection, `"holder_id":"npc:warden"`) {
		t.Fatalf("narration did not receive final visible mechanics: %s", projection)
	}
	if perceivedSource(snapshot, "player", "later:private") {
		t.Fatal("private world-event projection reached the player")
	}
	count := 0
	for _, perception := range snapshot.Perceptions["player"] {
		if perception.SourceEventID == "later:public" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("final public source included %d times", count)
	}
}
