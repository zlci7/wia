package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
)

type transportBlockingGenerator struct{ requests chan model.TextRequest }

func (transportBlockingGenerator) SupportsTextStreaming() bool { return true }
func (g transportBlockingGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	g.requests <- request
	<-ctx.Done()
	return model.TextResponse{}, ctx.Err()
}

func TestGenerationTransportPersistsWithoutTouchingModelConfig(t *testing.T) {
	g := transportBlockingGenerator{requests: make(chan model.TextRequest, 4)}
	a := newTestApp(t, g)
	if err := os.MkdirAll(filepath.Dir(a.ModelConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.ModelConfigPath(), []byte("unchanged configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	initial, err := a.Status(t.Context())
	if err != nil || initial.GenerationTransport.Mode != transportStream || !initial.GenerationTransport.StreamingSupported {
		t.Fatalf("initial: %+v %v", initial.GenerationTransport, err)
	}
	if _, err := a.UpdateGenerationTransport(t.Context(), "unknown"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid mode: %v", err)
	}
	if _, err := a.UpdateGenerationTransport(t.Context(), transportNonStream); err != nil {
		t.Fatal(err)
	}
	root := a.DataRoot()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), Options{DataRoot: root, Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	status, err := reopened.Status(t.Context())
	if err != nil || status.GenerationTransport.Mode != transportNonStream {
		t.Fatalf("persisted: %+v %v", status.GenerationTransport, err)
	}
	data, err := os.ReadFile(reopened.ModelConfigPath())
	if err != nil || string(data) != "unchanged configuration" {
		t.Fatalf("configuration changed: %v", err)
	}
}

func TestGenerationTransportCapturedByAcceptedRun(t *testing.T) {
	g := transportBlockingGenerator{requests: make(chan model.TextRequest, 4)}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "传输", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateGenerationTransport(t.Context(), transportNonStream); err != nil {
		t.Fatal(err)
	}
	run, err := a.SubmitRun(t.Context(), w.WorldID, RunRequest{RequestKey: "transport", Input: "我询问情况。"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-g.requests:
		if request.Streaming == nil || *request.Streaming || request.OnDelta == nil {
			t.Fatal("non-stream observer was lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	if _, err := a.UpdateGenerationTransport(t.Context(), transportStream); err != nil {
		t.Fatal(err)
	}
	progress, err := a.ReadRunProgress(t.Context(), w.WorldID, run.RunID, false)
	if err != nil || progress.Transport != transportNonStream {
		t.Fatalf("active run changed: %+v %v", progress, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = a.meteredText(ctx, g, model.TextRequest{Input: "correction"}, turn.ContextScope{World: w.WorldID, Run: run.RunID, Purpose: "scene"}, turn.ContextBuildReport{})
	}()
	select {
	case request := <-g.requests:
		if request.Streaming == nil || *request.Streaming {
			t.Error("later call changed transport")
		}
	case <-time.After(5 * time.Second):
		t.Error("later call did not start")
	}
	cancel()
	<-finished
	if err := a.CancelRun(t.Context(), w.WorldID, run.RunID); err != nil {
		t.Fatal(err)
	}
	if ended := waitRun(t, a, w.WorldID, run.RunID); ended.Status != "cancelled" {
		t.Fatal("cancel did not finish")
	}
	next, err := a.SubmitRun(t.Context(), w.WorldID, RunRequest{RequestKey: "next-transport", Input: "我再问一次。"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-g.requests:
		if request.Streaming == nil || !*request.Streaming {
			t.Error("next run did not use saved transport")
		}
	case <-time.After(5 * time.Second):
		t.Error("next request did not start")
	}
	if err := a.CancelRun(t.Context(), w.WorldID, next.RunID); err != nil {
		t.Fatal(err)
	}
	waitRun(t, a, w.WorldID, next.RunID)
}

func TestGenerationTransportUsesDeclaredProviderCapability(t *testing.T) {
	a := newTestApp(t, &scriptedGenerator{})
	status, err := a.Status(t.Context())
	if err != nil || status.GenerationTransport.Mode != transportNonStream || status.GenerationTransport.StreamingSupported {
		t.Fatalf("capability: %+v %v", status.GenerationTransport, err)
	}
	if _, err := a.UpdateGenerationTransport(t.Context(), transportStream); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unsupported transport accepted: %v", err)
	}
}
