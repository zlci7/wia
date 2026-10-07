package turn

import (
	"context"
	"testing"

	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

type reasoningCapture struct{ requests []model.TextRequest }

func (g *reasoningCapture) TextReasoningReserve() int { return 8192 }
func (g *reasoningCapture) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, req)
	if req.OnDelta != nil {
		req.OnDelta(model.TextDelta{Text: "result"})
	}
	return model.TextResponse{Text: "result"}, nil
}

func TestReasoningOffDoesNotConsumeReserveOrChangeSubsequentCalls(t *testing.T) {
	provider := &reasoningCapture{}
	call := NewContextGenerator(Deps{}, "test", provider, Material{System: "role", Required: "facts"}, Snapshot{}, wiaworld.Run{}, "scene", "", 0, "test").(*ContextGenerator)
	stream := false
	deltas := 0
	_, err := call.generateText(t.Context(), model.TextRequest{System: "role", MaxOutputTokens: 128, Reasoning: model.ReasoningOff, Streaming: &stream, OnDelta: func(model.TextDelta) { deltas++ }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = call.generateText(t.Context(), model.TextRequest{System: "role", MaxOutputTokens: 128})
	if err != nil {
		t.Fatal(err)
	}
	first, next := provider.requests[0], provider.requests[1]
	if first.Reasoning != model.ReasoningOff || first.ReasoningReserveTokens != 0 || first.TotalOutputTokens() != 128 || first.Streams() || deltas != 1 {
		t.Fatal("explicit options were lost")
	}
	if next.ReasoningReserveTokens != 8192 || next.Reasoning != model.ReasoningDefault {
		t.Fatal("off mutated provider defaults")
	}
}
