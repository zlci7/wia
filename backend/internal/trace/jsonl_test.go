package trace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestJSONLRecorderCreatesDirectoryAndWritesEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "traces.jsonl")

	recorder, err := NewJSONLRecorder(path, JSONLRecorderOptions{QueueSize: 8})
	if err != nil {
		t.Fatalf("new JSONL recorder: %v", err)
	}

	recorder.Record(Event{
		SchemaVersion: 1,
		TraceID:       "turn-1",
		TurnID:        "turn-1",
		Seq:           1,
		Event:         EventTurnStarted,
		EntityID:      "npc:Linus",
	})
	recorder.Record(Event{
		SchemaVersion: 1,
		TraceID:       "turn-1",
		TurnID:        "turn-1",
		Seq:           2,
		Event:         EventTurnCompleted,
		ActionID:      "act-1",
		Tool:          "speak",
	})

	if err := recorder.Close(context.Background()); err != nil {
		t.Fatalf("close recorder: %v", err)
	}

	events := readJSONLEvents(t, path)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Event != EventTurnStarted {
		t.Fatalf("expected first event %q, got %q", EventTurnStarted, events[0].Event)
	}
	if events[1].Event != EventTurnCompleted {
		t.Fatalf("expected second event %q, got %q", EventTurnCompleted, events[1].Event)
	}
	if events[1].ActionID != "act-1" || events[1].Tool != "speak" {
		t.Fatalf("second event action data mismatch: action_id=%q tool=%q", events[1].ActionID, events[1].Tool)
	}
}

func TestJSONLRecorderCloseIsIdempotentAndRecordAfterCloseIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traces.jsonl")

	recorder, err := NewJSONLRecorder(path, JSONLRecorderOptions{QueueSize: 8})
	if err != nil {
		t.Fatalf("new JSONL recorder: %v", err)
	}

	recorder.Record(Event{
		SchemaVersion: 1,
		TraceID:       "turn-1",
		TurnID:        "turn-1",
		Seq:           1,
		Event:         EventTurnStarted,
	})

	if err := recorder.Close(context.Background()); err != nil {
		t.Fatalf("first close: %v", err)
	}

	recorder.Record(Event{
		SchemaVersion: 1,
		TraceID:       "turn-1",
		TurnID:        "turn-1",
		Seq:           2,
		Event:         EventTurnCompleted,
	})

	if err := recorder.Close(context.Background()); err != nil {
		t.Fatalf("second close: %v", err)
	}

	events := readJSONLEvents(t, path)
	if len(events) != 1 {
		t.Fatalf("expected 1 event after record-after-close no-op, got %d", len(events))
	}
	if events[0].Event != EventTurnStarted {
		t.Fatalf("expected only event %q, got %q", EventTurnStarted, events[0].Event)
	}
}

func TestJSONLRecorderDropsWhenQueueIsFull(t *testing.T) {
	recorder := &JSONLRecorder{
		events: make(chan Event, 1),
		done:   make(chan struct{}),
	}

	recorder.Record(Event{Event: EventTurnStarted})
	recorder.Record(Event{Event: EventTurnCompleted})

	if got := recorder.Dropped(); got != 1 {
		t.Fatalf("expected 1 dropped event, got %d", got)
	}
}

func TestJSONLRecorderReturnsFirstWriterError(t *testing.T) {
	firstErr := errors.New("first")
	secondErr := errors.New("second")

	recorder := &JSONLRecorder{}
	recorder.setError(firstErr)
	recorder.setError(secondErr)

	if recorder.err != firstErr {
		t.Fatalf("expected first error to be preserved, got %v", recorder.err)
	}
}

func readJSONLEvents(t *testing.T, path string) []Event {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open JSONL file: %v", err)
	}
	defer file.Close()

	var events []Event
	decoder := json.NewDecoder(file)
	for {
		var event Event
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode JSONL event: %v", err)
		}
		events = append(events, event)
	}
	return events
}
