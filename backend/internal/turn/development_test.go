package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestDevelopmentsShareEvaluationAcrossFreshRuns(t *testing.T) {
	snapshot := Snapshot{Definition: story.Definition{Clock: "第 1 日 09:00", Revision: "r", Progression: &plot.OpenDefinition{Developments: []plot.Development{{ID: "a", MaterialIDs: []string{"a"}}, {ID: "b", MaterialIDs: []string{"b"}}}}, Materials: []story.Material{
		{ID: "a", Purpose: "development", Visibility: "author", Delivery: "on_demand", Body: "Situation A", Summary: "A"},
		{ID: "b", Purpose: "development", Visibility: "author", Delivery: "on_demand", Body: "Situation B", Summary: "B"},
	}}, Summary: wiaworld.WorldSummary{Clock: "第 1 日 09:00"}}
	progress, err := InitialOpenProgress(snapshot.Definition)
	if err != nil {
		t.Fatal(err)
	}
	for i, expected := range []string{"a", "b", "a", "b"} {
		clock := fmt.Sprintf("第 1 日 09:%02d", i)
		output := Output{Clock: clock, OpenProgress: cloneOpenProgress(progress), Events: []wiaworld.Event{{EventID: fmt.Sprintf("run:%d:input", i), EventType: "player_action_result", ActorID: "player", Content: "A brief wait."}}}
		response := plotResolution{Status: "deferred", Content: "No new external fact.", SourceIDs: []string{"material:r:" + expected}, Projections: []plotProjection{}, DecisionRequests: []string{}}
		g := &materialTestGenerator{responses: []string{wire.MarshalJSON(response)}}
		_, err := New(&rosterHost{}, Deps{}).advanceOpenWorld(context.Background(), g, snapshot, wiaworld.Run{RunID: fmt.Sprintf("run:%d", i)}, &output)
		if err != nil {
			t.Fatal(err)
		}
		if len(g.requests) != 1 {
			t.Fatal("world evaluation count changed")
		}
		if !containsDevelopmentRequest(g.requests[0].Input, expected) {
			t.Fatalf("development %s never received an evaluation", expected)
		}
		// Simulate the persisted open state being reloaded between runs.
		var restored wiaworld.OpenProgress
		if err := json.Unmarshal([]byte(wire.MarshalJSON(output.OpenProgress)), &restored); err != nil {
			t.Fatal(err)
		}
		progress = &restored
	}
	for i := 0; i < 2; i++ {
		output := Output{Clock: "第 1 日 09:03", OpenProgress: cloneOpenProgress(progress), Events: []wiaworld.Event{{EventID: fmt.Sprintf("fresh:%d", i), EventType: "player_action_result", ActorID: "player", Content: "A brief wait."}}}
		g := &materialTestGenerator{responses: []string{wire.MarshalJSON(plotResolution{Status: "deferred", Content: "No change.", SourceIDs: []string{"material:r:a"}, Projections: []plotProjection{}, DecisionRequests: []string{}})}}
		_, err := New(&rosterHost{}, Deps{}).advanceOpenWorld(t.Context(), g, snapshot, wiaworld.Run{RunID: fmt.Sprintf("fresh:%d", i)}, &output)
		if err != nil || len(g.requests) != 1-i {
			t.Fatalf("fresh IDs forced a repeated evaluation: calls=%d err=%v", len(g.requests), err)
		}
		progress = output.OpenProgress
	}
}

func containsDevelopmentRequest(input, id string) bool {
	return strings.Contains(input, "待评估对象："+id+"；")
}

func TestDevelopmentBasisTracksRelatedMeaningAndValues(t *testing.T) {
	development := plot.Development{ID: "a", EntityIDs: []string{"npc:a"}, MaterialIDs: []string{"pressure"}}
	fixture := func() (Snapshot, Output) {
		return Snapshot{Definition: story.Definition{Materials: []story.Material{{ID: "pressure", ItemIDs: []string{"mirror"}}}}, Positions: map[string]string{"npc:a": "office", "npc:b": "remote"}, States: map[string]map[string]wiaworld.EntityState{"npc:b": {"fatigue": {Value: wiaworld.StateValue{Type: "integer", Integer: 1}}}}, Items: map[string]wiaworld.ItemInstance{"mirror": {HolderID: "npc:a"}}}, Output{Clock: "第 1 日 09:00", Events: []wiaworld.Event{{EventID: "run:1:event", RunID: "run:1", ActorID: "npc:a", Content: "The clue was confirmed."}}}
	}
	for _, test := range []struct {
		name    string
		changed bool
		mutate  func(*Snapshot, *Output)
	}{
		{"fresh-event-identity", false, func(s *Snapshot, o *Output) {
			o.Events[0].EventID, o.Events[0].RunID, o.Events[0].Stage = "run:2:event", "run:2", 9
		}},
		{"unrelated-state", false, func(s *Snapshot, o *Output) {
			s.States["npc:b"]["fatigue"] = wiaworld.EntityState{Value: wiaworld.StateValue{Type: "integer", Integer: 20}}
		}},
		{"related-item", true, func(s *Snapshot, o *Output) { s.Items["mirror"] = wiaworld.ItemInstance{LocationID: "clinic"} }},
		{"related-event", true, func(s *Snapshot, o *Output) { o.Events[0].Content = "The clue was contradicted." }},
		{"time", true, func(s *Snapshot, o *Output) { o.Clock = "第 1 日 09:01" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, output := fixture()
			before := developmentBasis(snapshot, output, development)
			test.mutate(&snapshot, &output)
			if changed := before != developmentBasis(snapshot, output, development); changed != test.changed {
				t.Fatalf("basis changed=%t, want %t", changed, test.changed)
			}
		})
	}
}
