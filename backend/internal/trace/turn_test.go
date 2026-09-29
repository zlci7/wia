package trace

import (
	"context"
	"errors"
	"testing"
)

type memoryRecorder struct {
	events []Event
}

func (r *memoryRecorder) Record(event Event) {
	r.events = append(r.events, event)
}

func (r *memoryRecorder) Close(ctx context.Context) error {
	return nil
}

func TestTurnTracerEmitsContextAndSequentialEvents(t *testing.T) {
	recorder := &memoryRecorder{}
	ctx := TurnContext{
		GameID:    "fake-game",
		WorldID:   "world-1",
		SessionID: "session-1",
		EventID:   "event-1",
		EventType: "player_interacted_with_npc",
		EntityID:  "npc:Linus",
	}

	tracer := NewTurnTracer(recorder, ctx)
	tracer.Emit(EventTurnStarted, EventData{})
	tracer.Emit(EventToolCallSelected, EventData{
		Tool: "emote",
		Fields: Fields{
			"tool_schema_count": 2,
		},
	})

	if len(recorder.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recorder.events))
	}

	first := recorder.events[0]
	if first.SchemaVersion != 1 {
		t.Fatalf("expected schema version 1, got %d", first.SchemaVersion)
	}
	if first.Seq != 1 {
		t.Fatalf("expected first seq 1, got %d", first.Seq)
	}
	if first.TurnID == "" {
		t.Fatal("expected turn_id to be set")
	}
	if first.TraceID != first.TurnID {
		t.Fatalf("expected trace_id == turn_id, got trace_id=%q turn_id=%q", first.TraceID, first.TurnID)
	}
	if first.GameID != ctx.GameID || first.WorldID != ctx.WorldID || first.SessionID != ctx.SessionID || first.EventID != ctx.EventID || first.EventType != ctx.EventType || first.EntityID != ctx.EntityID {
		t.Fatalf("event context mismatch: got %+v", first)
	}

	second := recorder.events[1]
	if second.Seq != 2 {
		t.Fatalf("expected second seq 2, got %d", second.Seq)
	}
	if second.Tool != "emote" {
		t.Fatalf("expected tool emote, got %q", second.Tool)
	}
	if second.Fields["tool_schema_count"] != 2 {
		t.Fatalf("expected tool_schema_count field to be preserved, got %#v", second.Fields["tool_schema_count"])
	}
}

func TestTurnTracerCanUseCallerProvidedTurnID(t *testing.T) {
	recorder := &memoryRecorder{}
	tracer := NewTurnTracerWithID(recorder, TurnContext{EntityID: "npc:Linus"}, "turn-explicit")

	tracer.Emit(EventTurnStarted, EventData{})
	tracer.Complete(EventData{})

	if len(recorder.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(recorder.events))
	}
	for i, event := range recorder.events {
		if event.TurnID != "turn-explicit" {
			t.Fatalf("event[%d].TurnID = %q, want turn-explicit", i, event.TurnID)
		}
		if event.TraceID != "turn-explicit" {
			t.Fatalf("event[%d].TraceID = %q, want turn-explicit", i, event.TraceID)
		}
	}
}

func TestTurnTracerCompleteIsTerminalFinal(t *testing.T) {
	recorder := &memoryRecorder{}
	tracer := NewTurnTracer(recorder, TurnContext{EntityID: "npc:Linus"})

	tracer.Emit(EventTurnStarted, EventData{})
	tracer.Complete(EventData{ActionID: "act-1", Tool: "speak"})
	tracer.Emit(EventToolCallSelected, EventData{Tool: "emote"})
	tracer.Fail("model", "provider_failed", errors.New("should be ignored"), EventData{})
	tracer.Complete(EventData{})

	if len(recorder.events) != 2 {
		t.Fatalf("expected 2 events after terminal no-op, got %d", len(recorder.events))
	}

	terminal := recorder.events[1]
	if terminal.Event != EventTurnCompleted {
		t.Fatalf("expected terminal event %q, got %q", EventTurnCompleted, terminal.Event)
	}
	if terminal.Seq != 2 {
		t.Fatalf("expected terminal seq 2, got %d", terminal.Seq)
	}
	if terminal.ActionID != "act-1" || terminal.Tool != "speak" {
		t.Fatalf("terminal action data mismatch: got action_id=%q tool=%q", terminal.ActionID, terminal.Tool)
	}
}

