package storyapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type plotTestGenerator struct {
	mu            sync.Mutex
	requests      []model.TextRequest
	failNarration bool
	wake          bool
	deferNode     bool
	intervene     bool
}

func (g *plotTestGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	g.mu.Unlock()
	if strings.Contains(req.System, "结构化回合意图") {
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"","visibility":"public"}`}, nil
	}
	if strings.Contains(req.System, "重要 NPC") {
		if strings.Contains(req.Input, "阶段：5") {
			return model.TextResponse{Text: `{"speech":"我会查看门闩。","action_intent":"查看自己身边的门闩","silent":false,"memory":"听到自己获准的铃声"}`}, nil
		}
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":""}`}, nil
	}
	if strings.Contains(req.System, "场景协调 Agent") {
		var minutes int
		tail := strings.SplitN(req.Input, "世界剧情时间边界：本轮 time_minutes 最大为 ", 2)
		if len(tail) == 2 {
			fmt.Sscanf(tail[1], "%d", &minutes)
		}
		ids := []string{}
		tail = strings.SplitN(req.Input, "当前在场人物 entity_id：", 2)
		if len(tail) == 2 {
			value := strings.SplitN(tail[1], "\n", 2)[0]
			if value != "" {
				ids = strings.Split(value, ",")
			}
		}
		var candidates []wiaworld.Event
		raw := strings.SplitN(strings.SplitN(req.Input, "待裁定行动(JSON)：", 2)[1], "\n", 2)[0]
		if err := json.Unmarshal([]byte(raw), &candidates); err != nil {
			return model.TextResponse{}, err
		}
		outcomes := []hostActionResult{}
		for _, candidate := range candidates {
			content, status := "你等待到下一次铃声响起，尚未经过一个小时。", "partial"
			if g.intervene {
				content, status = "你将受伤信使护送至安全处，证据交由本人保管。", "succeeded"
			}
			outcomes = append(outcomes, hostActionResult{ActionID: candidate.EventID, Status: status, Content: content, Recipients: []string{"player"}})
		}
		return model.TextResponse{Text: wire.MarshalJSON(hostResult{TimeMinutes: minutes, Scene: "旧渡口客栈", SceneCharacters: ids, Outcomes: outcomes, SceneUpdates: []sceneUpdate{}})}, nil
	}
	if strings.Contains(req.System, "世界剧情协调器") {
		var node plot.Node
		raw := strings.SplitN(strings.SplitN(req.Input, "当前节点：", 2)[1], "\n", 2)[0]
		if err := json.Unmarshal([]byte(raw), &node); err != nil {
			return model.TextResponse{}, err
		}
		definitionRef := strings.SplitN(strings.SplitN(req.Input, "或 definition:", 2)[1], "；", 2)[0]
		result := plotResolution{Status: "occurred", Content: "作者隐藏事实：信使去向", SourceIDs: []string{"definition:" + definitionRef}, Projections: []plotProjection{{Recipient: "player", Content: "你听见码头铃声。"}}, DecisionRequests: []string{}}
		result.Projections[0].Scene = "你仍在客栈，刚听见码头铃声。"
		if g.intervene && node.ID == "courier_window" {
			var events []wiaworld.Event
			data := strings.SplitN(strings.SplitN(req.Input, "本轮已确认记录：", 2)[1], "\n", 2)[0]
			if err := json.Unmarshal([]byte(data), &events); err != nil {
				return model.TextResponse{}, err
			}
			found := false
			for _, e := range events {
				if e.EventType == "player_action_result" && e.SourceType == "action_succeeded" {
					result.SourceIDs = []string{e.EventID}
					found = true
				}
			}
			if !found {
				return model.TextResponse{}, errors.New("player intervention has no resolved evidence")
			}
			result.Status, result.Content = "skipped", "信使已被转移，搜寻者未能找到信使。"
		}
		if node.Terminal {
			result.Ending = "当夜机会结束"
		}
		if g.deferNode {
			result.Status = "deferred"
			result.Projections = []plotProjection{}
			result.DecisionRequests = []string{}
			result.Ending = ""
		}
		if g.wake {
			result.Projections = append(result.Projections, plotProjection{Recipient: "npc:mercenary", Content: "你所在位置能听见铃声。"})
			result.DecisionRequests = []string{"npc:mercenary"}
		}
		return model.TextResponse{Text: wire.MarshalJSON(result)}, nil
	}
	if strings.Contains(req.System, "世界剧情行动协调器") {
		var records []wiaworld.Event
		raw := strings.SplitN(strings.SplitN(req.Input, "待处理NPC记录：", 2)[1], "\n", 2)[0]
		if err := json.Unmarshal([]byte(raw), &records); err != nil {
			return model.TextResponse{}, err
		}
		outcomes := []hostActionResult{}
		for _, e := range records {
			if e.EventType == "npc_action_intent" {
				outcomes = append(outcomes, hostActionResult{ActionID: e.EventID, Status: "succeeded", Content: "门闩已经查看，锁扣完好。", Recipients: []string{e.ActorID}})
			}
		}
		updates := []sceneUpdate{}
		for _, o := range outcomes {
			updates = append(updates, sceneUpdate{Content: o.Content, SourceIDs: []string{o.ActionID}, Recipients: o.Recipients})
		}
		return model.TextResponse{Text: wire.MarshalJSON(map[string]any{"outcomes": outcomes, "scene_updates": updates})}, nil
	}
	if strings.Contains(req.System, "玩家正文 Agent") {
		if g.failNarration {
			return model.TextResponse{}, errors.New("fixture narration failure")
		}
		return model.TextResponse{Text: "你听见渡口的铃声，雨还在下。"}, nil
	}
	return model.TextResponse{}, errors.New("unexpected plot fixture purpose")
}

