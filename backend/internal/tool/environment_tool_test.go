package tool

import (
	"testing"

	"gameagent/backend/internal/model"
)

func TestBuildActionRequestConvertsToolCallArgumentMapToProtocolStruct(t *testing.T) {
	req, err := BuildActionRequest(ActionRequestInput{
		WorldID:  "world:test",
		EntityID: "npc:Linus",
		ToolCall: model.ToolCall{
			ID:        "call_1",
			Name:      "speak",
			Arguments: map[string]any{"text": "hello"},
		},
	})
	if err != nil {
		t.Fatalf("BuildActionRequest returned error: %v", err)
	}

	if req.Arguments == nil {
		t.Fatal("Arguments is nil")
	}
	if text := req.Arguments.Fields["text"].GetStringValue(); text != "hello" {
		t.Fatalf("text = %q, want hello", text)
	}
}

func TestBuildActionRequestCopiesSourceCorrelation(t *testing.T) {
	req, err := BuildActionRequest(ActionRequestInput{
		WorldID:       "world:test",
		EntityID:      "npc:Linus",
		SourceEventID: "event_1",
		SourceTurnID:  "turn_1",
		ToolCall: model.ToolCall{
			ID:        "call_1",
			Name:      "speak",
			Arguments: map[string]any{"text": "hello"},
		},
	})
	if err != nil {
		t.Fatalf("BuildActionRequest returned error: %v", err)
	}

	if req.SourceEventId != "event_1" {
		t.Fatalf("SourceEventId = %q, want event_1", req.SourceEventId)
	}
	if req.SourceTurnId != "turn_1" {
		t.Fatalf("SourceTurnId = %q, want turn_1", req.SourceTurnId)
	}
}

func TestBuildActionRequestRejectsNonStructSafeArgumentsBeforeExecution(t *testing.T) {
	_, err := BuildActionRequest(ActionRequestInput{
		WorldID:  "world:test",
		EntityID: "npc:Linus",
		ToolCall: model.ToolCall{
			ID:        "call_1",
			Name:      "speak",
			Arguments: map[string]any{"bad": make(chan int)},
		},
	})
	if err == nil {
		t.Fatal("BuildActionRequest returned nil error, want struct conversion failure")
	}
}
