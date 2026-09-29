package storyapp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

// Disposable real-model evaluation; narrative quality is reviewed separately.
func TestAutonomyRealModelSequence(t *testing.T) {
	runRealStorySequence(t, []string{"我走进客栈，朝老板点头问好。", "我沉默片刻，选择不回复。", "我继续安静地观察，不打断他们各自的事情。"}, true)
}

func TestContinuityRealModelSequence(t *testing.T) {
	runRealStorySequence(t, []string{"进门看看环境", "想找你问问，最近有没有发生什么怪事", "我沉默片刻，暂时不回复"}, false)
}

func TestPlotRealModelSequence(t *testing.T) {
	runRealStorySequence(t, []string{"我坐在客栈里静静等一个小时，遇到新的动静先停下来看看，不替我接受任务。", "我决定暂时不参加调查，继续在客栈等待一个小时，遇到新情况就停下。", "我仍不参与，留在客栈休息，等到夜渡离岸。"}, false)
}

func TestOpenWaitingRealModelSequence(t *testing.T) {
	runRealStorySequence(t, []string{"我留在客栈等待一个小时，不参加调查；新动静发生时先停下。", "我不介入，继续在客栈等待一个小时，遇到新情况再停下。", "我继续留在客栈，等到末班夜渡离岸。", "我朝老板点头道谢，问她现在还有没有热茶。"}, false, "open")
}

func TestInterventionRealModelSequence(t *testing.T) {
	runRealStorySequence(t, []string{"我等待下一次渡船的提醒，遇到新动静就停下。", "我离开客栈，走到码头仓棚门外，看看是否真有人需要帮助。", "我表明愿意帮忙；如果负伤的人愿意随我走，就护送他经侧门到渡船，同时请他自己保管证据。不要替他决定是否接受。", "我在安全处等待半小时，出现必须由我处理的危险就停下。", "我留在安全处等到末班夜渡离岸。", "我返回客栈，向老板问好。"}, false, "open")
}

func runRealStorySequence(t *testing.T, inputs []string, custom bool, modes ...string) {
	path := os.Getenv("WIA_AUTONOMY_MODEL_CONFIG")
	if path == "" {
		t.Skip("opt-in real-model evaluation")
	}
	provider, config, err := llm.NewProviderFromConfigFile(path)
	if err != nil {
		t.Fatal("real provider configuration unavailable")
	}
	g, ok := provider.(model.TextGenerator)
	if !ok || config.Provider == "fake" {
		t.Fatal("real text model required")
	}
	a := newTestApp(t, g)
	logger := &recordingLogger{}
	a.logger = logger
	ctx := context.Background()
	mode := "guided"
	if len(modes) > 0 {
		mode = modes[0]
	}
	w, err := a.createFixtureWorld(ctx, "自主性评估", mode, "旅人", "谨慎而礼貌的旅人", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("provider=%s model=%s", config.Provider, config.Model)
	for i, input := range inputs {
		if custom && i == 2 {
			s := readContextSnapshot(t, a, w.WorldID)
			p := wiaworld.BehaviorPolicies{NPC: "依据自己的动机和眼前机会处理事务；可以保持沉默，不为活跃气氛强行插话。已经完成的行动保持完成。", Narration: "简洁呈现本轮新增内容，以有意义的动作或对白收尾。"}
			if _, _, err = a.UpdateNarrativeSettings(ctx, w.WorldID, policyRequest(s.Summary.ContextEpoch, p)); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now()
		r, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: fmt.Sprint(i), Input: input})
		if err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(310 * time.Second); time.Now().Before(deadline); {
			r, err = a.Run(ctx, w.WorldID, r.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != "accepted" && r.Status != "running" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("turn=%d status=%s reason=%s elapsed_ms=%d", i+1, r.Status, r.Reason, time.Since(start).Milliseconds())
		if r.Status == "failed" && os.Getenv("WIA_REAL_RETRY") == "1" {
			for _, line := range strings.Split(logger.String(), "\n") {
				if strings.Contains(line, r.RunID) && strings.Contains(line, "validation failed") {
					t.Log(line)
				}
			}
			r, err = a.RetryRun(ctx, w.WorldID, r.RunID, fmt.Sprintf("explicit-retry-%d", i))
			if err != nil {
				t.Fatal(err)
			}
			retryStarted := time.Now()
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
			t.Logf("turn=%d explicit_retry=1 status=%s reason=%s elapsed_ms=%d", i+1, r.Status, r.Reason, time.Since(retryStarted).Milliseconds())
		}
		for _, line := range strings.Split(logger.String(), "\n") {
			if strings.Contains(line, r.RunID) && (strings.Contains(line, "model call finished") || strings.Contains(line, "context output budget")) {
				t.Log(line)
			}
		}
		if r.Status != "completed" {
			for _, line := range strings.Split(logger.String(), "\n") {
				if strings.Contains(line, r.RunID) && (strings.Contains(line, "story JSON validation:") || strings.Contains(line, "validation failed") || strings.Contains(line, "rejected:") || strings.Contains(line, "turn failed")) {
					t.Log(line)
				}
			}
			t.Fatal("incomplete real-model turn")
		}
		s := readContextSnapshot(t, a, w.WorldID)
		t.Logf("clock=%s plot_nodes=%d story_ended=%t", s.Summary.Clock, len(s.PlotProgress.Nodes), s.Summary.StoryEnded)
		for id, state := range s.PlotProgress.Nodes {
			t.Logf("node=%s status=%s", id, state.Status)
		}
		for _, e := range s.Events {
			if e.RunID == r.RunID && (e.EventType == "npc_action_intent" || e.EventType == "npc_action_result" || e.EventType == "npc_dialogue") {
				t.Logf("event=%s actor=%s stage=%d type=%s content=%s", e.EventType, e.ActorID, e.Stage, e.SourceType, e.Content)
			}
		}
		for _, m := range s.Messages {
			if m.RunID == r.RunID && m.Kind == "narrative" {
				t.Logf("narrative=%s", m.Content)
			}
		}
	}
}
