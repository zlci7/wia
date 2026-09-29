package storyapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type waitResultGenerator struct{ host hostResult }

func (g waitResultGenerator) GenerateText(context.Context, model.TextRequest) (model.TextResponse, error) {
	return model.TextResponse{Text: wire.MarshalJSON(g.host)}, nil
}

type overflowingWaitGenerator struct{ base actionConsistencyGenerator }

func (g overflowingWaitGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(req.System, "结构化回合意图") {
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"","visibility":"public","wait_minutes":5}`}, nil
	}
	if strings.Contains(req.System, "场景协调 Agent") {
		response, err := (actionConsistencyGenerator{status: "succeeded"}).GenerateText(ctx, req)
		if err != nil {
			return response, err
		}
		var host hostResult
		if err = json.Unmarshal([]byte(response.Text), &host); err != nil {
			return model.TextResponse{}, err
		}
		host.TimeMinutes = 30
		return model.TextResponse{Text: wire.MarshalJSON(host)}, nil
	}
	return g.base.GenerateText(ctx, req)
}

func TestExcessWaitRollsBackWholeTurnWithoutPlot(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, overflowingWaitGenerator{})
	withoutWorldEvents(app)
	w, err := app.CreateStoryWorld(ctx, CreateWorldRequest{GameID: "orbital-repair", ExpectedRevision: app.packs["orbital-repair"].Definition.Revision, RequestKey: "wait", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	before := readContextSnapshot(t, app, w.WorldID)
	r, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "wait-overflow", Input: "只等五分钟"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, app, w.WorldID, r.RunID); done.Status != "failed" {
		t.Fatal(done)
	}
	after := readContextSnapshot(t, app, w.WorldID)
	if before.Summary.Clock != after.Summary.Clock || before.Summary.EventHead != after.Summary.EventHead || wire.MarshalJSON(before.Messages) != wire.MarshalJSON(after.Messages) {
		t.Fatal("failed wait partially committed")
	}
}

func TestWaitingRequiresAvailableInterruptionEvidence(t *testing.T) {
	s := worldSnapshot{Summary: wiaworld.WorldSummary{Clock: "第 1 日 19:00"}, Plot: lanternPlotDefinition(), PlotProgress: plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{}}}
	s.Events = []wiaworld.Event{{EventID: "committed-danger", RunID: "previous", Stage: 4, EventType: "plot_result"}}
	events := []wiaworld.Event{{EventID: "current-danger", RunID: "run", Stage: 1, EventType: "npc_action_intent"}}
	for _, tc := range []struct {
		name    string
		minutes int
		ids     []string
		valid   bool
	}{
		{"boundary", 5, nil, true}, {"shortened", 1, nil, false}, {"wrong source", 1, []string{"another-run"}, false}, {"interrupted", 1, []string{"current-danger"}, true},
		{"existing danger", 1, []string{"committed-danger"}, true}, {"future plan", 1, []string{"definition:lantern-dusk.plot.v2:courier_window"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := waitResultGenerator{hostResult{TimeMinutes: tc.minutes, Scene: "客栈", SceneCharacters: []string{}, Outcomes: []hostActionResult{}, SceneUpdates: []sceneUpdate{}, InterruptSources: tc.ids}}
			_, _, err := newTestApp(t, g).coordinateTurn(context.Background(), g, s, wiaworld.Run{RunID: "run"}, turnIntent{IntentType: "act", WaitMinutes: 60}, nil, events, "")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}

func TestRequestedWaitIsAnUpperBound(t *testing.T) {
	for _, definition := range []*plot.Definition{nil, lanternPlotDefinition()} {
		s := worldSnapshot{Summary: wiaworld.WorldSummary{Clock: "第 1 日 19:00"}, Plot: definition, PlotProgress: plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{}}}
		for _, minutes := range []int{5, 30} {
			g := waitResultGenerator{hostResult{TimeMinutes: minutes, Scene: "原地", SceneCharacters: []string{}, Outcomes: []hostActionResult{}, SceneUpdates: []sceneUpdate{}}}
			_, _, err := newTestApp(t, g).coordinateTurn(context.Background(), g, s, wiaworld.Run{RunID: "wait"}, turnIntent{IntentType: "act", WaitMinutes: 5}, nil, nil, "")
			if (err == nil) != (minutes == 5) {
				t.Fatalf("plot=%t minutes=%d err=%v", definition != nil, minutes, err)
			}
		}
	}
}
