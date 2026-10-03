package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

// repairInformationGenerator is a deterministic test double, not a real-model
// playthrough. The package's setting and facts remain authored v4 data.
type repairInformationGenerator struct {
	mu              sync.Mutex
	requests        []model.TextRequest
	invalidTransfer bool
	overdraw        bool
}

func (g *repairInformationGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, request)
	g.mu.Unlock()
	switch {
	case strings.Contains(request.System, "结构化回合意图"):
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"npc:engineer","visibility":"public"}`}, nil
	case strings.Contains(request.System, "重要 NPC"):
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":"维修员核对了维修间的登记。","relationship_proposals":[]}`}, nil
	case strings.Contains(request.System, "持续世界协调器"):
		return deferredOpenWorldResponse(request)
	case strings.Contains(request.System, "场景协调 Agent"):
		marker := "待裁定行动(JSON)："
		_, remainder, found := strings.Cut(request.Input, marker)
		var actions []wiaworld.Event
		if !found || json.Unmarshal([]byte(strings.SplitN(remainder, "\n", 2)[0]), &actions) != nil || len(actions) != 1 {
			return model.TextResponse{}, errors.New("repair fixture needs one resolved player action")
		}
		action := actions[0]
		charge := -1250
		transfers := []map[string]any{{"instance_id": "wrench-1", "from_holder_id": "player", "to_holder_id": "npc:engineer", "action_id": action.EventID}}
		text := "维修员支付了12积分50分登记费，并把检修扳手交给林澈。"
		if strings.Contains(action.Content, "只结算") {
			charge, transfers, text = -100, []map[string]any{}, "维修员支付了1积分登记费。"
		}
		if g.invalidTransfer {
			transfers[0]["instance_id"] = "missing-tool"
		}
		if g.overdraw {
			charge = -20000
		}
		body, err := json.Marshal(map[string]any{
			"time_minutes": 10, "scene": "维修间", "scene_characters": []string{"npc:engineer"},
			"outcomes":      []map[string]any{{"action_id": action.EventID, "status": "succeeded", "content": text, "projections": outcomeProjectionFixture(text, []string{"player", "npc:engineer"}), "recipients": []string{"player", "npc:engineer"}, "bystanders": []string{}}},
			"scene_updates": []any{}, "movements": []any{}, "relationship_effects": []any{},
			"state_effects":  []map[string]any{{"entity_id": "player", "state_id": "credits", "delta": charge, "action_id": action.EventID}, {"entity_id": "player", "state_id": "fatigue", "delta": 1, "action_id": action.EventID}},
			"item_transfers": transfers,
		})
		return model.TextResponse{Text: string(body)}, err
	case strings.Contains(request.System, "玩家正文 Agent"):
		return model.TextResponse{Text: "你核对完登记，与林澈确认了这次实际结算。"}, nil
	default:
		return model.TextResponse{}, errors.New("unexpected repair fixture model stage")
	}
}

func repairInformationFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repair-station")
	source := filepath.Join("..", "content", "testdata", "repair-station")
	err := fs.WalkDir(os.DirFS(source), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		body, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func openRepairInformationApp(t *testing.T, dataRoot, packRoot string, generator model.TextGenerator) *App {
	t.Helper()
	a, err := Open(context.Background(), Options{DataRoot: dataRoot, UserID: LocalUserID, StoryPacksPath: filepath.Dir(packRoot), Generator: generator})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func createRepairInformationWorld(t *testing.T, a *App, key string) wiaworld.WorldSummary {
	t.Helper()
	game, err := a.Game("repair-station")
	if err != nil {
		t.Fatalf("repair package unavailable: %v; issues=%+v", err, a.PackIssues())
	}
	world, err := a.CreateStoryWorld(context.Background(), CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: key, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	return world
}

func requireRepairInformation(t *testing.T, a *App, worldID, clock string, credits, fatigue int, holder string) turn.Snapshot {
	t.Helper()
	snapshot, err := a.ReadWorld(context.Background(), worldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.Clock != clock || snapshot.Summary.Calendar == nil || *snapshot.Summary.Calendar != (wiaworld.Calendar{Kind: "gregorian", Era: "联合历"}) {
		t.Fatalf("world time=%+v", snapshot.Summary)
	}
	if snapshot.States["player"]["credits"].Value.Integer != credits || snapshot.States["player"]["fatigue"].Value.Integer != fatigue || snapshot.Items["wrench-1"].HolderID != holder || snapshot.Items["access-card-1"].HolderID != "player" {
		t.Fatalf("world assets states=%+v items=%+v", snapshot.States, snapshot.Items)
	}
	planMinute, err := plot.ClockMinute("2190-01-01 01:55")
	if err != nil || snapshot.OpenProgress == nil || len(snapshot.OpenProgress.Plans) != 1 || snapshot.OpenProgress.Plans[0].NextCheck != planMinute {
		t.Fatalf("saved personal plan time=%+v err=%v", snapshot.OpenProgress, err)
	}
	states := turn.PlayerStateProjection(snapshot)
	if len(states) != 2 {
		t.Fatalf("player states=%+v", states)
	}
	wantCash := map[int]string{10000: "100 积分", 8750: "87 积分 50 分", 8650: "86 积分 50 分", 44444: "444 积分 44 分"}[credits]
	for _, state := range states {
		if state.StateID == "credits" && (state.Category != "resource" || state.Currency == nil || state.Currency.Name != "维修站积分" || state.DisplayValue != wantCash) {
			t.Fatalf("cash projection=%+v", state)
		}
		if state.StateID == "fatigue" && state.Category != "condition" {
			t.Fatalf("body projection=%+v", state)
		}
	}
	locations := turn.PlayerLocationProjection(snapshot)
	if len(locations) != 2 || locations[0].ID != "cargo-locker" || locations[1].ID != "service-bay" {
		t.Fatalf("known locations=%+v", locations)
	}
	return snapshot
}

func TestWorldInformationNonMysteryPackageAndChangedSaveContinuity(t *testing.T) {
	ctx := context.Background()
	packRoot := repairInformationFixture(t)
	loaded, err := content.Load(packRoot)
	if err != nil || loaded.Definition.SchemaVersion != 4 || loaded.Definition.Progression == nil || len(loaded.Definition.Materials) != 4 {
		t.Fatalf("v4 package=%+v err=%v", loaded.Definition, err)
	}
	dataRoot, generator := t.TempDir(), &repairInformationGenerator{}
	a := openRepairInformationApp(t, dataRoot, packRoot, generator)
	world := createRepairInformationWorld(t, a, "repair-create")
	before := requireRepairInformation(t, a, world.WorldID, "2189-12-31 23:55", 10000, 5, "player")
	if before.PlayerName != "维修员" || len(turn.PlayerItemProjection(before)) != 2 || len(before.Characters) != 1 {
		t.Fatalf("initial player view=%+v", before)
	}
	request := RunRequest{RequestKey: "repair-payment", Input: "我支付登记费，并把检修扳手交给林澈。", AddresseeID: "npc:engineer"}
	run, err := a.SubmitRun(ctx, world.WorldID, request)
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, a, world.WorldID, run.RunID); completed.Status != "completed" {
		t.Fatalf("run=%+v", completed)
	}
	after := requireRepairInformation(t, a, world.WorldID, "2190-01-01 00:05", 8750, 6, "npc:engineer")
	if after.Summary.TurnSeq != 1 || after.States["player"]["credits"].Version != 2 || after.Items["wrench-1"].Version != 2 {
		t.Fatalf("committed versions=%+v/%+v", after.States, after.Items)
	}
	replayed, err := a.SubmitRun(ctx, world.WorldID, request)
	if err != nil || replayed.RunID != run.RunID {
		t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
	}
	requireRepairInformation(t, a, world.WorldID, "2190-01-01 00:05", 8750, 6, "npc:engineer")
	path, _, err := a.worldRecord(ctx, world.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	var stateChanges, transfers int
	err = store.TestingDB().QueryRow(`SELECT (SELECT COUNT(*) FROM state_changes),(SELECT COUNT(*) FROM item_transfers)`).Scan(&stateChanges, &transfers)
	_ = store.Close()
	if err != nil || stateChanges != 2 || transfers != 1 {
		t.Fatalf("atomic histories=%d/%d err=%v", stateChanges, transfers, err)
	}
	status, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := a.SaveAs(ctx, world.WorldID, "维修站分支", "repair-copy", status.ActiveRevision)
	if err != nil || copy.Status != "ready" {
		t.Fatalf("changed-world copy=%+v err=%v", copy, err)
	}
	requireRepairInformation(t, a, copy.TargetWorldID, "2190-01-01 00:05", 8750, 6, "npc:engineer")
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openRepairInformationApp(t, dataRoot, packRoot, generator)
	for _, id := range []string{world.WorldID, copy.TargetWorldID} {
		requireRepairInformation(t, reopened, id, "2190-01-01 00:05", 8750, 6, "npc:engineer")
	}
	status, err = reopened.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.ActivateWorld(ctx, copy.TargetWorldID, status.ActiveRevision, "repair-activate-copy"); err != nil {
		t.Fatal(err)
	}
	branchRun, err := reopened.SubmitRun(ctx, copy.TargetWorldID, RunRequest{RequestKey: "branch-payment", Input: "我只结算一积分登记费。", AddresseeID: "npc:engineer"})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRun(t, reopened, copy.TargetWorldID, branchRun.RunID); completed.Status != "completed" {
		t.Fatalf("branch=%+v", completed)
	}
	requireRepairInformation(t, reopened, copy.TargetWorldID, "2190-01-01 00:15", 8650, 7, "npc:engineer")
	requireRepairInformation(t, reopened, world.WorldID, "2190-01-01 00:05", 8750, 6, "npc:engineer")
	generator.mu.Lock()
	requests := append([]model.TextRequest(nil), generator.requests...)
	generator.mu.Unlock()
	timeStages := map[string]bool{}
	for _, request := range requests {
		text := request.System + request.Input
		for _, stage := range []string{"结构化回合意图", "重要 NPC", "场景协调 Agent", "玩家正文 Agent", "持续世界协调器"} {
			if !strings.Contains(request.System, stage) {
				continue
			}
			absoluteTime := false
			for _, clock := range []string{"2189-12-31 23:55", "2190-01-01 00:05", "2190-01-01 00:15"} {
				absoluteTime = absoluteTime || strings.Contains(request.Input, "时间："+clock)
			}
			if !absoluteTime {
				t.Fatalf("%s request omitted its authoritative world date", stage)
			}
			timeStages[stage] = true
		}
		for _, forbidden := range []string{"诡秘", "鲁恩", "贝克兰德", "金镑", "银镜", "第 1 日"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("non-mystery context contains %q", forbidden)
			}
		}
	}
	for _, stage := range []string{"结构化回合意图", "重要 NPC", "场景协调 Agent", "玩家正文 Agent"} {
		if !timeStages[stage] {
			t.Fatalf("missing world date verification at %s", stage)
		}
	}
}

func TestWorldInformationInvalidSettlementLeavesAllFactsUnchanged(t *testing.T) {
	for _, failure := range []string{"invalid-transfer", "insufficient-cash"} {
		t.Run(failure, func(t *testing.T) {
			generator := &repairInformationGenerator{invalidTransfer: failure == "invalid-transfer", overdraw: failure == "insufficient-cash"}
			a := openRepairInformationApp(t, t.TempDir(), repairInformationFixture(t), generator)
			world := createRepairInformationWorld(t, a, "invalid-create")
			before := requireRepairInformation(t, a, world.WorldID, "2189-12-31 23:55", 10000, 5, "player")
			run, err := a.SubmitRun(context.Background(), world.WorldID, RunRequest{RequestKey: failure, Input: "我支付登记费并交出扳手。", AddresseeID: "npc:engineer"})
			if err != nil {
				t.Fatal(err)
			}
			if failed := waitRun(t, a, world.WorldID, run.RunID); failed.Status != "failed" || failed.Reason != "coordination_generation_failed" {
				t.Fatalf("invalid settlement failure=%+v", failed)
			}
			after := requireRepairInformation(t, a, world.WorldID, "2189-12-31 23:55", 10000, 5, "player")
			if after.Summary.TurnSeq != before.Summary.TurnSeq || after.Summary.ContextEpoch != before.Summary.ContextEpoch || after.Summary.MessageHead != before.Summary.MessageHead || !reflect.DeepEqual(after.Items, before.Items) || !reflect.DeepEqual(after.States, before.States) {
				t.Fatal("invalid combined settlement changed committed facts")
			}
		})
	}
}

func TestWorldInformationRelativeSaveKeepsFrozenContractAfterNewPackage(t *testing.T) {
	ctx := context.Background()
	packRoot, dataRoot := repairInformationFixture(t), t.TempDir()
	statePath := filepath.Join(packRoot, "world", "state.json")
	newStates, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var states map[string][]map[string]any
	if err = json.Unmarshal(newStates, &states); err != nil {
		t.Fatal(err)
	}
	for _, state := range states["state_definitions"] {
		delete(state, "category")
		delete(state, "currency")
	}
	relativeStates, _ := json.Marshal(states)
	if err = os.WriteFile(statePath, relativeStates, 0644); err != nil {
		t.Fatal(err)
	}
	rewritePack(t, packRoot, func(pack map[string]any) {
		pack["revision"], pack["clock"] = "repair-station.test.relative", "第 1 日 23:55"
		delete(pack, "calendar")
		delete(pack["requires"].(map[string]any), "world_info")
		delete(pack["player"].(map[string]any), "known_locations")
	})
	a := openRepairInformationApp(t, dataRoot, packRoot, &repairInformationGenerator{})
	old := createRepairInformationWorld(t, a, "relative-create")
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(statePath, newStates, 0644); err != nil {
		t.Fatal(err)
	}
	rewritePack(t, packRoot, func(pack map[string]any) {
		pack["revision"], pack["clock"] = "repair-station.test.v2", "2189-12-31 23:55"
		pack["calendar"] = map[string]any{"kind": "gregorian", "era": "联合历"}
		pack["requires"].(map[string]any)["world_info"] = 1
		player := pack["player"].(map[string]any)
		player["known_locations"] = []string{"service-bay", "cargo-locker"}
		player["initial_state"].(map[string]any)["credits"] = 44444
	})
	reopened := openRepairInformationApp(t, dataRoot, packRoot, &repairInformationGenerator{})
	oldSnapshot, err := reopened.ReadWorld(ctx, old.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if oldSnapshot.Summary.Clock != "第 1 日 23:55" || oldSnapshot.Summary.Calendar != nil || oldSnapshot.Definition.Calendar != nil || oldSnapshot.Definition.Capabilities["world_info"] != 0 || oldSnapshot.States["player"]["credits"].Value.Integer != 10000 {
		t.Fatalf("old save adopted new authored facts: %+v", oldSnapshot.Summary)
	}
	for _, state := range turn.PlayerStateProjection(oldSnapshot) {
		if state.Currency != nil || state.Category != "" {
			t.Fatalf("old state acquired new projection contract: %+v", state)
		}
	}
	newWorld := createRepairInformationWorld(t, reopened, "absolute-create")
	requireRepairInformation(t, reopened, newWorld.WorldID, "2189-12-31 23:55", 44444, 5, "player")
	if minute, err := plot.ClockMinute(oldSnapshot.Summary.Clock); err != nil || minute != 23*60+55 {
		t.Fatalf("old clock parser=%d err=%v", minute, err)
	}
	if oldSnapshot.Summary.Revision != "repair-station.test.relative" || newWorld.Revision != "repair-station.test.v2" {
		t.Fatalf("frozen revisions=%s/%s", oldSnapshot.Summary.Revision, newWorld.Revision)
	}
}
