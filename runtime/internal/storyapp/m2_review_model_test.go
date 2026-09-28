package storyapp

import (
	"context"
	"os"
	"testing"
	"time"

	"gameagent/runtime/internal/llm"
	"gameagent/runtime/internal/model"
)

func TestM2ReviewRealActionAndWait(t *testing.T) {
	path := os.Getenv("WIA_AUTONOMY_MODEL_CONFIG")
	if path == "" {
		t.Skip("opt-in real model")
	}
	p, cfg, err := llm.NewProviderFromConfigFile(path)
	if err != nil || cfg.Provider == "fake" {
		t.Fatal("real provider unavailable")
	}
	g, ok := p.(model.TextGenerator)
	if !ok {
		t.Fatal("text generator unavailable")
	}
	a := newTestApp(t, g)
	ctx := context.Background()
	logger := &recordingLogger{}
	a.logger = logger
	w, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: "orbital-repair", ExpectedRevision: "orbital-repair.pack.v1", RequestKey: "review", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("provider=%s model=%s", cfg.Provider, cfg.Model)
	for _, input := range []string{"我走到公开值班表旁，查看上面已经公开的班次，不操作任何设备。", "我在值班室原地安静等待五分钟，不操作设备，也不接受任务。"} {
		start := time.Now()
		r, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: input, Input: input})
		if err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(310 * time.Second); time.Now().Before(deadline); {
			r, err = a.Run(ctx, w.WorldID, r.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != "running" && r.Status != "accepted" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("status=%s reason=%s elapsed_ms=%d", r.Status, r.Reason, time.Since(start).Milliseconds())
		if r.Status != "completed" {
			t.Log(logger.String())
			t.Fatal("real action incomplete")
		}
		s := readContextSnapshot(t, a, w.WorldID)
		found := false
		for _, e := range s.Events {
			if e.RunID == r.RunID && e.EventType == "player_action_result" {
				found = true
				t.Logf("result=%s content=%s", e.SourceType, e.Content)
			}
		}
		if !found {
			t.Fatal("no player action result")
		}
		t.Logf("clock=%s", s.Summary.Clock)
	}
}
