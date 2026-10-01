package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

type offsceneMovementGenerator struct{}

func (offsceneMovementGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	switch {
	case strings.Contains(request.System, "重要 NPC"):
		return model.TextResponse{Text: `{"speech":"","action_intent":"我按自己的计划前往诊所。","silent":true,"memory":"回收时机已经到来。"}`}, nil
	case strings.Contains(request.System, "世界剧情行动协调器"):
		marker := "待处理NPC记录："
		start := strings.Index(request.Input, marker)
		if start < 0 {
			return model.TextResponse{}, errors.New("records missing")
		}
		start += len(marker)
		end := strings.Index(request.Input[start:], "\n")
		var events []wiaworld.Event
		if end < 0 || json.Unmarshal([]byte(request.Input[start:start+end]), &events) != nil {
			return model.TextResponse{}, errors.New("invalid records")
		}
		id := ""
		for _, event := range events {
			if event.EventType == "npc_action_intent" {
				id = event.EventID
			}
		}
		if id == "" {
			return model.TextResponse{}, errors.New("action missing")
		}
		body, _ := json.Marshal(map[string]any{"outcomes": []map[string]any{{"action_id": id, "status": "succeeded", "content": "钟表匠抵达诊所。", "projections": []map[string]string{{"recipient": "npc:clock", "content": "钟表匠抵达诊所。"}}, "recipients": []string{"npc:clock"}}}, "scene_updates": []map[string]any{{"content": "钟表匠已经抵达诊所。", "source_ids": []string{id}, "recipients": []string{"npc:clock"}}}, "movements": []map[string]any{{"entity_id": "npc:clock", "from": "clockshop", "to": "clinic", "route": []string{"clockshop", "road", "clinic"}, "action_id": id}}})
		return model.TextResponse{Text: string(body)}, nil
	default:
		return model.TextResponse{}, errors.New("unexpected stage")
	}
}

func TestOffscenePlanMovementUsesIntentResultAndPersonalProjection(t *testing.T) {
	snapshot := Snapshot{
		Summary:    wiaworld.WorldSummary{WorldID: "world", TurnSeq: 2, Clock: "第 1 日 12:00"},
		Definition: story.Definition{Capabilities: map[string]int{"spatial": 1}, Locations: []story.Location{{ID: "office", Kind: "place", Connections: []string{}}, {ID: "clockshop", Kind: "place", Connections: []string{"road"}}, {ID: "road", Kind: "place", Connections: []string{"clinic"}}, {ID: "clinic", Kind: "place", Connections: []string{}}}, Characters: []wiaworld.Character{{EntityID: "npc:clock", Name: "钟表匠", Role: "钟表匠", Profile: "按计划行动。"}}},
		Characters: []wiaworld.Character{{EntityID: "npc:clock", Name: "钟表匠", Role: "钟表匠", Profile: "按计划行动。"}},
		Positions:  map[string]string{"player": "office", "npc:clock": "clockshop"}, PositionSources: map[string]string{"player": "opening", "npc:clock": "opening"},
		States: map[string]map[string]wiaworld.EntityState{}, Relationships: []wiaworld.Relationship{}, Items: map[string]wiaworld.ItemInstance{}, Perceptions: map[string][]wiaworld.Perception{}, Sources: map[string]SourceMetadata{}, LongMemory: map[string]MemoryContext{},
	}
	output := Output{Clock: snapshot.Summary.Clock, SceneVersion: 1, SceneCharacters: []string{}, Positions: clonePositions(snapshot.Positions), States: map[string]map[string]wiaworld.EntityState{}, Relationships: []wiaworld.Relationship{}, Items: map[string]wiaworld.ItemInstance{}, SceneViews: []SceneView{{Recipient: "npc:clock", Content: "钟表匠在钟表铺。", SourceIDs: []string{"opening"}}}, Events: []wiaworld.Event{}, Perceptions: []wiaworld.Perception{}}
	resolution := plotResolution{Status: "occurred", Content: "钟表匠自己的回收时机到了。", SourceIDs: []string{"definition:plot:node"}, Projections: []plotProjection{{Recipient: "npc:clock", Content: "你预定的回收时机已经到了。"}}, DecisionRequests: []string{"npc:clock"}}
	_, err := New(&rosterHost{}, Deps{}).publishPlotResolution(context.Background(), offsceneMovementGenerator{}, snapshot, wiaworld.Run{RunID: "run"}, "plot:node", resolution, &output)
	if err != nil {
		t.Fatal(err)
	}
	if output.Positions["npc:clock"] != "clinic" || len(output.PositionChanges) != 1 || len(output.SceneCharacters) != 0 {
		t.Fatalf("positions=%v changes=%v scene=%v", output.Positions, output.PositionChanges, output.SceneCharacters)
	}
	for _, perception := range output.Perceptions {
		if perception.RecipientID == "player" && strings.Contains(perception.Content, "抵达诊所") {
			t.Fatal("offscene result leaked to player")
		}
	}
	foundIntent, foundResult := false, false
	for _, event := range output.Events {
		foundIntent = foundIntent || event.EventType == "npc_action_intent"
		foundResult = foundResult || event.EventType == "npc_action_result"
	}
	if !foundIntent || !foundResult {
		t.Fatalf("events=%+v", output.Events)
	}
}
