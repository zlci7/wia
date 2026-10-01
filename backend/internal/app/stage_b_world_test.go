package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	wiaworld "gameagent/backend/internal/world"
)

type stageBGenerator struct {
	mu       sync.Mutex
	requests []model.TextRequest
}

func (g *stageBGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, request)
	g.mu.Unlock()
	switch {
	case strings.Contains(request.System, "结构化回合意图"):
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"npc:tailor","visibility":"public"}`}, nil
	case strings.Contains(request.System, "重要 NPC"):
		marker := "关系变化可用组合(JSON)："
		start := strings.Index(request.Input, marker)
		if start < 0 {
			return model.TextResponse{}, errors.New("npc relationship options missing")
		}
		start += len(marker)
		end := strings.Index(request.Input[start:], "\n")
		var options []struct {
			SourceID     string `json:"source_id"`
			TargetID     string `json:"target_id"`
			RelationType string `json:"relation_type"`
		}
		if end < 0 || json.Unmarshal([]byte(request.Input[start:start+end]), &options) != nil {
			return model.TextResponse{}, errors.New("invalid npc relationship options")
		}
		proposals := []map[string]any{}
		for _, option := range options {
			if option.TargetID == "player" && option.RelationType == "trust" {
				proposals = append(proposals, map[string]any{"target_id": option.TargetID, "relation_type": option.RelationType, "delta": 3, "source_id": option.SourceID})
				break
			}
		}
		body, _ := json.Marshal(map[string]any{"speech": "我会把细节说清楚。", "action_intent": "", "silent": false, "memory": "玩家愿意认真核对委托细节。", "relationship_proposals": proposals})
		return model.TextResponse{Text: string(body)}, nil
	case strings.Contains(request.System, "场景协调 Agent"):
		start := strings.Index(request.Input, "待裁定行动(JSON)：") + len("待裁定行动(JSON)：")
		end := strings.Index(request.Input[start:], "\n")
		if start < len("待裁定行动(JSON)：") || end < 0 {
			return model.TextResponse{}, errors.New("actions missing")
		}
		var actions []wiaworld.Event
		if json.Unmarshal([]byte(request.Input[start:start+end]), &actions) != nil || len(actions) != 1 {
			return model.TextResponse{}, errors.New("unexpected actions")
		}
		id := actions[0].EventID
		relationEffects := []map[string]any{}
		proposalMarker := `"source_id":"`
		proposalStart := strings.Index(request.Input, proposalMarker)
		if proposalStart >= 0 {
			proposalStart += len(proposalMarker)
			proposalEnd := strings.Index(request.Input[proposalStart:], `"`)
			if proposalEnd > 0 {
				relationEffects = append(relationEffects, map[string]any{"subject_id": "npc:tailor", "target_id": "player", "relation_type": "trust", "delta": 3, "proposal_source_id": request.Input[proposalStart : proposalStart+proposalEnd]})
			}
		}
		body, _ := json.Marshal(map[string]any{"time_minutes": 5, "scene": "调查事务所", "scene_characters": []string{"npc:tailor"}, "outcomes": []map[string]any{{"action_id": id, "status": "succeeded", "content": "玩家核对了委托记录，裁缝店老板认可了这种谨慎。", "recipients": []string{"player", "npc:tailor"}, "bystanders": []string{}}}, "scene_updates": []any{}, "movements": []any{}, "state_effects": []map[string]any{{"entity_id": "player", "state_id": "fatigue", "delta": 2, "action_id": id}}, "relationship_effects": relationEffects, "item_transfers": []any{}})
		return model.TextResponse{Text: string(body)}, nil
	case strings.Contains(request.System, "玩家正文 Agent"):
		return model.TextResponse{Text: "你逐项核对委托记录，裁缝店老板的回答比先前更认真。"}, nil
	default:
		return model.TextResponse{}, errors.New("unexpected stage")
	}
}

func (g *stageBGenerator) saw(systemPart, inputPart string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, request := range g.requests {
		if strings.Contains(request.System, systemPart) && strings.Contains(request.Input, inputPart) {
			return true
		}
	}
	return false
}

