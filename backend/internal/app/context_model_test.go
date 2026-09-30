package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
)

type liveContextProbe struct {
	model.TextGenerator
	window   model.WindowLimits
	mu       sync.Mutex
	requests []model.TextRequest
}

func (p *liveContextProbe) ModelWindow() model.WindowLimits { return p.window }
func (p *liveContextProbe) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return p.TextGenerator.GenerateText(ctx, req)
}

func TestContextRealModelSequence(t *testing.T) {
	path := os.Getenv("WIA_CONTEXT_MODEL_CONFIG")
	if path == "" {
		t.Skip("set WIA_CONTEXT_MODEL_CONFIG for disposable real-model validation")
	}
	provider, config, err := llm.NewProviderFromConfigFile(path)
	if err != nil {
		t.Fatal("real provider configuration unavailable")
	}
	g, ok := provider.(model.TextGenerator)
	if !ok || config.Provider == "fake" {
		t.Fatal("real text generator required")
	}
	probe := &liveContextProbe{TextGenerator: g}
	if window, ok := provider.(model.WindowProvider); ok {
		probe.window = window.ModelWindow()
	}
	a := newTestApp(t, probe)
	logger := &recordingLogger{}
	a.logger = logger
	w, err := a.createFixtureWorld(context.Background(), "上下文验证", "guided", "旅人", "谨慎而礼貌的旅人", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("provider=%s model=%s", config.Provider, config.Model)
	inputs := []string{"跟老板打声招呼", "最近发生了什么事吗？", "你明白的，最近这边不太平。", "我靠近沈岚耳语，只让她听见：约定的口令是青石，请不要公开复述。", "我恢复正常音量向老板道谢，然后看看眼前的桌子。"}
	for i, input := range inputs {
		probe.mu.Lock()
		probe.requests = nil
		probe.mu.Unlock()
		start := time.Now()
		r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: fmt.Sprint(i), Input: input})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(310 * time.Second)
		for time.Now().Before(deadline) {
			r, err = a.Run(context.Background(), w.WorldID, r.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != "accepted" && r.Status != "running" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		probe.mu.Lock()
		requests := append([]model.TextRequest{}, probe.requests...)
		probe.mu.Unlock()
		t.Logf("turn=%d status=%s reason=%s elapsed_ms=%d calls=%d", i+1, r.Status, r.Reason, time.Since(start).Milliseconds(), len(requests))
		if r.Status != "completed" {
			t.Log(logger.String())
			t.Fatalf("real turn failed: %s", r.Reason)
		}
		if i == 3 {
			var player, recipient, observer bool
			for _, req := range requests {
				if strings.Contains(req.System, "玩家正文 Agent") {
					player = strings.Contains(req.Input, "口令是青石")
				}
				if strings.Contains(req.Input, "阶段：1") && strings.Contains(req.System, "重要 NPC") {
					if strings.Contains(req.Input, "你的身份：沈岚") {
						recipient = strings.Contains(req.Input, "口令是青石")
					}
					if strings.Contains(req.Input, "你的身份：铁杉") {
						observer = true
						if strings.Contains(req.Input, "口令是青石") {
							t.Fatal("private current input reached observer")
						}
					}
				}
			}
			if !player || !recipient || !observer {
				t.Fatal("missing private visibility evidence")
			}
		}
		s := readContextSnapshot(t, a, w.WorldID)
		if i == 2 {
			for _, items := range s.Perceptions {
				for _, p := range items {
					if p.SourceEventID == r.RunID+":input" && p.SourceType == "observed_private_conversation" {
						t.Fatal("probing tone unexpectedly became private")
					}
				}
			}
		}
		for _, m := range s.Messages {
			if m.RunID == r.RunID && m.Kind == "narrative" {
				t.Logf("narrative=%s", m.Content)
			}
		}
	}
}