func TestPlotInterventionUsesResolvedPlayerEvidence(t *testing.T) {
	app := newTestApp(t, &plotTestGenerator{intervene: true})
	w, err := app.createFixtureWorld(context.Background(), "干预", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r, err := app.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: fmt.Sprint(i), Input: "我尝试把受伤信使带到安全处"})
		if err != nil {
			t.Fatal(err)
		}
		if done := waitRun(t, app, w.WorldID, r.RunID); done.Status != "completed" {
			t.Fatal(done)
		}
	}
	s := readContextSnapshot(t, app, w.WorldID)
	n := s.PlotProgress.Nodes["courier_window"]
	if n.Status != "skipped" || len(n.Evidence) != 1 {
		t.Fatalf("intervention=%+v", n)
	}
	e, ok := turn.EventByID(s.Events, n.Evidence[0])
	if !ok || e.EventType != "player_action_result" || e.SourceType != "action_succeeded" {
		t.Fatal("node used attempt instead of resolved result")
	}
}

func TestPlotTimelineCommitModesAndIdempotency(t *testing.T) {
	for _, mode := range []string{"open", "guided"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			app := newTestApp(t, &plotTestGenerator{})
			world, err := app.createFixtureWorld(ctx, "时间线", mode, "旅人", "", true)
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []string{"第 1 日 19:05", "第 1 日 19:30", "第 1 日 20:00"} {
				req := RunRequest{RequestKey: fmt.Sprintf("wait-%d", i), Input: "我在原地等待一个小时。"}
				run, err := app.SubmitRun(ctx, world.WorldID, req)
				if err != nil {
					t.Fatal(err)
				}
				if done := waitRun(t, app, world.WorldID, run.RunID); done.Status != "completed" {
					t.Fatalf("run=%+v", done)
				}
				repeated, err := app.SubmitRun(ctx, world.WorldID, req)
				if err != nil || repeated.RunID != run.RunID {
					t.Fatalf("repeat=%+v %v", repeated, err)
				}
				snap, err := app.ReadWorld(ctx, world.WorldID, 100)
				if err != nil {
					t.Fatal(err)
				}
				if snap.Summary.Clock != want || len(snap.PlotProgress.Nodes) != i+1 {
					t.Fatalf("snapshot=%+v plot=%+v", snap.Summary, snap.PlotProgress)
				}
			}
			snap, _ := app.ReadWorld(ctx, world.WorldID, 100)
			if snap.Summary.StoryEnded != (mode == "guided") {
				t.Fatalf("ending=%+v", snap.Summary)
			}
			run, err := app.SubmitRun(ctx, world.WorldID, RunRequest{RequestKey: "after", Input: "看看四周"})
			if mode == "guided" {
				if !errors.Is(err, ErrStoryEnded) {
					t.Fatalf("ended admission=%v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if done := waitRun(t, app, world.WorldID, run.RunID); done.Status != "completed" {
					t.Fatal(done)
				}
			}
		})
	}
}

