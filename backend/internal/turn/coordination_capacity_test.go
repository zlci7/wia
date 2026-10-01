package turn

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	material := composeCoordination(snapshot, run, TurnIntent{IntentType: "speak", AddresseeID: "npc:tailor", Visibility: "public"}, decisions, events, nil)
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
	t.Run("updated personal views", func(t *testing.T) {
		for i := range snapshot.SceneViews {
			view := &snapshot.SceneViews[i]
			view.Content = "接收者" + view.Recipient + "当前情境：" + strings.Repeat("本人已经通过实际接触获知委托细节，旧行动已经完成；其他地点与人物隐情保持未知。", 4)
		}
		material := composeCoordination(snapshot, run, TurnIntent{IntentType: "speak", AddresseeID: "npc:tailor", Visibility: "public"}, decisions, events, nil)
		material, _ = selectStoryMaterials(snapshot, "coordination", "coordinator", material)
		request, report, err := composer.Build(material, material.System, structuredTurnOutputTokens)
		if err != nil {
			t.Fatalf("updated views exceed unchanged input budget: tokens=%d: %v", report.InputTokens, err)
		}
		for _, view := range snapshot.SceneViews {
			if count := strings.Count(request.Input, view.Content); count != 1 {
				t.Fatalf("scene view %s supplied %d times, want one complete copy", view.Recipient, count)
			}
		}
		if !report.RequiredComplete || request.MaxInputTokens != 12000 {
			t.Fatal("required scene contract changed", report)
		}
	})
	t.Run("complete speech without a flat copy", func(t *testing.T) {
		speech := strings.Repeat("诺拉的失踪目前只有家人的说法，我会核对名字、时间、地点，未见到可靠证据前保留判断。", 30)
		private := "另一人物只向玩家说出的私密消息"
		decisions := map[string]NPCDecision{
			"npc:tailor":   {Speech: speech, SpeechVisibility: "public"},
			"npc:reporter": {Speech: private, SpeechVisibility: "private", SpeechRecipients: []string{"player"}},
		}
		speechEvent := wiaworld.Event{EventID: "run:npc:tailor:speech:1:player", EventType: "npc_dialogue", ActorID: "npc:tailor", TargetID: "player", Content: speech, RunID: run.RunID, Stage: 1, SourceType: "speech_public", CreatedAt: time.Now().UTC()}
		privateEvent := speechEvent
		privateEvent.EventID, privateEvent.ActorID, privateEvent.Content, privateEvent.SourceType = "run:npc:reporter:speech:1:player", "npc:reporter", private, "speech_private"
		actions := append(append([]wiaworld.Event{}, events...), speechEvent, privateEvent)
		material := composeCoordination(snapshot, run, TurnIntent{IntentType: "speak", Visibility: "public"}, decisions, actions, nil)
		request, report, err := composer.Build(material, material.System, structuredTurnOutputTokens)
		t.Logf("complete-speech input=%d", report.InputTokens)
		if err != nil || !report.RequiredComplete || request.MaxInputTokens != 12000 {
			t.Fatal("complete speech exceeds unchanged input budget", err, report)
		}
		for _, text := range []string{speech, private, CoordinationDecisionContext(decisions, snapshot.Characters), worldProgressionRecords(events)} {
			if !strings.Contains(request.Input, text) {
				t.Fatal("speech, scope or action identity missing")
			}
		}
		if count := strings.Count(request.Input, speech); count != 2 {
			t.Fatalf("speech must appear in its proposal and source, got %d copies", count)
		}
		for _, event := range []wiaworld.Event{speechEvent, privateEvent} {
			if !wiaworld.ContainsID(report.SelectedSources, event.EventID) {
				t.Fatal("speech provenance missing", event.EventID)
			}
		}
		legacy := material
		legacy.Required += "\nNPC 已确定的公开对白：" + speech
		_, oldReport, oldErr := composer.Build(legacy, legacy.System, structuredTurnOutputTokens)
		t.Logf("flat-speech input=%d", oldReport.InputTokens)
		if !errors.Is(oldErr, ErrContextCapacity) {
			t.Fatal("fixture must reproduce the redundant public speech failure", oldErr)
		}
	})
}
