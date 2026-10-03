package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type wholeTurnCapacityGenerator struct {
	mu       sync.Mutex
	requests []model.TextRequest
}

func (g *wholeTurnCapacityGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, request)
	g.mu.Unlock()
	switch {
	case strings.Contains(request.System, "结构化回合意图"):
		fragments := []InputFragment{}
		for _, text := range capacityInputParts {
			fragments = append(fragments, InputFragment{Text: text, ActorID: "player", IntentType: "observe", Visibility: "public"})
		}
		return model.TextResponse{Text: wire.MarshalJSON(TurnIntent{IntentType: "observe", Visibility: "public", Fragments: fragments})}, nil
	case strings.Contains(request.System, "重要 NPC"):
		decision := NPCDecision{Silent: true, RelationshipProposals: []relationshipProposal{}}
		if strings.Contains(request.Input, "阶段：5") {
			decision.ActionIntent = "我独立检查眼前的公开情况，暂时留在原地。"
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(wire.MarshalJSON(decision)), &body); err != nil {
			return model.TextResponse{}, err
		}
		body["relationship_proposals"] = []any{}
		return model.TextResponse{Text: wire.MarshalJSON(body)}, nil
	case strings.Contains(request.System, "持续世界协调器"):
		match := regexp.MustCompile(`来源 (material:[a-zA-Z0-9._:-]+)`).FindStringSubmatch(request.Input)
		if len(match) != 2 {
			return model.TextResponse{}, fmt.Errorf("world material source missing")
		}
		return model.TextResponse{Text: wire.MarshalJSON(plotResolution{Status: "deferred", Content: "暂无新的外部变化。", SourceIDs: []string{match[1]}, Projections: []plotProjection{}, DecisionRequests: []string{}})}, nil
	case strings.Contains(request.System, "场景协调 Agent"), strings.Contains(request.System, "世界剧情行动协调器"):
		marker := "待裁定行动(JSON)："
		offscene := strings.Contains(request.System, "世界剧情行动协调器")
		if offscene {
			marker = "待处理NPC记录："
		}
		_, raw, ok := strings.Cut(request.Input, marker)
		if !ok {
			return model.TextResponse{}, fmt.Errorf("pending action records missing")
		}
		var events []wiaworld.Event
		if err := json.Unmarshal([]byte(strings.SplitN(raw, "\n", 2)[0]), &events); err != nil {
			return model.TextResponse{}, err
		}
		audience := []string{"player", "npc:tailor", "npc:reporter", "npc:clockmaker", "npc:missing-woman"}
		outcomes := []hostActionResult{}
		for _, event := range events {
			if event.EventType != "player_action_intent" && event.EventType != "npc_action_intent" {
				continue
			}
			projections := []actionProjection{}
			for _, recipient := range audience {
				projections = append(projections, actionProjection{Recipient: recipient, Content: event.Content + "：公开检查已完成。"})
			}
			outcomes = append(outcomes, hostActionResult{ActionID: event.EventID, Status: "succeeded", Content: event.Content + "：公开检查已完成。", Recipients: audience, Projections: projections})
		}
		body := map[string]any{"time_minutes": 1, "outcomes": outcomes, "scene_updates": []any{}, "movements": []any{}, "state_effects": []any{}, "relationship_effects": []any{}, "item_transfers": []any{}}
		if !offscene {
			body["scene"], body["scene_characters"] = "调查事务所", audience[1:]
		}
		return model.TextResponse{Text: wire.MarshalJSON(body)}, nil
	default:
		return model.TextResponse{Text: "你在事务所检查了公开情况，其他人物分别完成了自己的观察。"}, nil
	}
}

var capacityInputParts = []string{"我观察门口。", "再检查桌面。", "然后看看窗外。", "最后整理公开记录。"}