func TestStageBStateRelationshipPersistenceAndSaveAs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	generator := &stageBGenerator{}
	a, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: generator})
	if err != nil {
		t.Fatal(err)
	}
	game, err := a.Game("mist-embers")
	if err != nil {
		t.Fatal(err)
	}
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: "stage-b-create", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := before.States["player"]["fatigue"].Value.Integer; got != 18 {
		t.Fatalf("initial fatigue=%d", got)
	}
	if item := before.Items["mirror-3-917"]; item.LocationID != "abandoned-clinic" || item.HolderID != "" {
		t.Fatalf("initial item=%+v", item)
	}
	run, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我仔细核对委托记录。", AddresseeID: "npc:tailor", RequestKey: "stage-b-turn"})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, a, world.WorldID, run.RunID); completed.Status != "completed" {
		t.Fatalf("run=%+v", completed)
	}
	after, err := a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := after.States["player"]["fatigue"].Value.Integer; got != 20 {
		t.Fatalf("fatigue=%d", got)
	}
	found := false
	for _, relation := range after.Relationships {
		if relation.SubjectID == "npc:tailor" && relation.TargetID == "player" && relation.RelationType == "trust" {
			found = true
			if relation.Value != 0 {
				t.Fatalf("trust=%d", relation.Value)
			}
		}
	}
	if !found {
		t.Fatal("relationship missing")
	}
	for _, relation := range after.Relationships {
		if relation.SubjectID == "player" && relation.TargetID == "npc:tailor" && relation.RelationType == "trust" && relation.Value != 0 {
			t.Fatalf("reverse relationship changed=%+v", relation)
		}
	}
	replayed, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我仔细核对委托记录。", AddresseeID: "npc:tailor", RequestKey: "stage-b-turn"})
	if err != nil || replayed.RunID != run.RunID {
		t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
	}
	second, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我再核对一次签名。", AddresseeID: "npc:tailor", RequestKey: "stage-b-turn-2"})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, a, world.WorldID, second.RunID); completed.Status != "completed" {
		t.Fatalf("second run=%+v", completed)
	}
	if !generator.saw("场景协调 Agent", `"integer":20`) {
		t.Fatal("second coordination did not receive the committed player state")
	}
	after, err = a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, relation := range after.Relationships {
		if relation.SubjectID == "npc:tailor" && relation.TargetID == "player" && relation.RelationType == "trust" {
			found = true
			if relation.Value != 3 || relation.SourceEvent != second.RunID+":relationship-effect:1" {
				t.Fatalf("relationship after prior experience=%+v", relation)
			}
		}
		if relation.SubjectID == "player" && relation.TargetID == "npc:tailor" && relation.RelationType == "trust" && relation.Value != 0 {
			t.Fatalf("reverse relationship changed=%+v", relation)
		}
	}
	if !found {
		t.Fatal("relationship missing after second turn")
	}
	if _, err := a.Correct(ctx, world.WorldID, memory.CorrectionRequest{RequestKey: "rewrite-structured-source", ExpectedEpoch: after.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: run.RunID + ":player-action:result:1", Replacement: "改写结果。"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("structured fact correction=%v", err)
	}
	path, _, err := a.worldRecord(ctx, world.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	var stateChanges, relationshipChanges int
	if err = store.TestingDB().QueryRow(`SELECT (SELECT COUNT(*) FROM state_changes),(SELECT COUNT(*) FROM relationship_changes)`).Scan(&stateChanges, &relationshipChanges); err != nil {
		t.Fatal(err)
	}
	relationEventID := second.RunID + ":relationship-effect:1"
	proposalEventID := run.RunID + ":player-action:result:1"
	var proposalSource string
	if err = store.TestingDB().QueryRow(`SELECT proposal_source_event_id FROM relationship_changes WHERE source_event_id=?`, relationEventID).Scan(&proposalSource); err != nil {
		t.Fatal(err)
	}
	rows, err := store.TestingDB().Query(`SELECT parent_id FROM event_dependencies WHERE child_id=? ORDER BY parent_id`, relationEventID)
	if err != nil {
		t.Fatal(err)
	}
	parents := []string{}
	for rows.Next() {
		var parent string
		if err = rows.Scan(&parent); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		parents = append(parents, parent)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	store.Close()
	if stateChanges != 2 || relationshipChanges != 1 {
		t.Fatalf("history=%d/%d", stateChanges, relationshipChanges)
	}
	if proposalSource != proposalEventID || !slices.Equal(parents, []string{"opening", proposalEventID}) {
		t.Fatalf("relationship provenance proposal=%q parents=%v", proposalSource, parents)
	}
	status, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	copyOp, err := a.SaveAs(ctx, world.WorldID, "阶段 B 分支", "stage-b-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	reopened, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: &stageBGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	copySnapshot, err := reopened.ReadWorld(ctx, copyOp.TargetWorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := copySnapshot.States["player"]["fatigue"].Value.Integer; got != 22 {
		t.Fatalf("copied fatigue=%d", got)
	}
	for _, relation := range copySnapshot.Relationships {
		if relation.SubjectID == "npc:tailor" && relation.TargetID == "player" && relation.RelationType == "trust" && relation.Value != 3 {
			t.Fatalf("copied trust=%+v", relation)
		}
	}
}

type stageBItemGenerator struct{}

func (stageBItemGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	switch {
	case strings.Contains(request.System, "结构化回合意图"):
		if strings.Contains(request.Input, "交给裁缝") {
			return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"npc:tailor","visibility":"public"}`}, nil
		}
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"","visibility":"public"}`}, nil
	case strings.Contains(request.System, "重要 NPC"):
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":"玩家离开了事务所。","relationship_proposals":[]}`}, nil
	case strings.Contains(request.System, "场景协调 Agent"):
		start := strings.Index(request.Input, "待裁定行动(JSON)：") + len("待裁定行动(JSON)：")
		end := strings.Index(request.Input[start:], "\n")
		var actions []wiaworld.Event
		if start < len("待裁定行动(JSON)：") || end < 0 || json.Unmarshal([]byte(request.Input[start:start+end]), &actions) != nil || len(actions) != 1 {
			return model.TextResponse{}, errors.New("unexpected item action list")
		}
		id := actions[0].EventID
		if strings.Contains(actions[0].Content, "交给裁缝") {
			body, _ := json.Marshal(map[string]any{
				"time_minutes": 1, "scene": "调查事务所", "scene_characters": []string{"npc:tailor"},
				"outcomes":      []map[string]any{{"action_id": id, "status": "succeeded", "content": "玩家把裂纹银镜交给裁缝店老板。", "recipients": []string{"player", "npc:tailor"}, "bystanders": []string{}}},
				"scene_updates": []any{}, "movements": []any{}, "state_effects": []any{}, "relationship_effects": []any{},
				"item_transfers": []map[string]any{{"instance_id": "mirror-3-917", "from_holder_id": "player", "to_holder_id": "npc:tailor", "action_id": id}},
			})
			return model.TextResponse{Text: string(body)}, nil
		}
		if strings.Contains(request.Input, `"player":"abandoned-clinic"`) {
			route := []string{"abandoned-clinic", "red-chimney-street", "tailor-shop", "office"}
			body, _ := json.Marshal(map[string]any{
				"time_minutes": 20, "scene": "调查事务所", "scene_characters": []string{"npc:tailor"},
				"outcomes":      []map[string]any{{"action_id": id, "status": "succeeded", "content": "玩家返回调查事务所。", "recipients": []string{"player"}, "bystanders": []string{}}},
				"scene_updates": []map[string]any{{"content": "玩家回到调查事务所，手中拿着裂纹银镜。", "source_ids": []string{id}, "recipients": []string{"player"}}},
				"movements":     []map[string]any{{"entity_id": "player", "from": "abandoned-clinic", "to": "office", "route": route, "action_id": id}},
				"state_effects": []any{}, "relationship_effects": []any{},
				"item_transfers": []any{},
			})
			return model.TextResponse{Text: string(body)}, nil
		}
		route := []string{"office", "cafe", "clock-shop", "red-chimney-street", "abandoned-clinic"}
		body, _ := json.Marshal(map[string]any{
			"time_minutes": 20, "scene": "废弃诊所", "scene_characters": []string{},
			"outcomes":      []map[string]any{{"action_id": id, "status": "succeeded", "content": "玩家抵达废弃诊所并拾起裂纹银镜。", "recipients": []string{"player"}, "bystanders": []string{}}},
			"scene_updates": []map[string]any{{"content": "玩家站在废弃诊所内，手中拿着裂纹银镜。", "source_ids": []string{id}, "recipients": []string{"player"}}},
			"movements":     []map[string]any{{"entity_id": "player", "from": "office", "to": "abandoned-clinic", "route": route, "action_id": id}},
			"state_effects": []any{}, "relationship_effects": []any{},
			"item_transfers": []map[string]any{{"instance_id": "mirror-3-917", "from_location_id": "abandoned-clinic", "to_holder_id": "player", "action_id": id}},
		})
		return model.TextResponse{Text: string(body)}, nil
	case strings.Contains(request.System, "玩家正文 Agent"):
		if strings.Contains(request.Input, "裁缝店老板") {
			return model.TextResponse{Text: "你回到事务所，将裂纹银镜交到裁缝店老板手中。"}, nil
		}
		if strings.Contains(request.Input, "返回调查事务所") {
			return model.TextResponse{Text: "你带着裂纹银镜回到调查事务所。"}, nil
		}
		return model.TextResponse{Text: "你抵达废弃诊所，在灰尘中拾起那面裂纹银镜。"}, nil
	default:
		return model.TextResponse{}, errors.New("unexpected item stage")
	}
}

func TestStageBItemTransferIsUniquePersistentAndIdempotent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: stageBItemGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	game, err := a.Game("mist-embers")
	if err != nil {
		t.Fatal(err)
	}
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: "item-create", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我赶到废弃诊所并拾起银镜。", RequestKey: "item-pickup"})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, a, world.WorldID, run.RunID); completed.Status != "completed" {
		t.Fatalf("run=%+v", completed)
	}
	replayed, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我赶到废弃诊所并拾起银镜。", RequestKey: "item-pickup"})
	if err != nil || replayed.RunID != run.RunID {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	snapshot, err := a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	item := snapshot.Items["mirror-3-917"]
	if item.HolderID != "player" || item.LocationID != "" || item.Version != 2 {
		t.Fatalf("item=%+v", item)
	}
	returned, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我带着银镜回到事务所。", RequestKey: "item-return"})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, a, world.WorldID, returned.RunID); completed.Status != "completed" {
		t.Fatalf("return=%+v", completed)
	}
	transfer, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我把银镜交给裁缝店老板。", AddresseeID: "npc:tailor", RequestKey: "item-handoff"})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, a, world.WorldID, transfer.RunID); completed.Status != "completed" {
		t.Fatalf("handoff=%+v", completed)
	}
	snapshot, err = a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	item = snapshot.Items["mirror-3-917"]
	if item.HolderID != "npc:tailor" || item.LocationID != "" || item.Version != 3 {
		t.Fatalf("handed off item=%+v", item)
	}
	path, _, err := a.worldRecord(ctx, world.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	var transfers int
	if err = store.TestingDB().QueryRow(`SELECT COUNT(*) FROM item_transfers`).Scan(&transfers); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	if transfers != 2 {
		t.Fatalf("transfers=%d", transfers)
	}
	a.Close()
	reopened, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: stageBItemGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reloaded, err := reopened.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Items["mirror-3-917"]; got.HolderID != "npc:tailor" || got.LocationID != "" || got.Version != 3 {
		t.Fatalf("reloaded item=%+v", got)
	}
}
