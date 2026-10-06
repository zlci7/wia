package turn

import (
	"context"
	"sync"
	"testing"

	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

// Freeze the formal path's call purposes as well as its causal results while
// the validators are extracted. Concurrent first-stage calls have no ordering.
func TestPhase13FormalBaseline(t *testing.T) {
	for _, test := range []struct {
		kind                          string
		npcCalls, events, projections int
	}{{"refusal", 5, 16, 15}, {"private", 4, 12, 11}, {"contested-item", 3, 20, 15}, {"failed-action", 3, 14, 11}} {
		t.Run(test.kind, func(t *testing.T) {
			snapshot := interactionSnapshot("港口办事处")
			g := &interactionGenerator{kind: test.kind, inputs: map[string][]string{}}
			var mu sync.Mutex
			counts := map[string]int{}
			meter := func(ctx context.Context, generator model.TextGenerator, request model.TextRequest, scope ContextScope, _ ContextBuildReport) (model.TextResponse, error) {
				mu.Lock()
				counts[scope.Purpose]++
				mu.Unlock()
				return generator.GenerateText(ctx, request)
			}
			out, err := New(coordinationTestHost{}, Deps{Meter: meter}).executeSnapshot(t.Context(), g, snapshot, wiaworld.Run{RunID: "baseline", Input: "我在工作台旁观察。"})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("model-purpose-counts=%v events=%d projections=%d transfers=%d clock=%s", counts, len(out.Events), len(out.Perceptions), len(out.ItemTransfers), out.Clock)
			if counts["npc"] != test.npcCalls || counts["coordination"] != 1 || counts["narration"] != 1 || counts["intent"] != 1 || len(counts) != 4 || len(out.Events) != test.events || len(out.Perceptions) != test.projections {
				t.Fatalf("formal baseline changed: %v", counts)
			}
			if out.Clock != "2189-12-31 23:56" {
				t.Fatalf("clock changed: %s", out.Clock)
			}
			if test.kind == "contested-item" && (len(out.ItemTransfers) != 1 || out.Items["unique-tool"].HolderID != "npc:a") {
				t.Fatal("unique item baseline changed")
			}
		})
	}
}
