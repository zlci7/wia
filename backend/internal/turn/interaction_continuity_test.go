package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// These controlled decisions exercise the real stage boundaries in two settings.
// They establish delivery and settlement, not a model's choice or prose quality.
type interactionGenerator struct {
	mu            sync.Mutex
	kind          string
	duplicateItem bool
	inputs        map[string][]string
	hostCalls     int
	narration     string
}

const refusalSpeech = "这件工具现在不能交给你，我要先核对登记。"
const privateSpeech = "我只告诉你：记录夹在蓝色封套里。"
const failedAttempt = "尝试打开仍然锁着的工具柜。"
const failureObservation = "柜门仍锁着，我没有拿到里面的东西。"

func (g *interactionGenerator) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(req.System, "结构化回合意图") {
		return model.TextResponse{Text: wire.MarshalJSON(TurnIntent{IntentType: "observe", Visibility: "public"})}, nil
	}
	if strings.Contains(req.System, "你是一个重要 NPC") {
		id := ""
		for _, candidate := range []string{"npc:a", "npc:b", "npc:c"} {
			if strings.Contains(req.Input, "角色资料：本人标识 "+candidate) {
				id = candidate
			}
		}
		if id == "" {
			return model.TextResponse{}, fmt.Errorf("character identity missing")
		}
		g.mu.Lock()
		g.inputs[id] = append(g.inputs[id], req.Input)
		g.mu.Unlock()
		decision := NPCDecision{Silent: true}
		first := strings.Contains(req.Input, "阶段：1；")
		switch g.kind {
		case "refusal":
			if first && id == "npc:a" {
				decision = NPCDecision{Speech: refusalSpeech}
			} else if !first && id == "npc:b" && strings.Contains(req.Input, refusalSpeech) {
				decision = NPCDecision{Speech: "那我先查看登记，不替你作决定。"}
			}
		case "private":
			if first && id == "npc:a" {
				decision = NPCDecision{Speech: privateSpeech, SpeechVisibility: "private", SpeechRecipients: []string{"npc:b"}}
			} else if !first && id == "npc:b" {
				decision = NPCDecision{Speech: "我会核对封套。", SpeechVisibility: "private", SpeechRecipients: []string{"npc:a"}}
			}
		case "contested-item":
			if first && (id == "npc:a" || id == "npc:b") {
				decision.ActionIntent = "尝试拿起工作台上唯一的工具。"
			}
		case "failed-action":
			if first && id == "npc:b" {
				decision.ActionIntent = failedAttempt
			}
		}
		return model.TextResponse{Text: wire.MarshalJSON(decision)}, nil
	}
	if strings.Contains(req.System, "场景协调 Agent") {
		g.hostCalls++
		_, raw, found := strings.Cut(req.Input, "待裁定行动(JSON)：")
		if !found {
			return model.TextResponse{}, fmt.Errorf("pending actions missing")
		}
		var actions []wiaworld.Event
		if err := json.Unmarshal([]byte(strings.SplitN(raw, "\n", 2)[0]), &actions); err != nil {
			return model.TextResponse{}, err
		}
		outcomes := []hostActionResult{}
		transfers := []itemTransferEffect{}
		for _, action := range actions {
			status, text := "succeeded", "我看到了工作台上的情况。"
			if g.kind == "contested-item" && action.ActorID == "npc:a" {
				text = "甲先拿到了唯一的工具。"
				transfers = append(transfers, itemTransferEffect{InstanceID: "unique-tool", FromLocationID: "workplace", ToHolderID: "npc:a", ActionID: action.EventID})
			}
			if g.kind == "contested-item" && action.ActorID == "npc:b" {
				status, text = "failed", "乙没有拿到已由甲持有的工具。"
				if g.duplicateItem {
					status = "succeeded"
					transfers = append(transfers, itemTransferEffect{InstanceID: "unique-tool", FromLocationID: "workplace", ToHolderID: "npc:b", ActionID: action.EventID})
				}
			}
			if g.kind == "failed-action" && action.ActorID == "npc:b" {
				status, text = "failed", failureObservation
			}
			recipients := []string{"player", "npc:a", "npc:b", "npc:c"}
			outcomes = append(outcomes, hostActionResult{ActionID: action.EventID, Status: status, Content: text, Recipients: recipients, Projections: outcomeProjectionFixture(text, recipients)})
		}
		return model.TextResponse{Text: wire.MarshalJSON(map[string]any{"time_minutes": 1, "scene": "工作场所", "scene_characters": []string{"npc:a", "npc:b", "npc:c"}, "scene_updates": []any{}, "outcomes": outcomes, "movements": []any{}, "item_transfers": transfers})}, nil
	}
	if strings.Contains(req.System, "玩家正文 Agent") {
		g.narration = req.Input
		return model.TextResponse{Text: "你看过工作台，几个人各自处理眼前的事情。"}, nil
	}
	return model.TextResponse{}, fmt.Errorf("unexpected stage")
}

