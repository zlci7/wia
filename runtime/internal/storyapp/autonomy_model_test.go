package storyapp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gameagent/runtime/internal/llm"
	"gameagent/runtime/internal/model"
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

func runRealStorySequence(t *testing.T, inputs []string, custom bool) {
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
	w, err := a.CreateWorld(ctx, "自主性评估", "guided", "旅人", "谨慎而礼貌的旅人", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("provider=%s model=%s", config.Provider, config.Model)
	for i, input := range inputs {
		if custom && i == 2 {
			s := readContextSnapshot(t, a, w.WorldID)
			p := BehaviorPolicies{NPC: "依据自己的动机和眼前机会处理事务；可以保持沉默，不为活跃气氛强行插话。已经完成的行动保持完成。", Narration: "简洁呈现本轮新增内容，以有意义的动作或对白收尾。"}
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
		for _, line := range strings.Split(logger.String(), "\n") {
			if strings.Contains(line, r.RunID) && (strings.Contains(line, "model call finished") || strings.Contains(line, "context output budget")) {
				t.Log(line)
			}
		}
		if r.Status != "completed" {
			for _, line := range strings.Split(logger.String(), "\n") {
				if strings.Contains(line, r.RunID) && (strings.Contains(line, "validation failed") || strings.Contains(line, "rejected:") || strings.Contains(line, "turn failed")) {
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
