package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	wiaworld "gameagent/backend/internal/world"
)

type stageCRiskGenerator struct {
	mu                sync.Mutex
	rejectFixedResult bool
}

func (g *stageCRiskGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(request.System, "持续世界协调器") {
		return deferredOpenWorldResponse(request)
	}
	switch {
	case strings.Contains(request.System, "结构化回合意图"):
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"","visibility":"public","action_rule_id":"clinic-stealth"}`}, nil
	case strings.Contains(request.System, "重要 NPC"):
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":"看见玩家尝试潜入。","relationship_proposals":[]}`}, nil
	case strings.Contains(request.System, "场景协调 Agent"):
		marker := "待裁定行动(JSON)："
		start := strings.Index(request.Input, marker)
		if start < 0 {
			return model.TextResponse{}, errors.New("actions missing")
		}
		start += len(marker)
		end := strings.Index(request.Input[start:], "\n")
		var actions []wiaworld.Event
		if end < 0 || json.Unmarshal([]byte(request.Input[start:start+end]), &actions) != nil || len(actions) != 1 {
			return model.TextResponse{}, errors.New("invalid actions")
		}
		fixed := "succeeded"
		section := request.Input[strings.Index(request.Input, "程序已固定的行动判定(JSON)："):]
		if strings.Contains(section, `"status":"failed"`) {
			fixed = "failed"
		}
		g.mu.Lock()
		reject := g.rejectFixedResult
		g.mu.Unlock()
		if reject {
			if fixed == "failed" {
				fixed = "succeeded"
			} else {
				fixed = "failed"
			}
		}
		delta := 3
		if fixed == "failed" {
			delta = 12
		}
		body, _ := json.Marshal(map[string]any{"time_minutes": 5, "scene": "废弃诊所", "scene_characters": []string{}, "outcomes": []map[string]any{{"action_id": actions[0].EventID, "status": fixed, "content": "玩家完成了潜入尝试。", "recipients": []string{"player"}, "bystanders": []string{}}}, "scene_updates": []any{}, "movements": []any{}, "state_effects": []map[string]any{{"entity_id": "player", "state_id": "investigation_strain", "delta": delta, "action_id": actions[0].EventID}}, "relationship_effects": []any{}, "item_transfers": []any{}})
		return model.TextResponse{Text: string(body)}, nil
	case strings.Contains(request.System, "玩家正文 Agent"):
		return model.TextResponse{Text: "你沿着地下室入口潜入，结果已经由判定确定。"}, nil
	default:
		return model.TextResponse{}, errors.New("unexpected stage")
	}
}

func (g *stageCRiskGenerator) allowFixedResult() {
	g.mu.Lock()
	g.rejectFixedResult = false
	g.mu.Unlock()
}

func TestStageCRiskRetryReusesPreparedRollAndCommitsFailureOnce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: stageBItemGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	game, err := a.Game("mist-embers")
	if err != nil {
		t.Fatal(err)
	}
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: "stage-c-create", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	move, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我赶到废弃诊所并拾起银镜。", RequestKey: "stage-c-move"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, world.WorldID, move.RunID); done.Status != "completed" {
		t.Fatalf("move=%+v", done)
	}
	generator := &stageCRiskGenerator{rejectFixedResult: true}
	a.modelMu.Lock()
	a.generator = generator
	a.modelMu.Unlock()
	failed, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我避开声响，悄悄潜入地下室。", RequestKey: "stage-c-risk"})
	if err != nil {
		t.Fatal(err)
	}
	failed = waitRun(t, a, world.WorldID, failed.RunID)
	if failed.Status != "failed" || failed.Reason != "coordination_generation_failed" {
		t.Fatalf("failed=%+v", failed)
	}
	path, _, err := a.worldRecord(ctx, world.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	prepared, found, err := store.ReadActionResolution(ctx, failed.InputID, "clinic-stealth")
	if err != nil || !found || prepared.SettledEventID != "" {
		t.Fatalf("prepared=%+v found=%t err=%v", prepared, found, err)
	}
	if _, err = store.TestingDB().ExecContext(ctx, `UPDATE action_resolutions SET roll=100 WHERE input_id=? AND rule_id='clinic-stealth'`, failed.InputID); err != nil {
		t.Fatal(err)
	}
	store.Close()
	generator.allowFixedResult()
	retried, err := a.RetryRun(ctx, world.WorldID, failed.RunID, "stage-c-risk-retry")
	if err != nil {
		t.Fatal(err)
	}
	retried = waitRun(t, a, world.WorldID, retried.RunID)
	if retried.Status != "completed" || retried.InputID != failed.InputID {
		t.Fatalf("retry=%+v", retried)
	}
	snapshot, err := a.ReadWorld(ctx, world.WorldID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.States["player"]["investigation_strain"].Value.Integer; got != 12 {
		t.Fatalf("investigation strain=%d", got)
	}
	failedEvent := false
	for _, event := range snapshot.Events {
		if event.SourceType == "rule:clinic-stealth:failed" {
			failedEvent = true
		}
	}
	if !failedEvent {
		t.Fatal("committed failed rule result is missing")
	}
	store, err = storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var preparations, changes int
	var roll int
	var settled string
	if err = store.TestingDB().QueryRow(`SELECT COUNT(*),MAX(roll),MAX(settled_event_id) FROM action_resolutions WHERE input_id=?`, failed.InputID).Scan(&preparations, &roll, &settled); err != nil {
		t.Fatal(err)
	}
	if err = store.TestingDB().QueryRow(`SELECT COUNT(*) FROM state_changes WHERE state_id='investigation_strain'`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if preparations != 1 || roll != 100 || changes != 1 || settled != retried.RunID+":rule:clinic-stealth" {
		t.Fatalf("preparations=%d roll=%d changes=%d settled=%q", preparations, roll, changes, settled)
	}
}
