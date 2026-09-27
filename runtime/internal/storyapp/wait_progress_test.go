package storyapp

import (
	"context"
	"testing"

	"gameagent/runtime/internal/model"
)

type waitResultGenerator struct{ host hostResult }

func (g waitResultGenerator) GenerateText(context.Context, model.TextRequest) (model.TextResponse, error) {
	return model.TextResponse{Text: marshalJSON(g.host)}, nil
}

func TestWaitingRequiresCurrentInterruptionEvidence(t *testing.T) {
	s := worldSnapshot{Summary: WorldSummary{Clock: "第 1 日 19:00"}, Plot: lanternPlotDefinition(), PlotProgress: PlotProgress{Version: 1, Nodes: map[string]PlotNodeState{}}}
	events := []Event{{EventID: "current-danger", RunID: "run", Stage: 1, EventType: "npc_action_intent"}}
	for _, tc := range []struct {
		name    string
		minutes int
		ids     []string
		valid   bool
	}{
		{"boundary", 5, nil, true}, {"shortened", 1, nil, false}, {"wrong source", 1, []string{"another-run"}, false}, {"interrupted", 1, []string{"current-danger"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := waitResultGenerator{hostResult{TimeMinutes: tc.minutes, Scene: "客栈", SceneCharacters: []string{}, Outcomes: []hostActionResult{}, SceneUpdates: []sceneUpdate{}, InterruptSources: tc.ids}}
			_, _, err := (&App{}).coordinateTurn(context.Background(), g, s, Run{RunID: "run"}, turnIntent{IntentType: "act", WaitMinutes: 60}, nil, events, "")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}
