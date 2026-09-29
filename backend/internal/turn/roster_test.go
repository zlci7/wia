package turn

import (
	"context"
	"testing"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	wiaworld "gameagent/backend/internal/world"
)

// rosterHost is a host whose opening roster and closing roster differ: A and B are
// present when the player acts, and by the end B has left while C has arrived. It
// records which characters the turn asked it to write memories for.
type rosterHost struct {
	memoryRecipients []string
}

func (h *rosterHost) LogStage(string, wiaworld.Run, Stage, string, string, int, string, []string, string, int, time.Duration) {
}

func (h *rosterHost) LoadInput(context.Context, *storage.WorldStore, wiaworld.Run, model.TextGenerator, int) (Snapshot, error) {
	return Snapshot{
		Summary: wiaworld.WorldSummary{WorldID: "w1", GameID: "harbor", Clock: "第 1 日 08:00"},
		Characters: []wiaworld.Character{
			{EntityID: "npc:a", Name: "甲", Role: "掌柜", InScene: true},
			{EntityID: "npc:b", Name: "乙", Role: "船夫", InScene: true},
			{EntityID: "npc:c", Name: "丙", Role: "旅人", InScene: false},
		},
	}, nil
}

func (h *rosterHost) ResolveIntent(_ context.Context, _ model.TextGenerator, snapshot *Snapshot, run wiaworld.Run) (TurnIntent, Output, error) {
	intent := TurnIntent{IntentType: "speak", AddresseeID: "npc:a", Visibility: "public"}
	return intent, OpenOutput(snapshot, intent, run), nil
}

func (h *rosterHost) RunCharacters(context.Context, model.TextGenerator, *Snapshot, wiaworld.Run, TurnIntent, *Output) error {
	return nil
}

// Coordinate ends the turn with a different roster than it started with.
func (h *rosterHost) Coordinate(_ context.Context, _ model.TextGenerator, _ *Snapshot, _ wiaworld.Run, _ TurnIntent, output *Output) error {
	output.SceneCharacters = []string{"npc:a", "npc:c"}
	return nil
}

func (h *rosterHost) Narrate(context.Context, model.TextGenerator, *Snapshot, wiaworld.Run, TurnIntent, *Output) error {
	return nil
}

// TestPlayerExperienceUsesTheOpeningRoster pins a regression no compiler could catch.
//
// Recording what the player did is a question about who was present when the player
// acted. The turn's closing roster answers a different question: a character who left
// during the turn perceived the input, and one who arrived afterwards did not. Reading
// the closing roster gives both of them the wrong answer, and does it silently — the
// memories are still written, just attributed to the wrong people.
//
// The scene moves during the turn in the adapter, so the closing roster is not available
// until after the character stages have run. The opening one has to be kept.
func TestPlayerExperienceUsesTheOpeningRoster(t *testing.T) {
	host := &rosterHost{}
	service := New(host, Deps{})
	output, err := service.Execute(context.Background(), nil, wiaworld.Run{RunID: "run1", Input: "我说给大家听"}, stubGenerator{})
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

type stubGenerator struct{}

func (stubGenerator) GenerateText(context.Context, model.TextRequest) (model.TextResponse, error) {
	return model.TextResponse{}, nil
}
