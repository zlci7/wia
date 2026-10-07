package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
)

type progressBlockingGenerator struct{ started chan struct{} }

func (g progressBlockingGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	if request.OnDelta != nil {
		request.OnDelta(model.TextDelta{Reasoning: "测试幕后内容", Text: "部分响应"})
	}
	close(g.started)
	<-ctx.Done()
	return model.TextResponse{}, ctx.Err()
}

func TestLiveRunProgressOwnershipAndCancellation(t *testing.T) {
	g := progressBlockingGenerator{started: make(chan struct{})}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "进度", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.SubmitRun(t.Context(), w.WorldID, RunRequest{RequestKey: "progress", Input: "我问现在的情况。"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.started:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	public, err := a.ReadRunProgress(t.Context(), w.WorldID, run.RunID, false)
	if err != nil || public.BudgetSeconds != 300 || len(public.Calls) != 1 || public.Calls[0].Reasoning != "" {
		t.Fatalf("public progress: %+v %v", public, err)
	}
	private, err := a.ReadRunProgress(t.Context(), w.WorldID, run.RunID, true)
	if err != nil || private.Calls[0].Reasoning != "测试幕后内容" {
		t.Fatalf("explicit disclosure: %+v %v", private, err)
	}
	other, err := a.createFixtureWorld(t.Context(), "另一存档", "open", "旅人", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadRunProgress(t.Context(), other.WorldID, run.RunID, true); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("foreign run visible: %v", err)
	}
	a.runsMu.Lock()
	done := a.runs[run.RunID].Done
	a.runsMu.Unlock()
	if err := a.CancelRun(t.Context(), w.WorldID, run.RunID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop the run")
	}
	ended, err := a.ReadRunProgress(t.Context(), w.WorldID, run.RunID, true)
	if err != nil || ended.Status != "cancelled" || len(ended.Calls) != 0 {
		t.Fatalf("cancelled progress: %+v %v", ended, err)
	}
}

func TestRunProgressRequiresExplicitThinkingDisclosure(t *testing.T) {
	var progress runProgressState
	progress.begin(7, turn.ContextScope{Purpose: "scene"})
	progress.delta(7, model.TextDelta{Reasoning: "NPC 私有计划", Text: "{\"beats\":"})
	public := progress.snapshot(false)
	private := progress.snapshot(true)
	if public[0].Reasoning != "" || public[0].ReasoningChars == 0 || private[0].Reasoning != "NPC 私有计划" || private[0].Phase != "writing" {
		t.Fatal("thinking disclosure or streaming state is incorrect")
	}
	public[0].Phase = "changed"
	if progress.snapshot(true)[0].Phase != "writing" {
		t.Fatal("reader mutated the live progress")
	}
	progress.finish(7, nil)
	if progress.snapshot(false)[0].Phase != "received" {
		t.Fatal("finished provider response was not marked as received")
	}
}

func TestRunProgressBoundsThinkingAcrossCalls(t *testing.T) {
	var progress runProgressState
	progress.begin(1, turn.ContextScope{Purpose: "npc", Recipient: "npc:a"})
	progress.delta(1, model.TextDelta{Reasoning: "先"})
	progress.begin(2, turn.ContextScope{Purpose: "npc", Recipient: "npc:b"})
	progress.delta(1, model.TextDelta{Reasoning: strings.Repeat("甲", runReasoningByteLimit)})
	progress.delta(2, model.TextDelta{Reasoning: "乙", Text: "继续"})
	calls := progress.snapshot(true)
	if progress.bytes > runReasoningByteLimit || !utf8.ValidString(calls[0].Reasoning) || !calls[0].ReasoningLimited || !calls[1].ReasoningLimited || calls[1].OutputChars != 2 {
		t.Fatal("bounded text or later output progress is incorrect")
	}
}
