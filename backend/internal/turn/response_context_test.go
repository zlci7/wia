package turn

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestWorldResponseMergesExactExperiencesOnce(t *testing.T) {
	old := wiaworld.Perception{RecipientID: "npc:a", SourceEventID: "shared", Content: "本人已经听到的私密内容", Stage: 3, SceneVersion: 2}
	snapshot := Snapshot{Perceptions: map[string][]wiaworld.Perception{"npc:a": {old}}}
	newProjection := wiaworld.Perception{RecipientID: "npc:a", SourceEventID: "world:projection", Content: "最新世界变化", Stage: 4, SceneVersion: 3}
	differentBody := old
	differentBody.Content = "同一来源的另一条获准内容"
	differentRecipient := old
	differentRecipient.RecipientID = "npc:b"
	differentRecipient.Content = "另一个人物只看到公开动作"
	additions := []wiaworld.Perception{old, newProjection, newProjection, differentBody, differentRecipient}
	mergeConfirmedInput(&snapshot, Output{Perceptions: additions})
	mergePerceptions(&snapshot, additions)
	if got := snapshot.Perceptions["npc:a"]; len(got) != 3 || got[0] != old || got[1] != newProjection || got[2] != differentBody {
		t.Fatalf("confirmed or new personal experience changed: %+v", got)
	}
	if got := snapshot.Perceptions["npc:b"]; len(got) != 1 || got[0] != differentRecipient {
		t.Fatalf("recipient-specific experience changed: %+v", got)
	}
}

func TestMistEmbersWorldActionContextPreservesCompleteRecordsWithinBudget(t *testing.T) {
	pack, err := content.Load(filepath.Join("..", "content", "packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	def := pack.Definition
	snapshot := Snapshot{Definition: def, Characters: def.Characters, Positions: def.InitialLocations, Items: map[string]wiaworld.ItemInstance{"mirror": {InstanceID: "mirror", HolderID: "player", SourceEvent: "latest-transfer"}}}
	output := Output{Clock: "第 1 日 09:45", SceneVersion: 4, SceneViews: initialSceneViews(snapshot)}
	for i := 1; i <= 26; i++ {
		output.Events = append(output.Events, wiaworld.Event{EventID: fmt.Sprintf("run_068b72f0dbd17ea36178e4f5:part:2:action:result:%d:projection:player", i), EventType: "action_perceived", ActorID: "npc:reporter", TargetID: "player", Content: fmt.Sprintf("已确认结果%d：玩家进入报社后看到的是普通接待情境，记者本人根据亲历的最新变化决定如何调查。", i), RunID: "run_068b72f0dbd17ea36178e4f5", Stage: i % 7, SceneVersion: 4, SourceType: "action_succeeded", CreatedAt: time.Now().UTC()})
	}
	action := wiaworld.Event{EventID: "current:reporter:action", EventType: "npc_action_intent", ActorID: "npc:reporter", Content: "本人决定向旧诊所调查新的消息", Stage: 5, SceneVersion: 4}
	extra := Output{Events: []wiaworld.Event{action}}
	allowed := map[string][]string{action.EventID: {"npc:reporter", "player"}}
	root := wiaworld.Event{EventID: "world:stimulus", EventType: "plot_result", Content: "最新世界事实", SourceType: "plot_occurred"}
	projection := wiaworld.Event{EventID: "world:stimulus:projection:0", EventType: "plot_perceived", TargetID: "npc:reporter", Content: "本人看到了新的变化", SourceType: "plot_observed"}
	output.Events = append(output.Events, root, projection)
	material := composePlotActions(snapshot, output, extra, root.EventID, allowed, map[string]NPCDecision{"npc:reporter": {ActionIntent: action.Content}}, 90)
	request, report, err := (ContextComposer{}).Build(material, material.System, structuredTurnOutputTokens)
	t.Logf("input=%d", report.InputTokens)
	if err != nil || !report.RequiredComplete || request.MaxInputTokens != 12000 {
		t.Fatalf("complete world action material exceeds unchanged budget: %v %+v", err, report)
	}
	for _, event := range append(output.Events, action) {
		if !strings.Contains(request.Input, event.Content) || !wiaworld.ContainsID(report.SelectedSources, event.EventID) {
			t.Fatalf("required event or source missing: %s", event.EventID)
		}
	}
	for _, required := range []string{root.EventID, wire.MarshalJSON(allowed), `"holder_id":"player"`, `"source_event_id":"latest-transfer"`, "预算：90分钟"} {
		if !strings.Contains(request.Input, required) {
			t.Fatalf("required action context missing: %s", required)
		}
	}
	for _, event := range output.Events {
		if strings.Contains(request.Input, wire.MarshalJSON(event)) {
			t.Fatal("world coordination received a storage envelope")
		}
	}
	legacy := material
	legacy.Required = strings.Replace(legacy.Required, worldProgressionRecords(output.Events), wire.MarshalJSON(output.Events), 1)
	_, oldReport, oldErr := (ContextComposer{}).Build(legacy, legacy.System, structuredTurnOutputTokens)
	t.Logf("storage-envelope input=%d", oldReport.InputTokens)
	if !errors.Is(oldErr, ErrContextCapacity) {
		t.Fatal("fixture must reproduce the storage-envelope capacity failure", oldErr)
	}
	large := strings.Repeat("完整必需记录", 20000)
	output.Events = []wiaworld.Event{{EventID: root.EventID, EventType: "plot_result", Content: large}}
	material = composePlotActions(snapshot, output, extra, root.EventID, allowed, nil, 90)
	if !strings.Contains(material.Required, large) {
		t.Fatal("required action record truncated before capacity validation")
	}
	_, report, err = (ContextComposer{}).Build(material, material.System, structuredTurnOutputTokens)
	if !errors.Is(err, ErrContextCapacity) || report.RequiredComplete {
		t.Fatal("oversized action record must fail before model call", err)
	}
}
