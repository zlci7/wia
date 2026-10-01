package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
)

type repeatedStateGenerator struct{ base stageBGenerator }

func (g *repeatedStateGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	response, err := g.base.GenerateText(ctx, request)
	if err != nil || !strings.Contains(request.System, "场景协调 Agent") {
		return response, err
	}
	var body map[string]any
	if err = json.Unmarshal([]byte(response.Text), &body); err != nil {
		return response, err
	}
	effects := body["state_effects"].([]any)
	body["state_effects"] = append(effects, effects[0])
	encoded, err := json.Marshal(body)
	response.Text = string(encoded)
	return response, err
}

func TestRepeatedStateEffectsPersistAndRetryDoesNotRepeat(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: &repeatedStateGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.Close() }()
	game, err := a.Game("mist-embers")
	if err != nil {
		t.Fatal(err)
	}
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: "create", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	request := RunRequest{Input: "核对记录。", AddresseeID: "npc:tailor", RequestKey: "repeat-effects"}
	run, err := a.SubmitRun(ctx, world.WorldID, request)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, world.WorldID, run.RunID); done.Status != "completed" {
		t.Fatalf("run=%+v", done)
	}
	if again, err := a.SubmitRun(ctx, world.WorldID, request); err != nil || again.RunID != run.RunID {
		t.Fatalf("retry=%+v err=%v", again, err)
	}
	path, _, err := a.worldRecord(ctx, world.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	err = store.TestingDB().QueryRow(`SELECT COUNT(*) FROM state_changes WHERE state_id='fatigue'`).Scan(&count)
	store.Close()
	if err != nil || count != 2 {
		t.Fatalf("changes=%d err=%v", count, err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Open(ctx, Options{DataRoot: root, UserID: LocalUserID, Generator: &repeatedStateGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.States["player"]["fatigue"].Value.Integer; got != 22 {
		t.Fatalf("persisted fatigue=%d", got)
	}
}

type ruleWithoutEffectsGenerator struct{ base stageCRiskGenerator }

func (g *ruleWithoutEffectsGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	response, err := g.base.GenerateText(ctx, request)
	if err != nil || !strings.Contains(request.System, "场景协调 Agent") {
		return response, err
	}
	var body map[string]any
	if err = json.Unmarshal([]byte(response.Text), &body); err != nil {
		return response, err
	}
	body["state_effects"] = []any{}
	encoded, err := json.Marshal(body)
	response.Text = string(encoded)
	return response, err
}

func TestCorrectionProtectsSettledRuleWithoutStateEffects(t *testing.T) {
	ctx := context.Background()
	a, err := Open(ctx, Options{DataRoot: t.TempDir(), UserID: LocalUserID, Generator: stageBItemGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	game, err := a.Game("mist-embers")
	if err != nil {
		t.Fatal(err)
	}
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: "create", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	move, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "前往诊所。", RequestKey: "move"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, world.WorldID, move.RunID); done.Status != "completed" {
		t.Fatalf("move=%+v", done)
	}
	a.modelMu.Lock()
	a.generator = &ruleWithoutEffectsGenerator{}
	a.modelMu.Unlock()
	run, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "潜入地下室。", RequestKey: "risk"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, world.WorldID, run.RunID); done.Status != "completed" {
		t.Fatalf("risk=%+v", done)
	}
	snapshot, err := a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Correct(ctx, world.WorldID, memory.CorrectionRequest{RequestKey: "correct", ExpectedEpoch: snapshot.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: run.RunID + ":player-action:result:1", Replacement: "没有执行该行动。"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("correction=%v", err)
	}
	after, err := a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if after.Summary.ContextEpoch != snapshot.Summary.ContextEpoch {
		t.Fatal("rejected correction changed epoch")
	}
}
