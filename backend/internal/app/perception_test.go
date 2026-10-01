package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

const whisperedPart = "我私下告诉裁缝：口令是午夜。"
const movedPart = "然后前往废弃诊所并拾起银镜。"

type mixedInputGenerator struct{ failMove bool }

func (g mixedInputGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(request.System, "结构化回合意图") {
		intent := turn.TurnIntent{IntentType: "act", Visibility: "public", Fragments: []turn.InputFragment{{Text: whisperedPart, ActorID: "player", IntentType: "speak", AddresseeID: "npc:tailor", Visibility: "private"}, {Text: movedPart, ActorID: "player", IntentType: "act", Visibility: "public"}}}
		return model.TextResponse{Text: wire.MarshalJSON(intent)}, nil
	}
	if strings.Contains(request.System, "重要 NPC") {
		return model.TextResponse{Text: `{"speech":"我会记住这句口令。","speech_visibility":"private","speech_recipients":["player"],"action_intent":"","silent":false,"memory":"我收到了一句私下传达的口令。","relationship_proposals":[]}`}, nil
	}
	if strings.Contains(request.System, "场景协调 Agent") {
		raw := strings.SplitN(strings.SplitN(request.Input, "待裁定行动(JSON)：", 2)[1], "\n", 2)[0]
		var actions []wiaworld.Event
		if err := json.Unmarshal([]byte(raw), &actions); err != nil || len(actions) != 1 {
			return model.TextResponse{}, errors.New("mixed action missing")
		}
		if actions[0].Content == whisperedPart {
			body := map[string]any{"time_minutes": 0, "scene": "调查事务所", "scene_characters": []string{"npc:tailor"}, "outcomes": []map[string]any{{"action_id": actions[0].EventID, "status": "succeeded", "content": "裁缝获知口令是午夜。", "recipients": []string{"player", "npc:tailor"}, "projections": []map[string]string{{"recipient": "player", "content": "我私下把口令告诉裁缝。"}, {"recipient": "npc:tailor", "content": "玩家告诉我口令是午夜。"}}}}, "scene_updates": []any{}, "movements": []any{}, "state_effects": []any{}, "relationship_effects": []any{}, "item_transfers": []any{}}
			return model.TextResponse{Text: wire.MarshalJSON(body)}, nil
		}
		if g.failMove {
			return model.TextResponse{}, errors.New("second fragment unavailable")
		}
	}
	return (stageBItemGenerator{}).GenerateText(ctx, request)
}

func TestMixedInputIsAtomicAndKeepsPersonalSourcesAcrossCopyRestart(t *testing.T) {
	for _, fail := range []bool{true, false} {
		t.Run(map[bool]string{true: "later-fragment-failure", false: "copy-restart"}[fail], func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			a, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: mixedInputGenerator{failMove: fail}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			game, _ := a.Game("mist-embers")
			world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: "mixed-create", Activate: true})
			if err != nil {
				t.Fatal(err)
			}
			run, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: whisperedPart + movedPart, RequestKey: "mixed"})
			if err != nil {
				t.Fatal(err)
			}
			done := waitRun(t, a, world.WorldID, run.RunID)
			snapshot := readContextSnapshot(t, a, world.WorldID)
			if fail {
				if done.Status != "failed" || done.Input != whisperedPart+movedPart || snapshot.Summary.TurnSeq != 0 || snapshot.Positions["player"] != "office" || snapshot.Items["mirror-3-917"].HolderID != "" || snapshot.Summary.EventHead != 1 {
					t.Fatal("partial input committed or original words lost")
				}
				return
			}
			if done.Status != "completed" || snapshot.Summary.TurnSeq != 1 || snapshot.Positions["player"] != "abandoned-clinic" || snapshot.Items["mirror-3-917"].HolderID != "player" {
				t.Fatalf("mixed turn: %+v", done)
			}
			status, err := a.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			copy, err := a.SaveAs(ctx, world.WorldID, "感知分支", "copy-mixed", status.ActiveRevision)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: stageBItemGenerator{}})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			for _, id := range []string{world.WorldID, copy.TargetWorldID} {
				saved := readContextSnapshot(t, reopened, id)
				if saved.Items["mirror-3-917"].HolderID != "player" || saved.Positions["player"] != "abandoned-clinic" {
					t.Fatal("saved working facts changed")
				}
				for recipient, perceptions := range saved.Perceptions {
					if recipient != "player" && recipient != "npc:tailor" && strings.Contains(wire.MarshalJSON(perceptions), "午夜") {
						t.Fatal("saved whisper crossed recipients")
					}
				}
				messages, err := reopened.ReadMessages(ctx, id, 20)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, message := range messages {
					found = found || message.Kind == "player" && message.Content == whisperedPart+movedPart
				}
				if !found {
					t.Fatal("full player input missing after copy/restart")
				}
				if !saved.PerceivedSources["player"][saved.Items["mirror-3-917"].SourceEvent] {
					t.Fatal("personal projection lost durable item visibility")
				}
			}
		})
	}
}
