package model_test

import (
	"testing"

	"gameagent/backend/internal/model"
)

func TestModelResponseSupportsMultipleToolCalls(t *testing.T) {
	resp := model.Response{
		Decision: model.ModelDecision{
			ToolCalls: []model.ToolCall{
				{
					ID:        "call_1",
					Name:      "speak",
					Arguments: map[string]any{"text": "hello"},
				},
				{
					ID:        "call_2",
					Name:      "emote",
					Arguments: map[string]any{"emote": "happy"},
				},
			},
			Control: model.ControlDirective{Kind: model.ControlContinue},
		},
	}

	if got := len(resp.Decision.ToolCalls); got != 2 {
		t.Fatalf("tool call count = %d, want 2", got)
	}
	if resp.Decision.ToolCalls[0].ID != "call_1" || resp.Decision.ToolCalls[1].ID != "call_2" {
		t.Fatalf("tool call ids = %+v", resp.Decision.ToolCalls)
	}
	if resp.Decision.ToolCalls[0].Arguments["text"] != "hello" {
		t.Fatalf("first arguments = %+v", resp.Decision.ToolCalls[0].Arguments)
	}
}

func TestModelResponseSupportsSettleControl(t *testing.T) {
	resp := model.Response{
		Decision: model.ModelDecision{
			Control: model.ControlDirective{
				Kind:   model.ControlSettle,
				Reason: "nothing to do",
			},
		},
	}

	if resp.Decision.Control.Kind != model.ControlSettle {
		t.Fatalf("control kind = %q, want settle", resp.Decision.Control.Kind)
	}
	if len(resp.Decision.ToolCalls) != 0 {
		t.Fatalf("settle response tool calls = %+v, want none", resp.Decision.ToolCalls)
	}
}

func TestModelMessageSupportsToolCallsAndToolResults(t *testing.T) {
	msg := model.Message{
		Role: model.RoleTool,
		ToolCalls: []model.ToolCall{
			{ID: "call_1", Name: "speak", Arguments: map[string]any{"text": "hello"}},
		},
		ToolResults: []model.ToolResult{
			{ToolCallID: "call_1", Name: "speak", Status: "succeeded", Code: "action_succeeded", Output: map[string]any{"visible": "ok"}},
		},
	}

	if msg.Role != model.RoleTool {
		t.Fatalf("role = %q, want tool", msg.Role)
	}
	if msg.ToolResults[0].ToolCallID != msg.ToolCalls[0].ID {
		t.Fatalf("tool result id = %q, want %q", msg.ToolResults[0].ToolCallID, msg.ToolCalls[0].ID)
	}
}

func TestToolResultSupportsNormalizedStatusCodeAndMessage(t *testing.T) {
	result := model.ToolResult{
		ToolCallID: "call_1",
		Name:       "speak",
		Status:     "invalid",
		Code:       "tool_arguments_missing",
		Message:    "tool arguments are missing",
		Output:     map[string]any{"visible": "value"},
	}

	if result.Status != "invalid" {
		t.Fatalf("Status = %q, want invalid", result.Status)
	}
	if result.Code != "tool_arguments_missing" {
		t.Fatalf("Code = %q, want tool_arguments_missing", result.Code)
	}
	if result.Message != "tool arguments are missing" {
		t.Fatalf("Message = %q, want tool arguments are missing", result.Message)
	}
}

func TestToolCallArgumentsUseProviderNeutralMap(t *testing.T) {
	call := model.ToolCall{
		ID:        "call_1",
		Name:      "speak",
		Arguments: map[string]any{"text": "hello"},
	}

	if _, ok := call.Arguments["text"].(string); !ok {
		t.Fatalf("arguments = %+v, want provider-neutral map", call.Arguments)
	}
}
