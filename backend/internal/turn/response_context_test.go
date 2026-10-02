package turn

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestWorldActionFactBodiesRoundTripWithoutMergingAudiencesOrOrder(t *testing.T) {
	body := strings.Repeat("已发生的完整对白，含\"引号\"与\n换行。", 12)
	events := []wiaworld.Event{
		{EventID: "speech", EventType: "npc_speech", ActorID: "npc:a", Content: body, Stage: 1, SceneVersion: 4, SourceType: "author_speech"},
		{EventID: "speech:player", EventType: "npc_dialogue", ActorID: "npc:a", TargetID: "player", Content: body, Stage: 1, SceneVersion: 4, SourceType: "speech_private"},
		{EventID: "intervening", EventType: "action_perceived", TargetID: "npc:b", Content: "另一人物只看到公开动作", Stage: 3, SceneVersion: 5, SourceType: "action_succeeded"},
		{EventID: "later:own", EventType: "npc_speech", ActorID: "npc:a", Content: body, Stage: 5, SceneVersion: 6, SourceType: "author_speech"},
	}
	text := plotActionFactContext(events)
	var table struct {
		Columns []string            `json:"columns"`
		Records [][]json.RawMessage `json:"records"`
		Bodies  []string            `json:"bodies"`
	}
	if err := json.Unmarshal([]byte(text), &table); err != nil || len(table.Bodies) != 2 {
		t.Fatal("repeated bodies did not use a complete text table", err)
	}
	var decoded []wiaworld.Event
	for _, row := range table.Records {
		values := map[string]json.RawMessage{}
		for i, name := range table.Columns {
			values[name] = row[i]
		}
		var index int
		if err := json.Unmarshal(values["content_index"], &index); err != nil {
			t.Fatal(err)
		}
		values["content"] = json.RawMessage(wire.MarshalJSON(table.Bodies[index]))
		var event wiaworld.Event
		if err := json.Unmarshal([]byte(wire.MarshalJSON(values)), &event); err != nil {
			t.Fatal(err)
		}
		decoded = append(decoded, event)
	}
	if !reflect.DeepEqual(events, decoded) || strings.Count(text, wire.MarshalJSON(body)) != 1 {
		t.Fatal("text sharing merged distinct recipients, event identity, order or source", decoded)
	}
	unique := []wiaworld.Event{{EventID: "one", Content: "独立正文"}}
	if plotActionFactContext(unique) != worldProgressionRecords(unique) {
		t.Fatal("unique event text acquired a larger reference representation")
	}
}

func TestWorldActionsKeepFactsAndNewProposalsWhilePlansRemainOwned(t *testing.T) {
	pack, err := content.Load(filepath.Join("..", "content", "packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{Definition: pack.Definition, Characters: pack.Definition.Characters, Positions: pack.Definition.InitialLocations}
	output := Output{Clock: "第 1 日 10:10", SceneViews: initialSceneViews(snapshot)}
	planText := strings.Repeat("这是本人未来调查计划，不是已经执行的事实。", 45)
	for _, owner := range []string{"npc:reporter", "npc:tailor"} {
		output.Events = append(output.Events, wiaworld.Event{EventID: owner + ":plan:1", EventType: "npc_plan_updated", ActorID: owner, Content: planText})
	}
	intent := wiaworld.Event{EventID: "old:action", EventType: "npc_action_intent", ActorID: "npc:reporter", Content: "本人已裁定的旧提案"}
	result := wiaworld.Event{EventID: intent.EventID + ":result:1", EventType: "npc_action_result", ActorID: intent.ActorID, Content: "旧行动的真实部分成功结果", SourceType: "action_partial"}
	perceived := wiaworld.Event{EventID: result.EventID + ":projection:player", EventType: "action_perceived", ActorID: intent.ActorID, TargetID: "player", Content: "玩家获知的部分结果"}
	stimulus := wiaworld.Event{EventID: "world:stimulus", EventType: "plot_result", ActorID: "world", Content: "最新刺激"}
	output.Events = append(output.Events, intent, result, perceived, stimulus)
	for i := 1; i <= 26; i++ {
		output.Events = append(output.Events, wiaworld.Event{EventID: fmt.Sprintf("earlier:result:%d:projection:player", i), EventType: "action_perceived", ActorID: "npc:tailor", TargetID: "player", Content: fmt.Sprintf("获准结果%d：%s", i, strings.Repeat("玩家实际获准的早先结果，位置已经改变，计划没有被当成执行事实。", 3))})
	}
	newAction := wiaworld.Event{EventID: "new:action", EventType: "npc_action_intent", ActorID: "npc:reporter", Content: "本人新提交的行动"}
	extra := Output{Events: []wiaworld.Event{{EventID: "npc:reporter:plan:2", EventType: "npc_plan_updated", ActorID: "npc:reporter", Content: planText}, newAction}}
	original, pendingOriginal := append([]wiaworld.Event{}, output.Events...), append([]wiaworld.Event{}, extra.Events...)
	material := composePlotActions(snapshot, output, extra, stimulus.EventID, map[string][]string{newAction.EventID: {"npc:reporter"}}, nil, 45)
	request, report, err := (ContextComposer{}).Build(material, material.System, structuredTurnOutputTokens)
	if err != nil || !report.RequiredComplete || request.MaxInputTokens != 12000 {
		t.Fatal("settled facts and new action did not fit", err, report)
	}
	if strings.Contains(request.Input, planText) || strings.Contains(request.Input, intent.Content) {
		t.Fatal("future plans or already resolved proposal entered action coordination")
	}
	for _, event := range append(plotActionRecords(original), newAction) {
		if !strings.Contains(request.Input, event.Content) || !wiaworld.ContainsID(report.SelectedSources, event.EventID) {
			t.Fatal("complete fact, projection or new action missing", event.EventID)
		}
	}
	for _, event := range append(original[:2:2], intent) {
		if wiaworld.ContainsID(report.SelectedSources, event.EventID) {
			t.Fatal("omitted record was offered as supplied provenance", event.EventID)
		}
	}
	if !reflect.DeepEqual(output.Events, original) || !reflect.DeepEqual(extra.Events, pendingOriginal) {
		t.Fatal("record selection changed the events committed with owned plans")
	}
	legacy := material
	legacy.Required = strings.Replace(legacy.Required, plotActionFactContext(plotActionRecords(original)), worldProgressionRecords(plotActionRecords(original)), 1)
	legacy.Required += worldProgressionRecords(append(original[:2:2], extra.Events[0]))
	_, oldReport, oldErr := (ContextComposer{}).Build(legacy, legacy.System, structuredTurnOutputTokens)
	t.Logf("facts/new-actions=%d; with future plans=%d", report.InputTokens, oldReport.InputTokens)
	if !errors.Is(oldErr, ErrContextCapacity) {
		t.Fatal("fixture must reproduce future-plan capacity failure", oldErr)
	}
}

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
	legacy.Required = strings.Replace(legacy.Required, plotActionFactContext(output.Events), wire.MarshalJSON(output.Events), 1)
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
