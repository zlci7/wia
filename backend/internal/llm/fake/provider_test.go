package fake

import (
	"context"
	"testing"

	"gameagent/backend/internal/model"
)

func TestGenerateReturnsModelDecisionWithStableToolCallID(t *testing.T) {
	resp, err := NewProvider().Generate(context.Background(), model.Request{
		Tools: []model.ToolDefinition{{Name: "speak"}},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if resp.Decision.Control.Kind != model.ControlSettle {
		t.Fatalf("control = %q, want settle", resp.Decision.Control.Kind)
	}
	if got := len(resp.Decision.ToolCalls); got != 1 {
		t.Fatalf("tool call count = %d, want 1", got)
	}
	call := resp.Decision.ToolCalls[0]
	if call.ID != "fake_call_1" {
		t.Fatalf("tool call id = %q, want fake_call_1", call.ID)
	}
	if call.Arguments["text"] == "" {
		t.Fatalf("arguments = %+v, want text", call.Arguments)
	}
}
