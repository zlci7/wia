package storyapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type packPlotGenerator struct {
	plotTestGenerator
	revision string
}

type closeoutModelProbe struct {
	inner model.TextGenerator
	t     *testing.T
}

func (g closeoutModelProbe) ModelWindow() model.WindowLimits {
	if p, ok := g.inner.(model.WindowProvider); ok {
		return p.ModelWindow()
	}
	return model.WindowLimits{}
}
func (g closeoutModelProbe) TextReasoningReserve() int {
	if p, ok := g.inner.(model.TextReasoningProvider); ok {
		return p.TextReasoningReserve()
	}
	return 0
}

func (g closeoutModelProbe) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	r, err := g.inner.GenerateText(ctx, req)
	if err == nil && strings.Contains(req.System, "结构化回合意图") {
		g.t.Logf("intent=%s", r.Text)
	}
	if err == nil && strings.Contains(req.System, "世界剧情协调器") {
		var result plotResolution
		if json.Unmarshal([]byte(r.Text), &result) == nil {
			g.t.Logf("plot status=%s source_ids=%v", result.Status, result.SourceIDs)
		}
	}
	if err == nil && strings.Contains(req.System, "场景协调 Agent") {
		var result hostResult
		if json.Unmarshal([]byte(r.Text), &result) == nil {
			g.t.Logf("coordination minutes=%d roster=%v scene_updates=%s", result.TimeMinutes, result.SceneCharacters, wire.MarshalJSON(result.SceneUpdates))
		}
	}
	return r, err
}

func (g *packPlotGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	r, err := g.plotTestGenerator.GenerateText(ctx, req)
	if strings.Contains(req.System, "世界剧情协调器") {
		r.Text = strings.ReplaceAll(r.Text, "definition:"+lanternPlotDefinition().Revision+":", "definition:"+g.revision+":")
	}
	return r, err
}

