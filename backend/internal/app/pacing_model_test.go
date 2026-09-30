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
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

// Opt-in comparison uses only disposable worlds and never edits the supplied config.
// The baseline removes this iteration's prompt changes; model settings stay identical.
type pacingProbe struct {
	provider model.TextGenerator
	baseline bool
	mu       sync.Mutex
	calls    []string
}

func (p *pacingProbe) ModelWindow() model.WindowLimits {
	if provider, ok := p.provider.(model.WindowProvider); ok {
		return provider.ModelWindow()
	}
	return model.WindowLimits{}
}

func (p *pacingProbe) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	if p.baseline {
		if index := strings.Index(req.System, "\n输出合同："); index >= 0 {
			req.System = req.System[:index]
		}
		if index := strings.Index(req.System, "\ntime_minutes 是"); index >= 0 {
			req.System = req.System[:index]
		}
		if start := strings.Index(req.Input, "连续状态规则："); start == 0 {
			req.Input = req.Input[strings.Index(req.Input, "\n世界：")+1:]
		}
		req.System = strings.ReplaceAll(req.System, "\n叙事节奏规则："+turn.PacingInstruction(), "")
		rule, _ := turn.LengthInstruction(wiaworld.NarrativeSettings{Length: wiaworld.NarrativeLengthStandard})
		req.Input = strings.ReplaceAll(req.Input, rule, "正文使用标准篇幅，通常为 300 至 600 个汉字，完整呈现本轮变化并保持节奏。")
	}
	started := time.Now()
	response, err := p.provider.GenerateText(ctx, req)
	stage := "structured"
	if strings.Contains(req.System, "玩家正文 Agent") {
		stage = "narration"
	}
	if strings.Contains(req.System, "场景协调 Agent") {
		stage = "coordination"
	}
	if strings.Contains(req.System, "重要 NPC") {
		stage = "npc"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	line := fmt.Sprintf("stage=%s elapsed_ms=%d success=%t", stage, time.Since(started).Milliseconds(), err == nil)
	if stage == "npc" && err == nil {
		var decision turn.NPCDecision
		if invalid := turn.DecodeGeneratedJSON(response.Text, &decision, []string{"speech", "action_intent", "memory"}, []string{"speech", "action_intent", "silent", "memory"}); invalid != nil {
			line += fmt.Sprintf(" schema_error=%v", invalid)
		}
	}
	if stage == "coordination" || stage == "narration" {
		line += " output=" + response.Text
	}
	p.calls = append(p.calls, line)
	return response, err
}

func TestPacingRealModelComparison(t *testing.T) {
	path := os.Getenv("WIA_PACING_MODEL_CONFIG")
	if path == "" {
		t.Skip("set WIA_PACING_MODEL_CONFIG for real-model comparison")
	}
	provider, config, err := llm.NewProviderFromConfigFile(path)
	if err != nil {
		t.Fatal("configured provider could not be initialized")
	}
	if config.Provider == "fake" || config.Provider == "" {
		t.Fatal("real provider required")
	}
	generator, ok := provider.(model.TextGenerator)
	if !ok {
		t.Fatal("text generation unavailable")
	}
	t.Logf("provider=%s model=%s", config.Provider, config.Model)
	for _, baseline := range []bool{true, false} {
		name := "current"
		if baseline {
			name = "baseline"
		}
		t.Run(name, func(t *testing.T) {
			probe := &pacingProbe{provider: generator, baseline: baseline}
			app := newTestApp(t, probe)
			world, err := app.createFixtureWorld(context.Background(), "节奏对照", "guided", "旅人", "一位谨慎、礼貌的旅人。", true)
			if err != nil {
				t.Fatal(err)
			}
			inputs := []string{"跟老板打声招呼", "只是来歇脚，谢谢。", "我看看四周", "最近渡口有什么消息？", "我先听听事情经过，还没决定是否帮忙。"}
			for index, input := range inputs {
				started := time.Now()
				run, err := app.SubmitRun(context.Background(), world.WorldID, RunRequest{RequestKey: fmt.Sprintf("probe-%d", index), Input: input})
				if err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(310 * time.Second)
				for time.Now().Before(deadline) {
					run, err = app.Run(context.Background(), world.WorldID, run.RunID)
					if err != nil {
						t.Fatal(err)
					}
					if run.Status != "accepted" && run.Status != "running" {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				t.Logf("turn=%d input=%s status=%s reason=%s elapsed_ms=%d", index+1, input, run.Status, run.Reason, time.Since(started).Milliseconds())
				probe.mu.Lock()
				calls := append([]string(nil), probe.calls...)
				probe.calls = nil
				probe.mu.Unlock()
				for _, call := range calls {
					t.Log(call)
				}
				if run.Status != "completed" {
					t.Errorf("turn did not complete: %s", run.Reason)
				}
			}
		})
	}
}
