package turn

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func creationResourceSession(t *testing.T) *CreationSession {
	t.Helper()
	def := creationTestDefinition()
	zero, hundred := 0, 100
	def.Capabilities = map[string]int{"state": 1, "items": 1}
	def.StateDefinitions = []story.StateDefinition{
		{ID: "coins", Name: "港币", Type: "integer", Minimum: &zero, Maximum: &hundred, Projection: "self", Knowledge: "owner", UpdatePolicy: story.StateUpdatePolicy{Kind: "model"}, Currency: &wiaworld.Currency{Name: "港币", Denominations: []wiaworld.Denomination{{Name: "枚", Units: 1}}}},
		{ID: "strain", Name: "负荷", Type: "integer", Minimum: &zero, Maximum: &hundred, Projection: "self", Knowledge: "owner", UpdatePolicy: story.StateUpdatePolicy{Kind: "bounded_proposal", MaxChangePerTurn: 3}},
		{ID: "skill", Name: "航海", Type: "boolean", Projection: "self", Knowledge: "owner", UpdatePolicy: story.StateUpdatePolicy{Kind: "readonly"}},
		{ID: "secret", Name: "隐秘状态", Type: "boolean", Projection: "hidden", Knowledge: "host_only", UpdatePolicy: story.StateUpdatePolicy{Kind: "model"}},
	}
	def.InitialStates = map[string]map[string]wiaworld.StateValue{"player": {
		"coins": {Type: "integer", Integer: 10}, "strain": {Type: "integer", Integer: 1}, "skill": {Type: "boolean", Boolean: true}, "secret": {Type: "boolean"},
	}}
	def.ItemDefinitions = []story.ItemDefinition{{ID: "tool", Name: "扳手", Projection: "holder"}, {ID: "letter", Name: "纸信", Projection: "public"}, {ID: "distant", Name: "异地秘密物品", Projection: "public"}}
	def.InitialItems = []story.InitialItem{{InstanceID: "wrench", DefinitionID: "tool", HolderID: "player"}, {InstanceID: "letter", DefinitionID: "letter", LocationID: "dock"}, {InstanceID: "distant", DefinitionID: "distant", LocationID: "island"}}
	session, err := NewCreationSession(def)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func resourceScene(states, items string) string {
	return creationTestJSON(`{"narrative":"工匠收下两枚港币，把纸信交给你。","scene_changes":{"elapsed_minutes":2,"state_changes":` + states + `,"item_moves":` + items + `}}`)
}

func TestCreationResourcesAcceptTogetherAndFeedNextContext(t *testing.T) {
	s := creationResourceSession(t)
	text := resourceScene(`[{"entity_id":"player","state_id":"coins","delta":-2,"reason":"已支付工钱"},{"entity_id":"player","state_id":"strain","delta":2,"reason":"检修后略累"}]`, `[{"instance_id":"letter","holder_id":"player","reason":"接过桌上的信"}]`)
	result, err := s.Interact(t.Context(), New(nil, Deps{}), creationFixedGenerator(text), CreationOptions{Input: "我付两枚港币并接过信。", Reasoning: model.ReasoningOff})
	if err != nil || result.Report.CoreCalls != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	snap := s.PlayerSnapshot()
	if snap.States["player"]["coins"].Value.Integer != 8 || snap.States["player"]["strain"].Value.Integer != 3 || snap.Items["letter"].HolderID != "player" || snap.Summary.TurnSeq != 1 {
		t.Fatal("accepted resources missing", snap.States, snap.Items)
	}
	if snap.States["player"]["coins"].UpdatedTurn != 1 || snap.Items["letter"].SourceEvent == "opening" {
		t.Fatal("provenance missing")
	}
	public, _ := json.Marshal(PlayerStateProjection(snap))
	if strings.Contains(string(public), "secret") || len(PlayerItemProjection(snap)) != 2 {
		t.Fatal("public projection leaked resources")
	}
	material, _ := s.creationMaterial(CreationOptions{Input: "我再核对一下现金。"})
	if !strings.Contains(material.Required, `"coins":8`) || strings.Contains(creationResourceContext(snap, []string{"player", "npc:smith"}), "异地秘密物品") {
		t.Fatal("context is stale or includes unrelated items")
	}
	// A second accepted scene uses the new value and increments the accepted turn.
	_, err = s.Interact(t.Context(), New(nil, Deps{}), creationFixedGenerator(resourceScene(`[{"entity_id":"player","state_id":"coins","delta":1,"reason":"工匠退回一枚"}]`, `[{"instance_id":"letter","location_id":"dock","reason":"放回桌上"}]`)), CreationOptions{Input: "我接过退款，把信放回桌上。", Reasoning: model.ReasoningOff})
	if err != nil || s.PlayerSnapshot().States["player"]["coins"].UpdatedTurn != 2 || s.PlayerSnapshot().States["player"]["coins"].Value.Integer != 9 || s.PlayerSnapshot().Items["letter"].HolderID != "" {
		t.Fatal("consecutive resource state", err)
	}
	// Returned public snapshots cannot mutate accepted resource maps.
	snap = s.PlayerSnapshot()
	delete(snap.Items, "letter")
	delete(snap.States["player"], "coins")
	if len(s.PlayerSnapshot().Items) != 3 || len(s.PlayerSnapshot().States["player"]) != 4 {
		t.Fatal("snapshot aliases accepted resources")
	}
}

func TestCreationInvalidResourcesPreserveWholeScene(t *testing.T) {
	cases := []struct{ name, states, items string }{
		{"negative balance", `[{"entity_id":"player","state_id":"coins","delta":-11,"reason":"付款"}]`, `[]`},
		{"currency overwrite", `[{"entity_id":"player","state_id":"coins","value":{"type":"integer","integer":8},"reason":"付款"}]`, `[]`},
		{"read only", `[{"entity_id":"player","state_id":"skill","value":{"type":"boolean"},"reason":"改变技能"}]`, `[]`},
		{"budget", `[{"entity_id":"player","state_id":"strain","delta":4,"reason":"疲倦"}]`, `[]`},
		{"wrong type", `[{"entity_id":"player","state_id":"strain","value":{"type":"boolean","boolean":true},"reason":"疲倦"}]`, `[]`},
		{"duplicate state", `[{"entity_id":"player","state_id":"coins","delta":-1,"reason":"付款"},{"entity_id":"player","state_id":"coins","delta":-1,"reason":"付款"}]`, `[]`},
		{"distant pickup", `[]`, `[{"instance_id":"distant","holder_id":"player","reason":"拿走"}]`},
		{"distant transfer", `[]`, `[{"instance_id":"wrench","holder_id":"npc:merchant","reason":"递给商人"}]`},
		{"unknown instance", `[]`, `[{"instance_id":"new-item","holder_id":"player","reason":"拿走"}]`},
		{"two destinations", `[]`, `[{"instance_id":"letter","holder_id":"player","location_id":"dock","reason":"拿走"}]`},
		{"missing reason", `[{"entity_id":"player","state_id":"coins","delta":-1,"reason":""}]`, `[]`},
		{"valid state invalid item", `[{"entity_id":"player","state_id":"coins","delta":-2,"reason":"付款"}]`, `[{"instance_id":"distant","holder_id":"player","reason":"拿走"}]`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			s := creationResourceSession(t)
			before := s.PlayerSnapshot()
			result, err := s.Interact(t.Context(), New(nil, Deps{}), creationFixedGenerator(resourceScene(test.states, test.items)), CreationOptions{Input: "工匠和商人正在谈价。", Reasoning: model.ReasoningOff})
			if err == nil || result.Report.Repairs != 1 {
				t.Fatal("invalid change accepted", err)
			}
			after := s.PlayerSnapshot()
			if !reflect.DeepEqual(before, after) || len(s.PersonalSources("player")) != 0 {
				t.Fatal("invalid candidate altered accepted scene")
			}
		})
	}
}

