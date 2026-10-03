package turn

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func TestKnownLocationsRespectPlayerKnowledge(t *testing.T) {
	snapshot := Snapshot{SceneLocation: "room", Definition: story.Definition{
		KnownLocations: []string{"station", "secret"},
		Locations: []story.Location{
			{ID: "district", Kind: "region", Name: "地区", Public: true},
			{ID: "room", Kind: "place", Parent: "district", Name: "当前房间", Public: true, Connections: []string{"street", "secret"}},
			{ID: "street", Kind: "place", Parent: "district", Name: "街道", Public: true, Connections: []string{"room", "remote"}},
			{ID: "station", Kind: "place", Name: "已知车站", Public: true},
			{ID: "remote", Kind: "place", Name: "未获知的远处地点", Public: true},
			{ID: "secret", Kind: "place", Name: "秘密地点", Public: false},
		},
	}}
	views := PlayerLocationProjection(snapshot)
	ids := []string{}
	for _, view := range views {
		ids = append(ids, view.ID)
		for _, id := range view.Connections {
			if id == "secret" || id == "remote" {
				t.Fatal("connection leaks unknown place")
			}
		}
	}
	if !reflect.DeepEqual(ids, []string{"district", "room", "station", "street"}) {
		t.Fatalf("known locations: %v", ids)
	}
}

func TestCurrencyRuleSettlementIsExactAndAtomic(t *testing.T) {
	minimum, maximum := 0, 100
	currency := &wiaworld.Currency{Name: "积分", Denominations: []wiaworld.Denomination{{Name: "分", Units: 1}}}
	definition := story.StateDefinition{ID: "cash", Type: "integer", Minimum: &minimum, Maximum: &maximum, Scope: "player", UpdatePolicy: story.StateUpdatePolicy{Kind: "rule_only"}, Currency: currency}
	snapshot := Snapshot{Definition: story.Definition{StateDefinitions: []story.StateDefinition{definition}}}
	initial := wiaworld.EntityState{EntityID: "player", StateID: "cash", Value: wiaworld.StateValue{Type: "integer", Integer: 50}, SourceEvent: "opening", Version: 1}
	for _, delta := range []int{-100, 51, math.MaxInt} {
		out := Output{States: map[string]map[string]wiaworld.EntityState{"player": {"cash": initial}}}
		before := cloneStates(out.States)
		err := applyRuleStateEffects(snapshot, &out, "player", "payment", "action", wiaworld.Event{EventID: "result"}, []story.RuleEffect{{Kind: "state_delta", EntityID: "actor", StateID: "cash", Delta: delta}})
		if err == nil || !reflect.DeepEqual(out.States, before) || len(out.StateChanges) != 0 {
			t.Fatalf("invalid payment published: delta=%d err=%v", delta, err)
		}
	}
	out := Output{States: map[string]map[string]wiaworld.EntityState{"player": {"cash": initial}}}
	if err := applyRuleStateEffects(snapshot, &out, "player", "payment", "action", wiaworld.Event{EventID: "result"}, []story.RuleEffect{{Kind: "state_delta", EntityID: "actor", StateID: "cash", Delta: -50}}); err != nil {
		t.Fatal(err)
	}
	if out.States["player"]["cash"].Value.Integer != 0 {
		t.Fatal("exact payment should reach zero")
	}
}

func TestPrivateCurrencyDoesNotEnterNPCContext(t *testing.T) {
	minimum, maximum := 0, 100
	currency := &wiaworld.Currency{Name: "秘密币", Denominations: []wiaworld.Denomination{{Name: "秘密单位", Units: 1}}}
	definition := story.StateDefinition{ID: "cash", Name: "现金", Type: "integer", Minimum: &minimum, Maximum: &maximum, Scope: "player", Projection: "self", Knowledge: "owner", Currency: currency}
	snapshot := Snapshot{Definition: story.Definition{StateDefinitions: []story.StateDefinition{definition}}, States: map[string]map[string]wiaworld.EntityState{"player": {"cash": {EntityID: "player", StateID: "cash", Value: wiaworld.StateValue{Type: "integer", Integer: 50}, SourceEvent: "opening", Version: 1}}}}
	if strings.Contains(MechanicsContext(snapshot, "npc:other"), "秘密币") || strings.Contains(MechanicsContext(snapshot, "npc:other"), "cash") {
		t.Fatal("private wallet leaked into NPC context")
	}
	if !strings.Contains(MechanicsContext(snapshot, "player"), "50 秘密单位") {
		t.Fatal("owner needs exact currency representation")
	}
}
