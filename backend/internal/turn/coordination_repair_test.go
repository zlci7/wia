package turn

import (
	"bytes"
	"context"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type repairSequence struct {
	responses []string
	requests  []model.TextRequest
}

func (g *repairSequence) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, req)
	return model.TextResponse{Text: g.responses[min(len(g.requests)-1, len(g.responses)-1)]}, nil
}

type repairStageLog struct{ calls, repairs int }

func (h *repairStageLog) LogStage(_ string, _ wiaworld.Run, _ Stage, _, _ string, _ int, _ string, _ []string, _ string, repairs int, _ time.Duration) {
	h.calls++
	h.repairs = repairs
}

func TestCoordinationRepairsBusinessValidation(t *testing.T) {
	for _, tc := range []struct {
		name, code, field string
		change            func(*hostResult)
	}{
		{"unknown source", "scene_source_unknown", "scene_updates[0].source_ids[0]", func(r *hostResult) { r.SceneUpdates[0].SourceIDs = []string{"PRIVATE_UNKNOWN_SOURCE"} }},
		{"forbidden source", "scene_source_forbidden", "scene_updates[0].source_ids[0]", func(r *hostResult) { r.SceneUpdates[0].SourceIDs = []string{"view:npc:a"} }},
		{"duplicate recipient", "scene_recipient_duplicate", "scene_updates[0].recipients[1]", func(r *hostResult) { r.SceneUpdates[0].Recipients = []string{"player", "player"} }},
		{"unknown recipient", "scene_recipient_unknown", "scene_updates[0].recipients[0]", func(r *hostResult) { r.SceneUpdates[0].Recipients = []string{"PRIVATE_UNKNOWN_RECIPIENT"} }},
		{"missing source", "scene_update_incomplete", "scene_updates[0].source_ids", func(r *hostResult) { r.SceneUpdates[0].SourceIDs = []string{} }},
		{"missing outcome", "action_outcome_count", "outcomes", func(r *hostResult) { r.Outcomes = []hostActionResult{} }},
	} {
		for _, repaired := range []bool{true, false} {
			t.Run(tc.name+"/repaired="+wire.MarshalJSON(repaired), func(t *testing.T) {
				snapshot := Snapshot{Summary: wiaworld.WorldSummary{WorldID: "world", Clock: "第 1 日 12:00", Scene: "room"}, SceneVersion: 1,
					Characters: []wiaworld.Character{{EntityID: "npc:a", InScene: true}},
					SceneViews: []SceneView{{Recipient: "player", Content: "room", Version: 1}, {Recipient: "npc:a", Content: "PRIVATE_VIEW", Version: 1}}}
				run := wiaworld.Run{RunID: "run", Attempt: 1}
				output := Output{Clock: snapshot.Summary.Clock, SceneVersion: 1, Events: make([]wiaworld.Event, 1, 8), Perceptions: make([]wiaworld.Perception, 1, 8)}
				output.Events[0] = wiaworld.Event{EventID: "action", RunID: "run", Stage: 1, EventType: "npc_action_intent", ActorID: "npc:a"}
				output.Perceptions[0] = wiaworld.Perception{RecipientID: "npc:a", Content: "existing"}
				beforeOutput, beforeSnapshot := wire.MarshalJSON(output), wire.MarshalJSON(snapshot)
				good := hostResult{TimeMinutes: 1, Scene: "room", SceneCharacters: []string{"npc:a"},
					Outcomes:     []hostActionResult{{ActionID: "action", Status: "succeeded", Content: "PRIVATE_OUTCOME", Projections: outcomeProjectionFixture("PRIVATE_OUTCOME", []string{"player", "npc:a"}), Recipients: []string{"player"}}},
					SceneUpdates: []sceneUpdate{{Content: "PRIVATE_SCENE", SourceIDs: []string{"action"}, Recipients: []string{"player"}}}}
				goodJSON := wire.MarshalJSON(good)
				tc.change(&good)
				badJSON := wire.MarshalJSON(good)
				g := &repairSequence{responses: []string{badJSON, goodJSON}}
				if !repaired {
					g.responses[1] = badJSON
				}
				var logs bytes.Buffer
				host := &repairStageLog{}
				s := New(host, Deps{Logger: log.New(&logs, "", 0)})
				_, visible, err := s.coordinateStage(context.Background(), g, &snapshot, run, TurnIntent{}, "", &output)
				if len(g.requests) != 2 {
					t.Fatalf("calls=%d err=%v", len(g.requests), err)
				}
				if !strings.Contains(g.requests[1].System, tc.code) || !strings.Contains(g.requests[1].System, tc.field) {
					t.Fatalf("missing repair feedback: %s", g.requests[1].System)
				}
				if !strings.Contains(logs.String(), `error_code="`+tc.code+`"`) || !strings.Contains(logs.String(), `field="`+tc.field+`"`) {
					t.Fatalf("missing diagnostic: %s", logs.String())
				}
				for _, private := range []string{"PRIVATE_VIEW", "PRIVATE_OUTCOME", "PRIVATE_SCENE", "PRIVATE_UNKNOWN_SOURCE", "PRIVATE_UNKNOWN_RECIPIENT"} {
					if strings.Contains(logs.String(), private) || strings.Contains(g.requests[1].System, private) {
						t.Fatalf("response content leaked: %s", private)
					}
				}
				if repaired {
					if err != nil || host.calls != 1 || host.repairs != 1 {
						t.Fatalf("err=%v host=%+v", err, host)
					}
					if len(output.Events) != 4 || len(output.Perceptions) != 3 || len(visible) != 1 || output.SceneVersion != 2 {
						t.Fatalf("invalid repaired output: %+v", output)
					}
					if !reflect.DeepEqual(output.SceneViews[0].SourceIDs, []string{"action:result:1:projection:player"}) {
						t.Fatal("result provenance lost")
					}
				} else {
					if err == nil || host.calls != 0 || beforeOutput != wire.MarshalJSON(output) || beforeSnapshot != wire.MarshalJSON(snapshot) {
						t.Fatalf("failed result applied: %v", err)
					}
					if output.Events[:cap(output.Events)][1].EventID != "" || output.Perceptions[:cap(output.Perceptions)][1].RecipientID != "" {
						t.Fatal("failed attempt mutated shared backing storage")
					}
				}
			})
		}
	}
}

func TestCoordinationRepairSharesAttemptLimit(t *testing.T) {
	invalid := wire.MarshalJSON(hostResult{Scene: "room", SceneCharacters: []string{}, Outcomes: []hostActionResult{}, SceneUpdates: []sceneUpdate{{Content: "room", SourceIDs: []string{"missing"}, Recipients: []string{"player"}}}})
	for _, responses := range [][]string{{"invalid JSON", invalid}, {invalid, "invalid JSON"}} {
		g := &repairSequence{responses: responses}
		snapshot := Snapshot{SceneViews: []SceneView{{Recipient: "player", Content: "room"}}}
		output := Output{}
		host := &repairStageLog{}
		_, _, err := New(host, Deps{}).coordinateStage(context.Background(), g, &snapshot, wiaworld.Run{}, TurnIntent{}, "", &output)
		if err == nil || len(g.requests) != 2 || host.calls != 0 {
			t.Fatalf("unbounded or accepted repair: calls=%d err=%v", len(g.requests), err)
		}
	}
}
