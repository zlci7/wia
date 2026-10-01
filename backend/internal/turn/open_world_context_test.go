package turn

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestOpenWorldContextPreservesOrderedEventsAndLatestFacts(t *testing.T) {
	snapshot := Snapshot{Definition: story.Definition{Capabilities: map[string]int{"items": 1}}, Items: map[string]wiaworld.ItemInstance{"mirror": {InstanceID: "mirror", HolderID: "player", SourceEvent: "latest-transfer"}}, Positions: map[string]string{"player": "clinic"}}
	output := Output{Clock: "第 1 日 10:00", Positions: snapshot.Positions}
	for i := 1; i <= 33; i++ {
		output.Events = append(output.Events, wiaworld.Event{EventID: fmt.Sprintf("event:%02d", i), EventType: "action_perceived", ActorID: "npc:a", TargetID: "player", Content: fmt.Sprintf("获准结果%d：原文完整保留，含\"引号\"及\n换行。", i), RunID: "current-run", Stage: i % 7, SceneVersion: int64(i), SourceType: "action_succeeded", CreatedAt: time.Now().UTC()})
	}
	text := worldProgressionRecords(output.Events)
	var decoded []wiaworld.Event
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatal(err)
	}
	expected := append([]wiaworld.Event{}, output.Events...)
	for i := range expected {
		expected[i].RunID, expected[i].CreatedAt = "", time.Time{}
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Fatal("event text, identity, actor, recipient, stage, scene version or source changed")
	}
	for _, field := range []string{`"seq":`, `"created_at":`, `"run_id":`} {
		if strings.Contains(text, field) {
			t.Fatalf("persistence metadata supplied to world evaluation: %s", field)
		}
	}
	material, err := composeOpenWorld(snapshot, output, "review", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	request, report, err := (ContextComposer{}).Build(material, material.System, 512)
	if err != nil || !report.RequiredComplete || !strings.Contains(request.Input, text) {
		t.Fatalf("required world records incomplete: %v %+v", err, report)
	}
	for _, event := range output.Events {
		if !wiaworld.ContainsID(report.SelectedSources, event.EventID) {
			t.Fatalf("provided source missing: %s", event.EventID)
		}
	}
	for _, fact := range []string{`"holder_id":"player"`, `"source_event_id":"latest-transfer"`, `"player":"clinic"`} {
		if !strings.Contains(request.Input, fact) {
			t.Fatalf("latest world fact missing: %s", fact)
		}
	}
	if len(text) >= len(wire.MarshalJSON(output.Events)) {
		t.Fatal("purpose projection retained the storage envelope")
	}
}

func TestOpenWorldContextRejectsOversizedRequiredEventWithoutTruncation(t *testing.T) {
	large := strings.Repeat("完整必需事件", 20000)
	output := Output{Events: []wiaworld.Event{{EventID: "large", Content: large}}}
	material, err := composeOpenWorld(Snapshot{}, output, "review", false, nil)
	if err != nil || !strings.Contains(material.Required, large) {
		t.Fatal("required event truncated before budget check", err)
	}
	_, report, err := (ContextComposer{}).Build(material, material.System, 512)
	if !errors.Is(err, ErrContextCapacity) || report.RequiredComplete {
		t.Fatal("oversized required event must fail before model call", err, report)
	}
}
