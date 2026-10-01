package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type spatialWorldGenerator struct{}

func (spatialWorldGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	switch {
	case strings.Contains(request.System, "结构化回合意图"):
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"","visibility":"public"}`}, nil
	case strings.Contains(request.System, "重要 NPC"):
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":"玩家离开了事务所。","relationship_proposals":[]}`}, nil
	case strings.Contains(request.System, "场景协调 Agent"):
		start := strings.Index(request.Input, "待裁定行动(JSON)：") + len("待裁定行动(JSON)：")
		end := strings.Index(request.Input[start:], "\n")
		if start < len("待裁定行动(JSON)：") || end < 0 {
			return model.TextResponse{}, errors.New("action list missing")
		}
		var actions []wiaworld.Event
		if err := json.Unmarshal([]byte(request.Input[start:start+end]), &actions); err != nil || len(actions) != 1 {
			return model.TextResponse{}, errors.New("unexpected action list")
		}
		actionID := actions[0].EventID
		from, to := "office", "cafe"
		scene := "街角咖啡馆：午前的客人不多，报纸架靠着临街窗户。"
		result := "玩家从调查事务所抵达街角咖啡馆。"
		if strings.Contains(request.Input, `"player":"cafe"`) {
			from, to = "cafe", "office"
			scene = "调查事务所：煤油灯照着铺开的旧报纸，街雾停在窗外。"
			result = "玩家从街角咖啡馆返回调查事务所。"
		}
		body, _ := json.Marshal(map[string]any{
			"time_minutes":     5,
			"scene":            scene,
			"scene_characters": []string{},
			"outcomes":         []map[string]any{{"action_id": actionID, "status": "succeeded", "content": result, "projections": outcomeProjectionFixture(result, []string{"player"}), "recipients": []string{"player"}, "bystanders": []string{}}},
			"scene_updates":    []map[string]any{{"content": scene, "source_ids": []string{actionID}, "recipients": []string{"player"}}},
			"movements":        []map[string]any{{"entity_id": "player", "from": from, "to": to, "route": []string{from, to}, "action_id": actionID}},
			"state_effects":    []map[string]any{}, "relationship_effects": []map[string]any{}, "item_transfers": []map[string]any{},
		})
		return model.TextResponse{Text: string(body)}, nil
	case strings.Contains(request.System, "玩家正文 Agent"):
		return model.TextResponse{Text: "你离开事务所，穿过雾中的街口，来到咖啡馆。"}, nil
	default:
		return model.TextResponse{}, errors.New("unexpected spatial test stage")
	}
}

func TestV3SpatialWorldMovesAndSurvivesSaveAndRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	app, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: spatialWorldGenerator{}, StoryPacksPath: filepath.Join("testdata", "mist-v3")})
	if err != nil {
		t.Fatal(err)
	}
	game, err := app.Game("mist-embers")
	if err != nil {
		t.Fatal(err)
	}
	world, err := app.CreateStoryWorld(ctx, CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: "create-spatial", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	if world.SceneLocation != "office" || world.Location == nil || world.Location.Name != "调查事务所" || len(world.AdjacentLocations) != 2 {
		t.Fatalf("initial spatial projection: %+v", world)
	}
	if _, err := app.PreviewCharacterPromotion(ctx, world.WorldID, "bystander:clerk", true); !errors.Is(err, content.ErrContentInvalid) {
		t.Fatalf("spatial promotion preview was not rejected: %v", err)
	}
	if _, err := app.PromoteCharacter(ctx, world.WorldID, PromotionRequest{
		RequestKey: "promote-spatial-bystander", ExpectedContextEpoch: world.ContextEpoch,
		BystanderID: "bystander:clerk", Draft: PromotionDraft{Role: "书记员", Profile: "事务所书记员。"},
	}); !errors.Is(err, content.ErrContentInvalid) {
		t.Fatalf("spatial promotion was not rejected: %v", err)
	}
	run, err := app.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我去街角咖啡馆。", RequestKey: "move-to-cafe"})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, app, world.WorldID, run.RunID); completed.Status != "completed" {
		t.Fatalf("movement run: %+v", completed)
	}
	snapshot, err := app.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Positions["player"] != "cafe" || snapshot.Positions["npc:tailor"] != "office" || characterInScene(snapshot.Characters, "npc:tailor") {
		t.Fatalf("committed positions: %+v characters=%+v", snapshot.Positions, snapshot.Characters)
	}
	for _, correction := range []memory.CorrectionRequest{
		{RequestKey: "rewrite-movement-action", ExpectedEpoch: snapshot.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: run.RunID + ":player-action", Replacement: "改写移动行动。"},
		{RequestKey: "rewrite-movement-result", ExpectedEpoch: snapshot.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: run.RunID + ":player-action:result:1", Replacement: "改写移动结果。"},
	} {
		if _, err := app.Correct(ctx, world.WorldID, correction); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("position dependency correction %s was not rejected: %v", correction.TargetID, err)
		}
	}
	unchanged, err := app.ReadWorld(ctx, world.WorldID, 20)
	if err != nil || unchanged.Summary.ContextEpoch != snapshot.Summary.ContextEpoch || unchanged.Positions["player"] != "cafe" {
		t.Fatalf("rejected correction changed the world: %+v err=%v", unchanged.Summary, err)
	}
	returnRun, err := app.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我返回调查事务所。", RequestKey: "return-to-office"})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, app, world.WorldID, returnRun.RunID); completed.Status != "completed" {
		t.Fatalf("return run: %+v", completed)
	}
	returned, err := app.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if returned.Positions["player"] != "office" || !characterInScene(returned.Characters, "npc:tailor") {
		t.Fatalf("returned positions: %+v characters=%+v", returned.Positions, returned.Characters)
	}
	for _, correction := range []memory.CorrectionRequest{
		{RequestKey: "rewrite-earlier-movement-action", ExpectedEpoch: returned.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: run.RunID + ":player-action", Replacement: "改写较早移动行动。"},
		{RequestKey: "rewrite-earlier-movement-result", ExpectedEpoch: returned.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: run.RunID + ":player-action:result:1", Replacement: "改写较早移动结果。"},
	} {
		if _, err := app.Correct(ctx, world.WorldID, correction); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("earlier position dependency correction %s was not rejected: %v", correction.TargetID, err)
		}
	}
	status, err := app.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := app.SaveAs(ctx, world.WorldID, "返程分支", wire.NewID("copy"), status.ActiveRevision)
	if err != nil || copy.Status != "ready" {
		t.Fatalf("save as: %+v err=%v", copy, err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: spatialWorldGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.ReadWorld(ctx, copy.TargetWorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Positions["player"] != "office" || restored.Summary.Location == nil || restored.Summary.Location.ID != "office" {
		t.Fatalf("restored position: %+v summary=%+v", restored.Positions, restored.Summary)
	}
	if restored.Definition.Capabilities["spatial"] != 1 {
		t.Fatalf("frozen capability manifest: %+v", restored.Definition.Capabilities)
	}
}

func characterInScene(characters []wiaworld.Character, entityID string) bool {
	for _, character := range characters {
		if character.EntityID == entityID {
			return character.InScene
		}
	}
	return false
}