func TestPlotNarrationFailureRollsBackClockAndProgress(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &plotTestGenerator{failNarration: true})
	w, err := app.createFixtureWorld(ctx, "失败边界", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	run, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "fail", Input: "等待"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, app, w.WorldID, run.RunID); done.Status != "failed" {
		t.Fatal(done)
	}
	snap, err := app.ReadWorld(ctx, w.WorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Summary.Clock != "第 1 日 19:00" || len(snap.PlotProgress.Nodes) != 0 || snap.Summary.EventHead != 1 {
		t.Fatalf("partial commit: %+v", snap)
	}
}

func TestPlotOffSceneDecisionAndPlayerProjection(t *testing.T) {
	ctx := context.Background()
	g := &plotTestGenerator{wake: true}
	app := newTestApp(t, g)
	w, err := app.createFixtureWorld(ctx, "场外", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := app.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	if _, err = store.Database().Exec(`UPDATE characters SET in_scene=0 WHERE entity_id='npc:mercenary'`); err != nil {
		t.Fatal(err)
	}
	run, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "outside", Input: "我保密地等待"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, app, w.WorldID, run.RunID); done.Status != "completed" {
		t.Fatal(done)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	woken := false
	for _, req := range g.requests {
		if strings.Contains(req.System, "玩家正文 Agent") && (strings.Contains(req.Input, "作者隐藏事实") || strings.Contains(req.Input, "我会查看门闩") || strings.Contains(req.Input, "锁扣完好")) {
			t.Fatal("offscene information in narration")
		}
		if strings.Contains(req.System, "重要 NPC") && strings.Contains(req.Input, "阶段：5") {
			woken = true
			if strings.Contains(req.Input, "玩家本轮表达中的行动与等待尚待场景协调") {
				t.Fatal("post-plot decision reverted resolved actions to pending")
			}
			if strings.Contains(req.Input, "作者隐藏事实") || strings.Contains(req.Input, "我保密地等待") {
				t.Fatal("offscene received unauthorized input")
			}
		}
	}
	if !woken {
		t.Fatal("offscene NPC never decided")
	}
	ps, err := app.ReadPerceptions(ctx, w.WorldID, "npc:mercenary")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) < 2 {
		t.Fatalf("missing observation/result: %+v", ps)
	}
	snapshot, err := app.ReadWorld(ctx, w.WorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if turn.SceneFor(snapshot, "npc:mercenary") != "门闩已经查看，锁扣完好。" || turn.SceneFor(snapshot, "player") != "你仍在客栈，刚听见码头铃声。" {
		t.Fatal("plot stage state did not reach recipient views")
	}
	if snapshot.SceneVersion < 3 {
		t.Fatal("scene version did not advance")
	}
	history, err := store.LoadDialogue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range history {
		if strings.Contains(event.Content, "我会查看门闩") {
			t.Fatal("offscene speech leaked through future intent history")
		}
	}
}

func TestPlotCopyAndLegacyReadDoNotAdvanceOrInject(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &plotTestGenerator{})
	w, err := app.createFixtureWorld(ctx, "原存档", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	run, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "first", Input: "等待"})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, app, w.WorldID, run.RunID)
	op, err := app.SaveAs(ctx, w.WorldID, "副本", "copy", 1)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := app.ReadWorld(ctx, op.TargetWorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if copy.Summary.Clock != "第 1 日 19:05" || len(copy.PlotProgress.Nodes) != 1 {
		t.Fatalf("bad copy: %+v", copy)
	}
	if turn.SceneFor(copy, "player") != "你仍在客栈，刚听见码头铃声。" || copy.SceneVersion < 2 {
		t.Fatal("copy lost the plot's final scene view")
	}
	path, _, _ := app.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Database().Exec(`DELETE FROM meta WHERE key IN ('plot_definition','plot_progress')`)
	store.Database().Close()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := app.ReadWorld(ctx, w.WorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Plot != nil || legacy.Summary.Clock != "第 1 日 19:05" {
		t.Fatal("legacy changed")
	}
}

func TestPlotDeferredChecksAgainWithoutSkippingTimeBoundary(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &plotTestGenerator{deferNode: true})
	w, err := app.createFixtureWorld(ctx, "未满足条件", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"第 1 日 19:05", "第 1 日 19:06"} {
		run, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: fmt.Sprint(i), Input: "我等一小时"})
		if err != nil {
			t.Fatal(err)
		}
		if done := waitRun(t, app, w.WorldID, run.RunID); done.Status != "completed" {
			t.Fatal(done)
		}
		s, err := app.ReadWorld(ctx, w.WorldID, 100)
		if err != nil {
			t.Fatal(err)
		}
		if s.Summary.Clock != want || len(s.PlotProgress.Nodes) != 1 || s.PlotProgress.Nodes["dock_warning"].Status != "deferred" {
			t.Fatalf("invalid deferred state %+v", s.PlotProgress)
		}
		for _, e := range s.Events {
			if e.EventType == "plot_result" {
				t.Fatal("deferred event treated as fact")
			}
		}
	}
}

