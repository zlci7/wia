package turn

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type offsceneRepairGenerator struct {
	failures []string
	requests []model.TextRequest
}

func (g *offsceneRepairGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	response, err := (offsceneMovementGenerator{}).GenerateText(ctx, request)
	if err != nil || !strings.Contains(request.System, "世界剧情行动协调器") {
		return response, err
	}
	index := len(g.requests)
	g.requests = append(g.requests, request)
	var body map[string]any
	if err := json.Unmarshal([]byte(response.Text), &body); err != nil {
		return response, err
	}
	body["time_minutes"] = 5
	body["state_effects"] = []map[string]any{{"entity_id": "npc:clock", "state_id": "strain", "delta": 3, "action_id": body["outcomes"].([]any)[0].(map[string]any)["action_id"]}}
	if index < len(g.failures) {
		switch g.failures[index] {
		case "syntax":
			return model.TextResponse{Text: "{"}, nil
		case "projection":
			delete(body["outcomes"].([]any)[0].(map[string]any), "projections")
		case "scene":
			body["scene_updates"].([]any)[0].(map[string]any)["source_ids"] = []string{
				body["outcomes"].([]any)[0].(map[string]any)["action_id"].(string), "private-invalid-source",
			}
		}
	}
	encoded, err := json.Marshal(body)
	response.Text = string(encoded)
	return response, err
}

func offsceneRepairFixture() (Snapshot, Output, plotResolution) {
	minimum, maximum := 0, 100
	snapshot := Snapshot{
		Summary: wiaworld.WorldSummary{WorldID: "world", Clock: "第 1 日 12:00"},
		Definition: story.Definition{Capabilities: map[string]int{"spatial": 1, "state": 1}, StateDefinitions: []story.StateDefinition{{ID: "strain", Type: "integer", Minimum: &minimum, Maximum: &maximum, UpdatePolicy: story.StateUpdatePolicy{Kind: "model"}}}, Progression: &plot.OpenDefinition{}, Locations: []story.Location{
			{ID: "office", Kind: "place"}, {ID: "clockshop", Kind: "place", Connections: []string{"road"}}, {ID: "road", Kind: "place", Connections: []string{"clinic"}}, {ID: "clinic", Kind: "place"},
		}},
		Characters: []wiaworld.Character{{EntityID: "npc:clock", Name: "钟表匠"}}, Positions: map[string]string{"player": "office", "npc:clock": "clockshop"},
		Sources: map[string]SourceMetadata{}, Perceptions: map[string][]wiaworld.Perception{},
		States: map[string]map[string]wiaworld.EntityState{"npc:clock": {"strain": {EntityID: "npc:clock", StateID: "strain", Value: wiaworld.StateValue{Type: "integer"}, Version: 1, SourceEvent: "opening"}}},
	}
	output := Output{Clock: snapshot.Summary.Clock, SceneVersion: 1, Positions: clonePositions(snapshot.Positions), States: cloneStates(snapshot.States), OpenProgress: &wiaworld.OpenProgress{}, SceneViews: []SceneView{{Recipient: "npc:clock", Content: "钟表铺", SourceIDs: []string{"opening"}}}}
	// Give existing slices spare capacity: rejected attempts must leave even their
	// backing storage intact, rather than merely restoring the visible length.
	output.Events = make([]wiaworld.Event, 1, 100)
	output.Events[0] = wiaworld.Event{EventID: "prior", EventType: "plot_perceived", ActorID: "world", TargetID: "npc:clock", Content: "出发", Stage: 4}
	output.Perceptions = make([]wiaworld.Perception, 1, 100)
	output.Perceptions[0] = wiaworld.Perception{RecipientID: "npc:clock", SourceEventID: "prior", Content: "出发", Stage: 4}
	resolution := plotResolution{Projections: []plotProjection{{Recipient: "npc:clock", Content: "出发"}}, DecisionRequests: []string{"npc:clock"}}
	return snapshot, output, resolution
}

func TestOffsceneBusinessValidationSharesOneRepairOpportunity(t *testing.T) {
	for _, failure := range []string{"projection", "scene"} {
		t.Run(failure, func(t *testing.T) {
			snapshot, output, resolution := offsceneRepairFixture()
			g := &offsceneRepairGenerator{failures: []string{failure}}
			_, err := New(&rosterHost{}, Deps{}).respondToPlot(t.Context(), g, snapshot, wiaworld.Run{RunID: "run"}, "root", resolution, &output)
			if err != nil {
				t.Fatalf("business failure did not get its repair opportunity: %v", err)
			}
			if len(g.requests) != 2 || !strings.Contains(g.requests[1].System, "本地字段校验") || strings.Contains(g.requests[1].System, "private-invalid-source") {
				t.Fatal("repair feedback or attempt limit changed")
			}
			if output.Positions["npc:clock"] != "clinic" || len(output.PositionChanges) != 1 || output.Clock != "第 1 日 12:05" || output.elapsedMinutes != 5 {
				t.Fatal("accepted result was lost or applied more than once")
			}
			if output.States["npc:clock"]["strain"].Value.Integer != 3 || len(output.StateChanges) != 1 || snapshot.States["npc:clock"]["strain"].Value.Integer != 0 {
				t.Fatal("rejected state effects leaked into the accepted candidate or snapshot")
			}
			counts := map[string]int{}
			for _, event := range output.Events {
				counts[event.EventType]++
			}
			if counts["npc_action_intent"] != 1 || counts["npc_action_result"] != 1 || counts["time_advanced"] != 1 {
				t.Fatalf("rejected events leaked: %v", counts)
			}
		})
	}
}

func TestOffsceneRejectedCandidatesLeaveTheOriginalUntouched(t *testing.T) {
	for _, failures := range [][]string{{"syntax", "scene"}, {"scene", "syntax"}, {"scene", "scene"}} {
		t.Run(strings.Join(failures, "-"), func(t *testing.T) {
			snapshot, output, resolution := offsceneRepairFixture()
			before, beforeSnapshot := wire.MarshalJSON(output), wire.MarshalJSON(snapshot)
			events := append([]wiaworld.Event{}, output.Events[:cap(output.Events)]...)
			perceptions := append([]wiaworld.Perception{}, output.Perceptions[:cap(output.Perceptions)]...)
			g := &offsceneRepairGenerator{failures: failures}
			_, err := New(&rosterHost{}, Deps{}).respondToPlot(t.Context(), g, snapshot, wiaworld.Run{RunID: "run"}, "root", resolution, &output)
			if err == nil || len(g.requests) != 2 {
				t.Fatalf("format and business validation did not share two attempts: %v, calls=%d", err, len(g.requests))
			}
			if before != wire.MarshalJSON(output) || beforeSnapshot != wire.MarshalJSON(snapshot) || !reflect.DeepEqual(events, output.Events[:cap(output.Events)]) || !reflect.DeepEqual(perceptions, output.Perceptions[:cap(output.Perceptions)]) {
				t.Fatal("rejected candidate mutated the original turn or snapshot")
			}
		})
	}
}
