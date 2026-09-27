package storyapp

import (
	"context"
	"strings"
	"testing"
)

func TestSecondTurnCoordinationReceivesCommittedActionResults(t *testing.T) {
	generator := &scriptedGenerator{}
	app := newTestApp(t, generator)
	world, err := app.createFixtureWorld(context.Background(), "连续状态", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"向老板问好", "看看四周"} {
		run, err := app.SubmitRun(context.Background(), world.WorldID, RunRequest{RequestKey: input, Input: input})
		if err != nil {
			t.Fatal(err)
		}
		if completed := waitRun(t, app, world.WorldID, run.RunID); completed.Status != "completed" {
			t.Fatalf("run: %+v", completed)
		}
	}
	generator.mu.Lock()
	defer generator.mu.Unlock()
	var coordination []string
	for _, request := range generator.requests {
		if strings.Contains(request, "你是场景协调 Agent") {
			coordination = append(coordination, request)
		}
	}
	if len(coordination) != 2 {
		t.Fatalf("coordination count=%d", len(coordination))
	}
	for _, want := range []string{"本轮新增的游戏内分钟数", "列表为空时 outcomes 必须为 []", "action_id 原样使用该列表中的 event_id"} {
		if !strings.Contains(coordination[1], want) {
			t.Fatalf("missing coordination output contract: %s", want)
		}
	}
	prior := strings.SplitN(coordination[1], "\n世界：", 2)[0]
	if !strings.Contains(prior, "完成了行动") || !strings.Contains(prior, `"event_type":"npc_action_result"`) {
		t.Fatalf("second turn missing committed results: %s", prior)
	}
}

func TestNarrativeLengthPreferencesPreserveOutputBudgets(t *testing.T) {
	for length, budget := range map[string]int{"concise": 768, "standard": 1536, "detailed": 3072} {
		rule, got := narrativeLengthInstruction(NarrativeSettings{Length: length})
		if got != budget || !strings.Contains(rule, "不设最低字数") || !strings.Contains(rule, "简单") {
			t.Fatalf("length %s: budget=%d rule=%s", length, got, rule)
		}
	}
	rich := narrativeDetailInstruction(NarrativeSettings{Detail: NarrativeDetailRich})
	if strings.Contains(rich, "不得创造新事实") || !strings.Contains(rich, "低影响表现") {
		t.Fatalf("detail boundary: %s", rich)
	}
}

func TestCoordinationContinuityUsesCompletedResultsNotIntentOrPrivateMemory(t *testing.T) {
	text := coordinationContinuity([]Event{
		{EventType: "npc_action_intent", Content: "想藏起茶碗"},
		{EventType: "npc_action_result", Content: "茶已放在桌角，沈岚在桌边。"},
		{EventType: "player_private_speech", Content: "不公开的密语"},
	})
	if !strings.Contains(text, "茶已放在桌角") || strings.Contains(text, "想藏起") || strings.Contains(text, "密语") {
		t.Fatalf("continuity source: %s", text)
	}
	if !strings.Contains(text, "不是动作回放或对白记录") {
		t.Fatal("missing settled-state contract")
	}
}