func TestFormalFourFragmentTurnAndTwoDueActionsFitEachRequestBudget(t *testing.T) {
	pack, err := content.Load(filepath.Join("..", "content", "packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	def := pack.Definition
	progress, err := InitialOpenProgress(def)
	if err != nil {
		t.Fatal(err)
	}
	minute, _ := plot.ClockMinute(def.Clock)
	for i := range progress.Plans {
		progress.Plans[i].NextCheck = minute
	}
	snapshot := Snapshot{Definition: def, Characters: append([]wiaworld.Character{}, def.Characters...), OpenProgress: progress, Summary: wiaworld.WorldSummary{WorldID: "world", GameID: "mist-embers", Clock: def.Clock, ContextEpoch: 1}, Narrative: wiaworld.DefaultNarrativeSettings(), SceneVersion: 1, Positions: clonePositions(def.InitialLocations), States: map[string]map[string]wiaworld.EntityState{}, Items: map[string]wiaworld.ItemInstance{}, Sources: map[string]SourceMetadata{}, Perceptions: map[string][]wiaworld.Perception{}, LongMemory: map[string]MemoryContext{}}
	// This is a valid later-world situation: all four fixed characters are now
	// with the player. Their authored initial locations remain frozen in def.
	for _, c := range snapshot.Characters {
		snapshot.Positions[c.EntityID] = "office"
	}
	for entity, values := range def.InitialStates {
		snapshot.States[entity] = map[string]wiaworld.EntityState{}
		for id, value := range values {
			snapshot.States[entity][id] = wiaworld.EntityState{EntityID: entity, StateID: id, Value: value, SourceEvent: "opening", Version: 1}
		}
	}
	for _, item := range def.InitialItems {
		snapshot.Items[item.InstanceID] = wiaworld.ItemInstance{InstanceID: item.InstanceID, DefinitionID: item.DefinitionID, HolderID: item.HolderID, LocationID: item.LocationID, SourceEvent: "opening", Version: 1}
	}
	entities := append([]string{"player"}, CharacterIDs(snapshot.Characters)...)
	for _, definition := range def.RelationDefinitions {
		for _, subject := range entities {
			for _, target := range entities {
				if subject == target {
					continue
				}
				value := definition.Default
				for _, initial := range def.InitialRelations {
					if initial.SubjectID == subject && initial.TargetID == target && initial.RelationType == definition.ID {
						value = initial.Value
					}
				}
				snapshot.Relationships = append(snapshot.Relationships, wiaworld.Relationship{SubjectID: subject, TargetID: target, RelationType: definition.ID, Value: value, SourceEvent: "opening", Version: 1})
			}
		}
	}
	if err := refreshSpatialProjection(&snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.SceneViews = initialSceneViews(snapshot)
	g := &wholeTurnCapacityGenerator{}
	run := wiaworld.Run{RunID: "capacity", Input: strings.Join(capacityInputParts, ""), BaseContextEpoch: 1}
	output, err := New(&rosterHost{}, Deps{}).executeSnapshot(t.Context(), g, snapshot, run)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	total, maximum := 0, 0
	for _, request := range g.requests {
		tokens := model.FramedTextInputTokens(request)
		if request.MaxInputTokens != 12000 || tokens > request.MaxInputTokens {
			t.Fatal("request input budget changed or was exceeded")
		}
		total += tokens
		maximum = max(maximum, tokens)
		switch {
		case strings.Contains(request.System, "结构化回合意图"):
			counts["intent"]++
		case strings.Contains(request.System, "重要 NPC"):
			counts["npc"]++
		case strings.Contains(request.System, "持续世界协调器"):
			counts["world"]++
		case strings.Contains(request.System, "世界剧情行动协调器"):
			counts["world_actions"]++
		case strings.Contains(request.System, "场景协调 Agent"):
			counts["scene"]++
		default:
			counts["narration"]++
		}
	}
	if counts["intent"] != 1 || counts["npc"] != 18 || counts["scene"] != 4 || counts["world"] != 1 || counts["world_actions"] != 1 || counts["narration"] != 1 {
		t.Fatalf("whole turn call boundaries changed: %v", counts)
	}
	for part := range capacityInputParts {
		if _, ok := EventByID(output.Events, fmt.Sprintf("capacity:part:%d:player-action:result:1", part+1)); !ok {
			t.Fatal("a segment result was lost")
		}
	}
	npcResults := 0
	for _, event := range output.Events {
		if event.EventType == "npc_action_result" {
			npcResults++
		}
	}
	if npcResults != 2 {
		t.Fatalf("due action results=%d, want 2", npcResults)
	}
	t.Logf("requests=%d max_input=%d aggregate_input=%d counts=%v", len(g.requests), maximum, total, counts)
}