func TestPlotRestartAndCopyRemainIndependent(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &plotTestGenerator{})
	w, err := app.createFixtureWorld(ctx, "重启前", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	run, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "first", Input: "等待"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, app, w.WorldID, run.RunID); done.Status != "completed" {
		t.Fatal(done)
	}
	op, err := app.SaveAs(ctx, w.WorldID, "独立分支", "branch", 1)
	if err != nil {
		t.Fatal(err)
	}
	root := app.DataRoot()
	if err = app.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: &plotTestGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, id := range []string{w.WorldID, op.TargetWorldID} {
		s, err := reopened.ReadWorld(ctx, id, 100)
		if err != nil {
			t.Fatal(err)
		}
		if s.Summary.Clock != "第 1 日 19:05" || len(s.PlotProgress.Nodes) != 1 {
			t.Fatal("restart advanced clock or lost node")
		}
		if turn.SceneFor(s, "player") != "你仍在客栈，刚听见码头铃声。" {
			t.Fatal("restart lost the final plot scene")
		}
	}
	if _, err = reopened.ActivateWorld(ctx, op.TargetWorldID, 1, "switch-branch"); err != nil {
		t.Fatal(err)
	}
	run, err = reopened.SubmitRun(ctx, op.TargetWorldID, RunRequest{RequestKey: "second", Input: "继续等待"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, reopened, op.TargetWorldID, run.RunID); done.Status != "completed" {
		t.Fatal(done)
	}
	original, _ := reopened.ReadWorld(ctx, w.WorldID, 100)
	branch, _ := reopened.ReadWorld(ctx, op.TargetWorldID, 100)
	if len(original.PlotProgress.Nodes) != 1 || len(branch.PlotProgress.Nodes) != 2 {
		t.Fatal("copy state crossed worlds")
	}
}