func createPackWorld(t *testing.T, a *App, game string) wiaworld.WorldSummary {
	t.Helper()
	p, err := a.Game(game)
	if err != nil {
		t.Fatal(err)
	}
	w, err := a.CreateStoryWorld(context.Background(), CreateWorldRequest{GameID: game, ExpectedRevision: p.Revision, RequestKey: t.Name(), Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestBundledGuidedRoutesClose(t *testing.T) {
	for _, participate := range []bool{false, true} {
		t.Run(fmt.Sprint(participate), func(t *testing.T) {
			g := &packPlotGenerator{plotTestGenerator: plotTestGenerator{intervene: participate}}
			a := newTestApp(t, g)
			g.revision = a.packs[GameID].Definition.Plot.Revision
			w := createPackWorld(t, a, GameID)
			for i := 0; i < 3; i++ {
				input := "留在客栈等待，不参与调查"
				if participate {
					input = "尝试护送信使到安全处"
				}
				r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: fmt.Sprint(i), Input: input})
				if err != nil {
					t.Fatal(err)
				}
				if r = waitRun(t, a, w.WorldID, r.RunID); r.Status != "completed" {
					t.Fatal(r)
				}
			}
			s := readContextSnapshot(t, a, w.WorldID)
			if !s.Summary.StoryEnded || s.PlotProgress.Ending == "" {
				t.Fatal("story did not close")
			}
			if participate && s.PlotProgress.Nodes["courier_window"].Status != "skipped" {
				t.Fatal("intervention ignored")
			}
		})
	}
}

func closeoutRealApp(t *testing.T) *App {
	t.Helper()
	if os.Getenv("WIA_M2_CLOSEOUT_REAL") != "1" {
		t.Skip("opt-in closeout experience")
	}
	p, cfg, err := llm.NewProviderFromConfigFile(os.Getenv("WIA_AUTONOMY_MODEL_CONFIG"))
	if err != nil || cfg.Provider == "fake" {
		t.Fatal("real provider unavailable")
	}
	g, ok := p.(model.TextGenerator)
	if !ok {
		t.Fatal("text generator unavailable")
	}
	t.Logf("provider=%s model=%s", cfg.Provider, cfg.Model)
	a := newTestApp(t, closeoutModelProbe{inner: g, t: t})
	keepRealUsageReport(t, a)
	a.logger = &recordingLogger{}
	return a
}

func realPackTurn(t *testing.T, a *App, world, input string, number int) turn.Snapshot {
	t.Helper()
	ctx := context.Background()
	start := time.Now()
	r, err := a.SubmitRun(ctx, world, RunRequest{RequestKey: fmt.Sprintf("experience-%d", number), Input: input})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		for deadline := time.Now().Add(310 * time.Second); time.Now().Before(deadline); {
			r, err = a.Run(ctx, world, r.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != "running" && r.Status != "accepted" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if r.Status != "failed" || attempt == 1 {
			break
		}
		t.Logf("explicit retry: turn=%d first_failure=%s elapsed_ms=%d", number, r.Reason, time.Since(start).Milliseconds())
		r, err = a.RetryRun(ctx, world, r.RunID, fmt.Sprintf("experience-retry-%d", number))
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("turn=%d status=%s reason=%s elapsed_ms=%d", number, r.Status, r.Reason, time.Since(start).Milliseconds())
	if r.Status != "completed" {
		if logger, ok := a.logger.(*recordingLogger); ok {
			for _, line := range strings.Split(logger.String(), "\n") {
				if strings.Contains(line, r.RunID) && (strings.Contains(line, "failed") || strings.Contains(line, "validation") || strings.Contains(line, "purpose=\"plot\"")) {
					t.Log(line)
				}
			}
		}
		t.Fatalf("turn incomplete: %+v", r)
	}
	s := readContextSnapshot(t, a, world)
	for _, m := range s.Messages {
		if m.RunID == r.RunID {
			t.Logf("message=%s", m.Content)
		}
	}
	t.Logf("clock=%s ending=%s", s.Summary.Clock, s.PlotProgress.Ending)
	return s
}

func TestM2RealGuidedRoutes(t *testing.T) {
	for _, route := range []string{"wait", "participate"} {
		t.Run(route, func(t *testing.T) {
			a := closeoutRealApp(t)
			w := createPackWorld(t, a, GameID)
			inputs := []string{}
			if route == "participate" {
				inputs = []string{"我去码头看看公告，询问搬运工有没有见过需要帮助的人。", "我沿栈道去仓棚，走进棚内，靠近有动静的麻袋堆，出声问有没有人需要帮助。", "我向棚里的人表明没有恶意，问他需要什么帮助；如果他愿意，我可以扶他到末班渡船，随身东西由他自己保管。", "我按照刚才得到的回应提供帮助。如果对方愿意上船，我扶他去码头；如果他拒绝，我留在附近不强迫。遇到拦阻先停下说明，不动手。", "我继续扶愿意登船的伤者走到码头，向船夫说明他伤了腿，请船夫协助上船。若有人追问，我说明只是帮伤者搭船，不交出他的随身东西，也不主动动手。", "我不再承担新的护送，沿来路回客栈，找安全的位置休息。"}
			}
			for i, input := range inputs {
				realPackTurn(t, a, w.WorldID, input, i)
			}
			for i := len(inputs); i < len(inputs)+6; i++ {
				s := realPackTurn(t, a, w.WorldID, "我留在当前安全的位置等待一个小时，不接受新任务。", i)
				if s.Summary.StoryEnded {
					return
				}
			}
			t.Fatal("ending not reached within the bounded route")
		})
	}
}

func TestM2RealWaitIntent(t *testing.T) {
	a := closeoutRealApp(t)
	w := createPackWorld(t, a, "orbital-repair")
	s := readContextSnapshot(t, a, w.WorldID)
	s.SceneViews[0].Content = "你在设备检修间，工程师正在核对一把已隔离的扳手，未安排你操作设备。"
	for i := range s.Characters {
		s.Characters[i].InScene = s.Characters[i].EntityID == "npc:innkeeper"
	}
	for i := 0; i < 2; i++ {
		_, _, err := a.turnService().ResolveTurnIntent(context.Background(), a.generator, s, wiaworld.Run{RunID: fmt.Sprint("wait-probe", i), Input: "我在安全位置等待半小时，不参加维修，也不替任何人签字。"})
		if err != nil {
			t.Error(err)
		}
	}
}
