package storyapp

import (
	"context"
	"encoding/json"
	"errors"
	"gameagent/runtime/internal/model"
	"strings"
	"testing"
)

type overlappingActionGenerator struct{ scriptedGenerator }

func (g *overlappingActionGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(req.System, "重要 NPC") {
		if strings.Contains(req.Input, "你的身份：沈岚") {
			if strings.Contains(req.Input, "阶段：2") {
				if !strings.Contains(req.Input, "尚未执行、待场景协调") || !strings.Contains(req.Input, "已进入本轮交谈") {
					return model.TextResponse{}, errors.New("stage state missing")
				}
				return model.TextResponse{Text: `{"speech":"热水在这里。","action_intent":"给玩家的碗续满热水","silent":false,"memory":"我准备提供热水"}`}, nil
			}
			return model.TextResponse{Text: `{"speech":"要热水吗？","action_intent":"端一碗热水放在玩家近旁","silent":false,"memory":"我准备端水"}`}, nil
		}
		if strings.Contains(req.Input, "阶段：1") {
			return model.TextResponse{Text: `{"speech":"外面风大。","action_intent":"","silent":false,"memory":"我看见来客"}`}, nil
		}
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":"我听见老板问话"}`}, nil
	}
	if strings.Contains(req.System, "场景协调 Agent") {
		start := strings.Index(req.Input, "待裁定行动(JSON)：") + len("待裁定行动(JSON)：")
		end := strings.Index(req.Input, "\n所有可用重要人物：")
		var actions []Event
		if start < 0 || end < start || json.Unmarshal([]byte(req.Input[start:end]), &actions) != nil || len(actions) != 2 {
			return model.TextResponse{}, errors.New("missing action sources")
		}
		out := hostResult{Scene: "客栈", SceneCharacters: []string{"npc:innkeeper", "npc:mercenary"}, SceneUpdates: []sceneUpdate{}, Outcomes: []hostActionResult{
			{ActionID: actions[0].EventID, Status: "succeeded", Content: "沈岚把一碗热水放在桌上。", Recipients: []string{"player", "npc:mercenary"}},
			{ActionID: actions[1].EventID, Status: "not_executed", Content: "热水刚放下，没有消耗，无须另行续水。", Recipients: []string{"player"}},
		}}
		data, _ := json.Marshal(out)
		return model.TextResponse{Text: string(data)}, nil
	}
	if strings.Contains(req.System, "玩家正文 Agent") {
		if !strings.Contains(req.Input, `"outcome_status":"not_executed"`) {
			return model.TextResponse{}, errors.New("lost outcome status")
		}
		return model.TextResponse{Text: "沈岚把热水放在桌上。"}, nil
	}
	return g.scriptedGenerator.GenerateText(ctx, req)
}

func TestPendingActionsAndNotExecutedResultsSurviveAtomicTurn(t *testing.T) {
	a := newTestApp(t, &overlappingActionGenerator{})
	ctx := context.Background()
	w, _ := a.createFixtureWorld(ctx, "衔接", "guided", "旅人", "", true)
	r, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "overlap", Input: "最近有什么怪事？"})
	if err != nil {
		t.Fatal(err)
	}
	r = waitRun(t, a, w.WorldID, r.RunID)
	if r.Status != "completed" {
		t.Fatalf("%+v", r)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	actions, results, skipped := 0, 0, 0
	for _, e := range s.Events {
		switch e.EventType {
		case "npc_action_intent":
			actions++
		case "npc_action_result":
			results++
			if e.SourceType == "action_not_executed" {
				skipped++
			}
		}
	}
	if actions != 2 || results != 2 || skipped != 1 {
		t.Fatalf("actions=%d results=%d skipped=%d", actions, results, skipped)
	}
	for _, p := range s.Perceptions["npc:mercenary"] {
		if p.SourceType == "action_not_executed" {
			t.Fatal("private result leaked")
		}
	}
	var observed bool
	for _, p := range s.Perceptions["npc:innkeeper"] {
		if p.SourceType == "action_not_executed" {
			observed = true
		}
	}
	if !observed {
		t.Fatal("actor lost own result")
	}
}

func TestNotExecutedStillRequiresEveryActionAndValidRecipients(t *testing.T) {
	run := Run{RunID: "r"}
	actor := lanternDefinition().Characters[0]
	out := turnOutput{Events: []Event{{EventID: "action", ActorID: actor.EntityID, EventType: "npc_action_intent", RunID: "r", Stage: 1}}}
	if _, err := appendHostOutcomes(&out, run, []Character{actor}, nil); err == nil {
		t.Fatal("missing outcome accepted")
	}
	if _, err := appendHostOutcomes(&out, run, []Character{actor}, []hostActionResult{{ActionID: "action", Status: "not_executed", Content: "重复", Recipients: []string{"foreign"}}}); err == nil {
		t.Fatal("unknown recipient accepted")
	}
}
