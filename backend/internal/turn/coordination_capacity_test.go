package turn

import (
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestMistEmbersOpeningCoordinationFitsApplicationBudget(t *testing.T) {
	pack, err := content.Load(filepath.Join("..", "content", "packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	def := pack.Definition
	snapshot := Snapshot{Definition: def, Characters: def.Characters, Summary: wiaworld.WorldSummary{GameID: "mist-embers", Clock: def.Clock}, SceneVersion: 1, Positions: def.InitialLocations, States: map[string]map[string]wiaworld.EntityState{}, Items: map[string]wiaworld.ItemInstance{}}
	snapshot.SceneViews = initialSceneViews(snapshot)
	for entity, values := range def.InitialStates {
		snapshot.States[entity] = map[string]wiaworld.EntityState{}
		for id, value := range values {
			snapshot.States[entity][id] = wiaworld.EntityState{EntityID: entity, StateID: id, Value: value, SourceEvent: "opening", Version: 1}
		}
	}
	for _, item := range def.InitialItems {
		snapshot.Items[item.InstanceID] = wiaworld.ItemInstance{InstanceID: item.InstanceID, DefinitionID: item.DefinitionID, HolderID: item.HolderID, LocationID: item.LocationID, SourceEvent: "opening", Version: 1}
	}
	run := wiaworld.Run{RunID: "run", Input: "我接受这份寻人委托，但先不收钱。请把她的近照和住址纸条交给我，并告诉我她最后一次离家去了哪里。"}
	events := []wiaworld.Event{{EventID: "run:player-action", EventType: "player_action_intent", ActorID: "player", Content: run.Input}}
	decisions := map[string]NPCDecision{"npc:tailor": {Speech: "谢谢你。我把照片和住址交给你。", ActionIntent: "将照片和纸条递给调查员", ActionTargetID: "player"}}
	material := composeCoordination(snapshot, run, TurnIntent{IntentType: "speak", AddresseeID: "npc:tailor", Visibility: "public"}, decisions, events, decisions["npc:tailor"].Speech, nil)
	material, _ = selectStoryMaterials(snapshot, "coordination", "coordinator", material)
	for _, text := range []string{def.Rules, def.Secret, wire.MarshalJSON(def.Locations), CoordinationDecisionContext(decisions, snapshot.Characters)} {
		if text == "" {
			continue
		}
		if count := strings.Count(material.Required, text); count != 1 {
			t.Fatalf("coordination material appears %d times, want one", count)
		}
	}
	composer := ContextComposer{Window: model.WindowLimits{ContextTokens: 131072, OutputTokens: 12800}, ReasoningReserve: 8192}
	if _, report, err := composer.Build(material, material.System, structuredTurnOutputTokens); err != nil {
		t.Fatalf("opening coordination exceeds application budget: tokens=%d: %v", report.InputTokens, err)
	}
}
