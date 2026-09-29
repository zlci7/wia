package storyapp

import (
	"context"
	"encoding/json"
	wiaworld "gameagent/backend/internal/world"
	"strings"
	"testing"
)

func TestInitialConcernsAreSnapshotDataAndPrivate(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "关切", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	for _, c := range s.Characters {
		initial, _ := characterByID(lanternDefinition(), c.EntityID)
		if c.InitialConcerns == "" || c.InitialConcerns != initial.InitialConcerns {
			t.Fatal("initial concern not copied")
		}
	}
	public, _ := json.Marshal(wiaworld.PublicCharacterViews(s.Characters))
	for _, c := range s.Characters {
		if strings.Contains(string(public), c.InitialConcerns) {
			t.Fatal("private concern in public projection")
		}
	}
	s.Characters[0].InitialConcerns = "ONLY_FIRST_CHARACTER_CONCERN"
	own := composeNPC(s, lanternDefinition(), s.Characters[0], "", "act", npcStageInput{PlayerPerception: "我沉默"}, "", 1)
	other := composeNPC(s, lanternDefinition(), s.Characters[1], "", "act", npcStageInput{PlayerPerception: "我沉默"}, "", 1)
	if !strings.Contains(own.Required, "ONLY_FIRST_CHARACTER_CONCERN") || strings.Contains(other.Required, "ONLY_FIRST_CHARACTER_CONCERN") {
		t.Fatal("concern scope")
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	db, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.db.Close()
	if _, err = db.db.Exec("DELETE FROM meta WHERE key LIKE 'initial_concerns:%'"); err != nil {
		t.Fatal(err)
	}
	legacy := readContextSnapshot(t, a, w.WorldID)
	for _, c := range legacy.Characters {
		if c.InitialConcerns != "" {
			t.Fatal("legacy world acquired latest definition concern")
		}
	}
}

func TestAutonomousSilentActionHasIndependentChannel(t *testing.T) {
	var out turnOutput
	run := wiaworld.Run{RunID: "autonomy"}
	actor := lanternDefinition().Characters[0]
	appendNPCDecisionOutput(&out, run, actor, npcDecision{Silent: true, ActionIntent: "检查门闩", Memory: "我准备检查门闩"}, lanternDefinition().Characters, "autonomy:input", 1, 1)
	var actions int
	for _, e := range out.Events {
		if e.EventType == "npc_dialogue" {
			t.Fatal("silent action invented speech")
		}
		if e.EventType == "npc_action_intent" {
			actions++
		}
	}
	if actions != 1 {
		t.Fatalf("actions=%d", actions)
	}
	s := contextFixture()
	m := composeNPC(s, lanternDefinition(), actor, "", "act", npcStageInput{PlayerPerception: "我沉默片刻，不回复"}, "", 1)
	if strings.Contains(m.Required, "基于玩家本轮输入作出一次自然决定") || !strings.Contains(m.Required, "处理自己的事务") {
		t.Fatal("reactive-only task")
	}
	if strings.Contains(m.System, "沈岚") || strings.Contains(m.System, "铁杉") {
		t.Fatal("character content in generic system")
	}
}