func TestTurnTracerFailIsTerminalFinal(t *testing.T) {
	recorder := &memoryRecorder{}
	tracer := NewTurnTracer(recorder, TurnContext{EntityID: "npc:Linus"})

	tracer.Emit(EventTurnStarted, EventData{})
	tracer.Fail("model", "provider_timeout", errors.New("context deadline exceeded"), EventData{
		Fields: Fields{
			"provider": "deepseek",
		},
	})
	tracer.Emit(EventToolCallSelected, EventData{Tool: "speak"})
	tracer.Complete(EventData{})

	if len(recorder.events) != 2 {
		t.Fatalf("expected 2 events after terminal no-op, got %d", len(recorder.events))
	}

	terminal := recorder.events[1]
	if terminal.Event != EventTurnFailed {
		t.Fatalf("expected terminal event %q, got %q", EventTurnFailed, terminal.Event)
	}
	if terminal.Stage != "model" {
		t.Fatalf("expected stage model, got %q", terminal.Stage)
	}
	if terminal.Reason != "provider_timeout" {
		t.Fatalf("expected reason provider_timeout, got %q", terminal.Reason)
	}
	if terminal.ErrorMessage != "context deadline exceeded" {
		t.Fatalf("expected error message to be recorded, got %q", terminal.ErrorMessage)
	}
	if terminal.Fields["provider"] != "deepseek" {
		t.Fatalf("expected provider field to be preserved, got %#v", terminal.Fields["provider"])
	}
}

func TestTurnTracerFailAcceptsNilError(t *testing.T) {
	recorder := &memoryRecorder{}
	tracer := NewTurnTracer(recorder, TurnContext{EntityID: "npc:Linus"})

	tracer.Fail("action", "action_result_failed", nil, EventData{
		ActionID: "act-1",
		Tool:     "emote",
		Fields: Fields{
			"action_status": "REJECTED",
		},
	})
	tracer.Emit(EventActionResultReceived, EventData{})

	if len(recorder.events) != 1 {
		t.Fatalf("expected 1 event after terminal no-op, got %d", len(recorder.events))
	}

	event := recorder.events[0]
	if event.Event != EventTurnFailed {
		t.Fatalf("expected event %q, got %q", EventTurnFailed, event.Event)
	}
	if event.ErrorMessage != "" {
		t.Fatalf("expected no error message for business failure, got %q", event.ErrorMessage)
	}
	if event.ActionID != "act-1" || event.Tool != "emote" {
		t.Fatalf("action fields mismatch: got action_id=%q tool=%q", event.ActionID, event.Tool)
	}
	if event.Fields["action_status"] != "REJECTED" {
		t.Fatalf("expected action_status REJECTED, got %#v", event.Fields["action_status"])
	}
}

func TestTurnTracerSuspendResumeEventsAreNonTerminal(t *testing.T) {
	recorder := &memoryRecorder{}
	tracer := NewTurnTracer(recorder, TurnContext{EntityID: "npc:Linus"})

	tracer.Emit(EventTurnStarted, EventData{})
	tracer.Emit(EventActionStatusUpdateReceived, EventData{
		ActionID: "act-1",
		Tool:     "move_to",
		Fields: Fields{
			"action_status": "ACTION_STATUS_ACCEPTED",
		},
	})
	tracer.Emit(EventTurnSuspended, EventData{ActionID: "act-1", Tool: "move_to"})
	tracer.Emit(EventTurnResumed, EventData{ActionID: "act-1", Tool: "move_to"})
	tracer.Complete(EventData{})

	if len(recorder.events) != 5 {
		t.Fatalf("event count = %d, want 5", len(recorder.events))
	}
	if recorder.events[1].Event != EventActionStatusUpdateReceived {
		t.Fatalf("second event = %q, want %q", recorder.events[1].Event, EventActionStatusUpdateReceived)
	}
	if recorder.events[2].Event != EventTurnSuspended {
		t.Fatalf("third event = %q, want %q", recorder.events[2].Event, EventTurnSuspended)
	}
	if recorder.events[3].Event != EventTurnResumed {
		t.Fatalf("fourth event = %q, want %q", recorder.events[3].Event, EventTurnResumed)
	}
	if recorder.events[4].Event != EventTurnCompleted {
		t.Fatalf("last event = %q, want %q", recorder.events[4].Event, EventTurnCompleted)
	}
}