func TestCreationCancelledResourcesAndUnchangedScene(t *testing.T) {
	s := creationResourceSession(t)
	before := s.PlayerSnapshot()
	ctx, cancel := context.WithCancel(t.Context())
	g := &creationTestGenerator{answer: func(context.Context, model.TextRequest, int) (model.TextResponse, error) {
		cancel()
		return model.TextResponse{Text: resourceScene(`[{"entity_id":"player","state_id":"coins","delta":-2,"reason":"付款"}]`, `[]`)}, nil
	}}
	if _, err := s.Interact(ctx, New(nil, Deps{}), g, CreationOptions{Input: "我付款。", Reasoning: model.ReasoningOff}); err == nil {
		t.Fatal("cancel accepted")
	}
	if !reflect.DeepEqual(before, s.PlayerSnapshot()) {
		t.Fatal("cancel mutated resources")
	}
	_, err := s.Interact(t.Context(), New(nil, Deps{}), creationFixedGenerator(creationTestJSON(`{"narrative":"工匠说愿意收两枚港币，你还没付款。","scene_changes":{}}`)), CreationOptions{Input: "你愿意收多少？", Reasoning: model.ReasoningOff})
	if err != nil || !reflect.DeepEqual(before.States, s.PlayerSnapshot().States) || !reflect.DeepEqual(before.Items, s.PlayerSnapshot().Items) {
		t.Fatal("omitted changes invented resource updates", err)
	}
}
