package turn

import (
	"context"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

// rosterHost is a host whose opening roster and closing roster differ: A and B are
// present when the player acts, and by the end B has left while C has arrived.
type rosterHost struct {
	memoryRecipients []string
}

func (h *rosterHost) LogStage(string, wiaworld.Run, Stage, string, string, int, string, []string, string, int, time.Duration) {
}

// stageGenerator answers each stage of a turn with the smallest valid response, so a test
// can run the real pipeline — intent, character decisions, narration — without a model.
type stageGenerator struct{}

func (stageGenerator) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	switch {
	case strings.Contains(req.System, "结构化回合意图"):
		return model.TextResponse{Text: `{"intent_type":"speak","addressee_id":"","visibility":"public"}`}, nil
	case strings.Contains(req.System, "你是一个重要 NPC"):
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":""}`}, nil
	case strings.Contains(req.System, "场景协调 Agent"):
		return model.TextResponse{Text: `{"time_minutes":0,"scene":"大厅","scene_characters":["npc:a","npc:c"],"outcomes":[{"action_id":"run1:player-action","status":"succeeded","content":"话已经说出。","recipients":["player","npc:a"]}],"scene_updates":[]}`}, nil
	default:
		return model.TextResponse{Text: "旅人把话说给众人听。"}, nil
	}
}

// TestPlayerExperienceUsesTheOpeningRoster pins a regression no compiler could catch.
//
// Recording what the player did is a question about who was present when the player
// acted. The turn's closing roster answers a different question: a character who left
// during the turn perceived the input, and one who arrived afterwards did not. Reading
// the closing roster gives both of them the wrong answer, and does it silently — the
// memories are still written, just attributed to the wrong people.
//
// The scene moves during coordination, so the closing roster is not available until after
// the character stages have run. The opening one has to be kept.
func TestPlayerExperienceUsesTheOpeningRoster(t *testing.T) {
	host := &rosterHost{}
	service := New(host, Deps{})
	snapshot := Snapshot{
		Summary: wiaworld.WorldSummary{WorldID: "w1", GameID: "harbor", Clock: "第 1 日 08:00"},
		Characters: []wiaworld.Character{
			{EntityID: "npc:a", Name: "甲", Role: "掌柜", InScene: true},
			{EntityID: "npc:b", Name: "乙", Role: "船夫", InScene: true},
			{EntityID: "npc:c", Name: "丙", Role: "旅人", InScene: false},
		},
	}
	output, err := service.executeSnapshot(context.Background(), stageGenerator{}, snapshot, wiaworld.Run{RunID: "run1", Input: "我说给大家听"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var recipients []string
	for _, memory := range output.Memories {
		recipients = append(recipients, memory.RecipientID)
	}
	// A and B heard the player; C arrived afterwards and did not.
	if len(recipients) != 2 || recipients[0] != "npc:a" || recipients[1] != "npc:b" {
		t.Errorf("memories recorded for %v, want the opening participants [npc:a npc:b]; "+
			"the closing roster was %v", recipients, output.SceneCharacters)
	}
	// The closing roster really did differ, or the test would pass for the wrong reason.
	if len(output.SceneCharacters) != 2 || output.SceneCharacters[1] != "npc:c" {
		t.Fatalf("the fixture did not change the roster: %v", output.SceneCharacters)
	}
}
