package storyapp

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
		req.System = strings.ReplaceAll(req.System, "\n叙事节奏规则："+narrativePacingInstruction(), "")
		rule, _ := narrativeLengthInstruction(NarrativeSettings{Length: NarrativeLengthStandard})
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
		var decision npcDecision
		if invalid := decodeGeneratedJSON(response.Text, &decision, []string{"speech", "action_intent", "memory"}, []string{"speech", "action_intent", "silent", "memory"}); invalid != nil {
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

func TestPacingCommittedTeaReplay(t *testing.T) {
	path := os.Getenv("WIA_PACING_MODEL_CONFIG")
	if path == "" {
		t.Skip("real-model continuity replay")
	}
	provider, config, err := llm.NewProviderFromConfigFile(path)
	if err != nil || config.Provider == "fake" || config.Provider == "" {
		t.Fatal("real provider unavailable")
	}
	generator, ok := provider.(model.TextGenerator)
	if !ok {
		t.Fatal("text generation unavailable")
	}
	app := newTestApp(t, generator)
	world, err := app.createFixtureWorld(context.Background(), "递茶连续性样本", "guided", "旅人", "礼貌的旅人", true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := app.ReadWorld(context.Background(), world.WorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Summary.Scene = "旧渡口客栈内。旅人已经进堂。沈岚从柜台后取碗斟茶，把碗放到门边桌角。铁杉仍坐在窗边。"
	snapshot.Messages = []Message{{Kind: "narrative", Content: "你已进入客栈并扶正灯架。沈岚把热茶放在门边桌角，站在桌旁：‘先坐下歇歇。’"}}
	snapshot.Events = []Event{{Seq: 5, EventID: "prior-tea", EventType: "npc_action_result", ActorID: "npc:innkeeper", Content: "沈岚已把热茶放到门边桌角，现在站在桌边。"}}
	for i := range snapshot.SceneViews {
		snapshot.SceneViews[i].Content = "旧渡口客栈。沈岚已把热茶放到门边桌角，现在站在桌边。"
		snapshot.SceneViews[i].SourceIDs = []string{"prior-tea"}
	}
	run := Run{RunID: "replay", Input: "我走进去跟老板打了声招呼，观测起四周"}
	speech := "晚上好，茶刚沏好，趁热。"
	decisions := map[string]npcDecision{"npc:innkeeper": {Speech: speech}, "npc:mercenary": {Silent: true}}
	events := []Event{{EventID: "current-greeting", RunID: "replay", Stage: 1, EventType: "npc_dialogue", ActorID: "npc:innkeeper", Content: speech}, {EventID: "current-input", RunID: "replay", Stage: 1, EventType: "player_attempt", ActorID: "player", Content: run.Input}}
	for _, baseline := range []bool{true, false} {
		name := "current"
		if baseline {
			name = "baseline"
		}
		t.Run(name, func(t *testing.T) {
			probe := &pacingProbe{provider: generator, baseline: baseline}
			host, _, err := app.coordinateTurn(context.Background(), probe, snapshot, run, turnIntent{IntentType: "speak", AddresseeID: "npc:innkeeper", Visibility: "public"}, decisions, events, speech)
			if err != nil {
				t.Fatal("coordination replay failed")
			}
			narrative, _, err := app.narrateVisible(context.Background(), probe, snapshot, run, lanternDefinition(), "npc:innkeeper", "speak", events, false, snapshot.Summary.Clock, host.Scene, host.SceneCharacters)
			if err != nil {
				t.Fatal("narration replay failed")
			}
			t.Logf("scene=%s\nnarrative=%s", host.Scene, narrative.Narrative)
		})
	}
}