func interactionSnapshot(setting string) Snapshot {
	def := story.Definition{Revision: setting + ".test.v1", Background: setting, Scene: "工作场所", Clock: "2189-12-31 23:55", Capabilities: map[string]int{"spatial": 1, "items": 1}, Locations: []story.Location{{ID: "workplace", Name: "工作场所", Kind: "place", Public: true}}, ItemDefinitions: []story.ItemDefinition{{ID: "tool", Name: "唯一的工具", Projection: "public"}}}
	s := Snapshot{Definition: def, Summary: wiaworld.WorldSummary{Clock: def.Clock}, PlayerName: "主角", Narrative: wiaworld.DefaultNarrativeSettings(), SceneLocation: "workplace", SceneVersion: 1, Positions: map[string]string{"player": "workplace"}, PositionSources: map[string]string{"player": "opening"}, Perceptions: map[string][]wiaworld.Perception{}, LongMemory: map[string]MemoryContext{}, Items: map[string]wiaworld.ItemInstance{"unique-tool": {InstanceID: "unique-tool", DefinitionID: "tool", LocationID: "workplace", Version: 1, SourceEvent: "opening"}}}
	for _, id := range []string{"npc:a", "npc:b", "npc:c"} {
		s.Characters = append(s.Characters, wiaworld.Character{EntityID: id, Name: id, Role: "值班人员", Profile: "本人标识 " + id, InScene: true})
		s.Positions[id], s.PositionSources[id] = "workplace", "opening"
		s.LongMemory[id] = MemoryContext{}
	}
	s.Definition.Characters = s.Characters
	s.SceneViews = initialSceneViews(s)
	return s
}

func TestIndependentNPCInteractionContinuity(t *testing.T) {
	for _, setting := range []string{"城市调查事务所", "轨道维修站"} {
		for _, kind := range []string{"refusal", "contested-item", "failed-action", "private"} {
			t.Run(setting+"/"+kind, func(t *testing.T) {
				snapshot := interactionSnapshot(setting)
				g := &interactionGenerator{kind: kind, inputs: map[string][]string{}}
				run := wiaworld.Run{RunID: "interaction", Input: "我在工作台旁观察。"}
				output, err := New(&rosterHost{}, Deps{}).executeSnapshot(t.Context(), g, snapshot, run)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "refusal":
					if len(g.inputs["npc:b"]) != 2 || !strings.Contains(g.inputs["npc:b"][1], refusalSpeech) || !strings.Contains(g.inputs["npc:b"][1], "interaction:npc:a:speech:1:projection:npc:b") || output.Decisions["npc:b"].Speech == "" {
						t.Fatal("the refusal did not reach the listener's own incremental decision")
					}
					if len(g.inputs["npc:a"]) != 1 {
						t.Fatal("a speaker reacted again to its own first-stage speech")
					}
				case "private":
					if len(g.inputs["npc:b"]) != 2 || len(g.inputs["npc:c"]) != 1 || strings.Contains(strings.Join(g.inputs["npc:c"], ""), privateSpeech) || strings.Contains(g.narration, privateSpeech) {
						t.Fatal("private speech lost its listener or reached an unlisted bystander/player")
					}
					for _, event := range output.VisibleEvents {
						if strings.Contains(event.Content, privateSpeech) || event.EventType == "npc_dialogue" {
							t.Fatal("private NPC-to-NPC replies became visible to the player")
						}
					}
				case "contested-item":
					if output.Items["unique-tool"].HolderID != "npc:a" || len(output.ItemTransfers) != 1 || len(g.inputs["npc:a"]) != 1 || len(g.inputs["npc:b"]) != 1 {
						t.Fatal("conflicting attempts did not settle to one owner within the existing stages")
					}
				case "failed-action":
					mergeConfirmedInput(&snapshot, output)
					material := composeNPC(snapshot, snapshot.Definition, snapshot.Characters[1], "", "observe", StageInput{PlayerPerception: "我问你刚才发生了什么。"}, "", 1)
					request, _, buildErr := (ContextComposer{}).Build(material, material.System, 512)
					if buildErr != nil || !strings.Contains(request.Input, failureObservation) || !strings.Contains(request.Input, "interaction:npc:b:action:1:result:") {
						t.Fatal("the failed action was absent from the actor's subsequent authorized request", buildErr)
					}
				}
			})
		}
	}
}

func TestConflictingItemResultsFailWithoutPublishingASecondOwner(t *testing.T) {
	snapshot := interactionSnapshot("轨道维修站")
	g := &interactionGenerator{kind: "contested-item", duplicateItem: true, inputs: map[string][]string{}}
	output, err := New(&rosterHost{}, Deps{}).executeSnapshot(t.Context(), g, snapshot, wiaworld.Run{RunID: "conflict", Input: "我在工作台旁观察。"})
	if err == nil || StageOf(err) != string(StageCoordination) || g.hostCalls != 2 || len(output.ItemTransfers) != 0 || snapshot.Items["unique-tool"].LocationID != "workplace" || snapshot.Items["unique-tool"].HolderID != "" {
		t.Fatal("an invalid contested-item result escaped the one-repair, atomic-failure boundary", err)
	}
}
