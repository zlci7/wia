package storyapp

import (
	"context"
	"encoding/json"
	"fmt"
	"gameagent/runtime/internal/model"
	"strings"
	"testing"
	"time"
)

type plotPresenceGenerator struct {
	base    plotTestGenerator
	present bool
}

func (g *plotPresenceGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	r, err := g.base.GenerateText(ctx, req)
	if err != nil || !strings.Contains(req.System, "世界剧情行动协调器") {
		return r, err
	}
	var result struct {
		Outcomes []plotActionResult `json:"outcomes"`
		Updates  []sceneUpdate      `json:"scene_updates"`
	}
	if err = json.Unmarshal([]byte(r.Text), &result); err != nil {
		return r, err
	}
	for i := range result.Outcomes {
		p := g.present
		result.Outcomes[i].ActorInScene = &p
	}
	r.Text = marshalJSON(result)
	return r, nil
}

func TestPlotActionPresencePersistsAndControlsNextRound(t *testing.T) {
	ctx := context.Background()
	g := &plotPresenceGenerator{base: plotTestGenerator{wake: true}}
	app := newTestApp(t, g)
	w, err := app.createFixtureWorld(ctx, "进出场", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for i, present := range []bool{false, true} {
		g.present = present
		g.base.mu.Lock()
		requestStart := len(g.base.requests)
		g.base.mu.Unlock()
		r, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: fmt.Sprint(i), Input: "等候"})
		if err != nil {
			t.Fatal(err)
		}
		if done := waitRun(t, app, w.WorldID, r.RunID); done.Status != "completed" {
			t.Fatal(done)
		}
		s := readContextSnapshot(t, app, w.WorldID)
		for _, c := range s.Characters {
			if c.EntityID == "npc:mercenary" && c.InScene != present {
				t.Fatalf("presence=%t want=%t", c.InScene, present)
			}
		}
		if i == 1 {
			g.base.mu.Lock()
			for _, req := range g.base.requests[requestStart:] {
				if strings.Contains(req.System, "重要 NPC") && strings.Contains(req.Input, "阶段：1") && strings.Contains(req.Input, "你的身份：铁杉") {
					g.base.mu.Unlock()
					t.Fatal("departed NPC was scheduled at stage 1")
				}
			}
			g.base.mu.Unlock()
		}
	}
	status, _ := app.Status(ctx)
	op, err := app.SaveAs(ctx, w.WorldID, "进出场分支", "presence-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); op.Status != "ready" && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		op, err = app.CopyOperation(ctx, op.OperationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if op.Status != "ready" {
		t.Fatal(op)
	}
	root := app.dataRoot
	if err = app.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: root, Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, id := range []string{w.WorldID, op.TargetWorldID} {
		s := readContextSnapshot(t, reopened, id)
		found := false
		for _, c := range s.Characters {
			if c.EntityID == "npc:mercenary" {
				found = c.InScene
			}
		}
		if !found {
			t.Fatal("presence lost after copy/restart")
		}
	}
}

func TestPlotPresenceRejectsUnresolvedMovement(t *testing.T) {
	p := false
	events := []Event{{EventID: "a", ActorID: "npc:a", EventType: "npc_action_intent"}}
	for _, status := range []string{"failed", "not_executed"} {
		if _, err := plotActionPresence([]string{"npc:a"}, events, []plotActionResult{{hostActionResult: hostActionResult{ActionID: "a", Status: status}, ActorInScene: &p}}); err == nil {
			t.Fatal("unresolved movement accepted")
		}
	}
}

type actionConsistencyGenerator struct{ status string }

func (g actionConsistencyGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	switch {
	case strings.Contains(req.System, "结构化回合意图"):
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"","visibility":"public"}`}, nil
	case strings.Contains(req.System, "重要 NPC"):
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":""}`}, nil
	case strings.Contains(req.System, "场景协调 Agent"):
		var candidates []Event
		raw := strings.SplitN(strings.SplitN(req.Input, "待裁定行动(JSON)：", 2)[1], "\n", 2)[0]
		if err := json.Unmarshal([]byte(raw), &candidates); err != nil {
			return model.TextResponse{}, err
		}
		outcomes := []hostActionResult{}
		for _, e := range candidates {
			outcomes = append(outcomes, hostActionResult{ActionID: e.EventID, Status: g.status, Content: "已裁定的设备检查结果", Recipients: []string{"player", "npc:innkeeper"}})
		}
		return model.TextResponse{Text: marshalJSON(hostResult{TimeMinutes: 1, Scene: "值班室", SceneCharacters: []string{"npc:innkeeper", "npc:mercenary"}, Outcomes: outcomes, SceneUpdates: []sceneUpdate{}})}, nil
	default:
		return model.TextResponse{Text: "你检查了面板。"}, nil
	}
}

func TestPackWithoutPlotResolvesPlayerActions(t *testing.T) {
	for _, status := range []string{"succeeded", "failed"} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			app := newTestApp(t, actionConsistencyGenerator{status})
			w, err := app.CreateStoryWorld(ctx, CreateWorldRequest{GameID: "orbital-repair", ExpectedRevision: "orbital-repair.pack.v1", RequestKey: "action", Activate: true})
			if err != nil {
				t.Fatal(err)
			}
			r, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "inspect", Input: "检查面板"})
			if err != nil {
				t.Fatal(err)
			}
			if done := waitRun(t, app, w.WorldID, r.RunID); done.Status != "completed" {
				t.Fatal(done)
			}
			s := readContextSnapshot(t, app, w.WorldID)
			if s.Plot != nil {
				t.Fatal("fixture must not have a plot")
			}
			found := false
			for _, e := range s.Events {
				if e.EventType == "player_action_result" {
					found = true
					if e.SourceType != "action_"+status {
						t.Fatal(e)
					}
				}
			}
			if !found {
				t.Fatal("player action was never resolved")
			}
			found = false
			for _, p := range s.Perceptions["npc:innkeeper"] {
				if p.Content == "已裁定的设备检查结果" {
					found = true
				}
			}
			if !found {
				t.Fatal("witness did not receive resolved result")
			}
		})
	}
}
