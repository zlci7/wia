package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type sceneCommitGenerator struct {
	calls int
}

func (g *sceneCommitGenerator) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	g.calls++
	if !strings.Contains(req.System, "集中创作") {
		return model.TextResponse{}, errors.New("candidate attempted an unexpected stage")
	}
	_, header, ok := strings.Cut(req.Input, "本轮依据：")
	if !ok {
		return model.TextResponse{}, errors.New("candidate input missing")
	}
	var basis struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal([]byte(header), &basis); err != nil {
		return model.TextResponse{}, err
	}
	beat := map[string]any{"local_id": "b1", "kind": "dialogue", "actor_id": "npc:innkeeper", "offset_minutes": 0, "basis": []string{"input:0"}, "content": "我会帮你查看登记。", "recipients": []string{}, "bystanders": []string{}, "projections": []any{}, "effects": map[string]any{}, "scope": "public"}
	draft := map[string]any{"schema_revision": "scene-draft.v1", "input_map": []any{map[string]any{"text": basis.Input, "intent_type": "speak", "addressee_id": "npc:innkeeper", "visibility": "public", "beat_ids": []string{"b1"}, "status": "succeeded", "unexecuted_reason": ""}}, "beats": []any{beat}, "narrative_blocks": []any{map[string]any{"text": "你听见老板答应帮忙查看登记，雨声暂时盖过了门外的脚步。", "beat_ids": []string{"b1"}}}, "elapsed_minutes": 0, "stop": map[string]any{"reason": "completed", "content": "得到答复。"}, "progress_updates": []any{}}
	return model.TextResponse{Text: wire.MarshalJSON(draft)}, nil
}

func insertSceneCandidateRun(t *testing.T, store *storage.WorldStore, snapshot turn.Snapshot, id string, input ...string) wiaworld.Run {
	t.Helper()
	run := wiaworld.Run{RunID: id, InputID: id + ":input-id", InputSeq: 1, Input: "请帮我查看登记。", AddresseeID: snapshot.Characters[0].EntityID, BaseTurnSeq: snapshot.Summary.TurnSeq, BaseMessageHead: snapshot.Summary.MessageHead, BaseEventHead: snapshot.Summary.EventHead, BaseContextEpoch: snapshot.Summary.ContextEpoch, BaseSceneVersion: snapshot.SceneVersion}
	if len(input) > 0 {
		run.Input = input[0]
	}
	if err := store.InTx(t.Context(), func(tx *storage.WorldTx) error {
		sequence, err := tx.MaxRunInputSequence(t.Context())
		if err != nil {
			return err
		}
		run.InputSeq = sequence + 1
		return tx.InsertRun(t.Context(), storage.RunWrite{RunID: run.RunID, RequestKey: run.RunID, InputID: run.InputID, InputSeq: run.InputSeq, Input: run.Input, AddresseeID: run.AddresseeID, Status: "running", BaseTurnSeq: run.BaseTurnSeq, BaseMessageHead: run.BaseMessageHead, BaseEventHead: run.BaseEventHead, BaseContextEpoch: run.BaseContextEpoch, BaseSceneVersion: run.BaseSceneVersion, CreatedAt: wire.NowText(), UpdatedAt: wire.NowText()})
	}); err != nil {
		t.Fatal(err)
	}
	return run
}

type sceneCandidateFixtureGenerator func(model.TextRequest) (string, error)

func (g sceneCandidateFixtureGenerator) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	text, err := g(req)
	return model.TextResponse{Text: text}, err
}

func TestSceneGeneratedEventSurvivesWindowCopyAndRestartThenSettles(t *testing.T) {
	settle, calls := false, 0
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		calls++
		if !strings.Contains(req.System, "集中创作") {
			return "", errors.New("unexpected candidate stage")
		}
		_, header, _ := strings.Cut(req.Input, "本轮依据：")
		var basis struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal([]byte(header), &basis); err != nil {
			return "", err
		}
		a := map[string]any{"local_id": "b1", "kind": "action_result", "actor_id": "player", "offset_minutes": 2, "basis": []string{"input:0"}, "content": "玩家抵达检修间。", "recipients": []string{"player"}, "bystanders": []string{}, "projections": []any{map[string]any{"recipient": "player", "content": "我来到检修间。"}}, "effects": map[string]any{"movements": []any{map[string]any{"entity_id": "player", "from": "control", "to": "workshop", "route": []string{"control", "workshop"}}}}, "status": "succeeded", "attempt": map[string]any{"content": basis.Input, "input_fragment_index": 0}}
		b := map[string]any{"local_id": "b2", "kind": "world_change", "actor_id": "world", "offset_minutes": 2, "basis": []string{"beat:b1"}, "content": "检修间屏幕出现待核对的标签。", "recipients": []string{"player"}, "bystanders": []string{}, "projections": []any{map[string]any{"recipient": "player", "content": "屏幕显示一张待核对的标签。"}}, "effects": map[string]any{}}
		elapsed, status, progress := 2, "succeeded", []any{}
		if settle {
			a = map[string]any{"local_id": "b1", "kind": "observation", "actor_id": "player", "offset_minutes": 5, "basis": []string{"input:0"}, "content": "玩家等到核对窗口结束。", "recipients": []string{"player"}, "bystanders": []string{}, "projections": []any{map[string]any{"recipient": "player", "content": "我等到窗口结束。"}}, "effects": map[string]any{}}
			b["offset_minutes"], b["basis"], b["content"] = 5, []string{"scene-offer:scene:2:world"}, "未核对的标签留待复查。"
			b["projections"] = []any{map[string]any{"recipient": "player", "content": "窗口结束，标签仍留待复查。"}}
			elapsed = 5
			progress = []any{map[string]any{"type": "generated_event", "id": "scene-offer:input-id", "status": "occurred", "content": "核对窗口结束。", "offset_minutes": 5, "basis": []string{"beat:b2"}, "beat_ids": []string{"b2"}}}
		}
		d := map[string]any{"schema_revision": "scene-draft.v1", "input_map": []any{map[string]any{"text": basis.Input, "intent_type": "act", "addressee_id": "npc:innkeeper", "visibility": "public", "beat_ids": []string{"b1", "b2"}, "status": status, "unexecuted_reason": ""}}, "beats": []any{a, b}, "narrative_blocks": []any{map[string]any{"text": "你看完检修间屏幕，继续处理自己的事情。", "beat_ids": []string{"b1", "b2"}}}, "elapsed_minutes": elapsed, "stop": map[string]any{"reason": "completed", "content": "观察结束。"}, "progress_updates": progress}
		if !settle {
			d["event_offer"] = map[string]any{"trigger_beat_id": "b1", "kind": "arrival", "location": "workshop", "condition": "五分钟后核对窗口结束。", "development": "未核对的标签留待复查。", "after_minutes": 5, "initial_beat_ids": []string{"b2"}}
		}
		return wire.MarshalJSON(d), nil
	})
	packRoot := packFixture(t, "orbital-repair")
	rewritePack(t, packRoot, func(p map[string]any) {
		p["schema_version"], p["requires"], p["plot"], p["bystanders"] = 3, map[string]int{"spatial": 1}, nil, []any{}
		delete(p, "plot")
		for _, location := range p["locations"].([]any) {
			location.(map[string]any)["kind"] = "place"
		}
	})
	pack, err := loadPack(packRoot)
	if err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, g)
	a.SetPack("orbital-repair", pack)
	w := createPackWorld(t, a, "orbital-repair")
	path, _, err := a.worldRecord(t.Context(), w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := readContextSnapshot(t, a, w.WorldID)
	run := insertSceneCandidateRun(t, store, snapshot, "scene-offer", "我到检修间看屏幕。")
	out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil || report.CoreCalls != 1 {
		t.Fatal("offer candidate failed", report, err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	snapshot, err = turn.LoadInputSnapshot(t.Context(), store, turn.LoadSnapshotLimit)
	if err != nil || len(snapshot.GeneratedEvents.Active) != 1 {
		t.Fatal("committed active event missing", err)
	}
	start := snapshot.GeneratedEvents.Active[0].StartID
	if err := store.InTx(t.Context(), func(tx *storage.WorldTx) error {
		for index := range 80 {
			if err := tx.InsertEvent(t.Context(), storage.EventWrite{Seq: snapshot.Summary.EventHead + int64(index) + 1, EventID: fmt.Sprintf("old-window:%d", index), EventType: "audit", ActorID: "world", Content: "既有日志", CreatedAt: wire.NowText()}); err != nil {
				return err
			}
		}
		return tx.SetMeta(t.Context(), "event_head", strconv.FormatInt(snapshot.Summary.EventHead+80, 10))
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	snapshot = readContextSnapshot(t, a, w.WorldID)
	if slices.ContainsFunc(snapshot.Events, func(e wiaworld.Event) bool { return e.EventID == start }) {
		t.Fatal("source has not left recent global window")
	}
	if _, exists := snapshot.Sources[start]; !exists {
		t.Fatal("active event start metadata lost outside recent window")
	}
	appStatus, err := a.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	copy, err := a.SaveAs(t.Context(), w.WorldID, "开放事件另存", "scene-event-copy", appStatus.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	root := a.DataRoot()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), Options{DataRoot: root, UserID: LocalUserID, Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	settle = true
	for index, id := range []string{w.WorldID, copy.TargetWorldID} {
		snapshot = readContextSnapshot(t, reopened, id)
		if len(snapshot.GeneratedEvents.Active) != 1 || snapshot.GeneratedEvents.Active[0].StartID != start {
			t.Fatal("copy/restart lost active event origin")
		}
		path, _, err = reopened.worldRecord(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		store, err = storage.OpenWorldDB(path)
		if err != nil {
			t.Fatal(err)
		}
		run = insertSceneCandidateRun(t, store, snapshot, fmt.Sprintf("scene-finish-%d", index), "我等五分钟看看标签。")
		out, report, err = reopened.turnService().BuildSceneCandidate(t.Context(), store, run, g)
		if err != nil || report.CoreCalls != 1 {
			t.Fatal("resumed event candidate failed", report, err)
		}
		if err := commitSceneCandidate(t, store, run, out); err != nil {
			t.Fatal("event parent source failed SQL commit", err)
		}
		store.Close()
		after := readContextSnapshot(t, reopened, id)
		if len(after.GeneratedEvents.Active) != 0 || after.GeneratedEvents.Completed != 1 || after.Summary.StoryEnded || after.Summary.Clock != "第 1 日 09:07" {
			t.Fatalf("resumed event state inconsistent: %+v", after.GeneratedEvents)
		}
	}
	if calls != 3 {
		t.Fatal("read/copy/restart generated extra scenes", calls)
	}
}

type sceneTradeGenerator struct {
	revision string
	calls    int
}

func (g *sceneTradeGenerator) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	g.calls++
	if !strings.Contains(req.System, "集中创作") {
		return model.TextResponse{}, errors.New("unexpected trade model purpose")
	}
	_, header, _ := strings.Cut(req.Input, "本轮依据：")
	var basis struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal([]byte(header), &basis); err != nil {
		return model.TextResponse{}, err
	}
	projection := []any{map[string]any{"recipient": "player", "content": "我支付了登记费，并交付检修扳手。"}, map[string]any{"recipient": "npc:engineer", "content": "维修员支付登记费并将检修扳手交给了我。"}}
	action := map[string]any{"local_id": "b1", "kind": "action_result", "actor_id": "player", "offset_minutes": 10, "basis": []string{"input:0"}, "content": "玩家支付1250分并交付扳手。", "recipients": []string{"player", "npc:engineer"}, "bystanders": []string{}, "projections": projection, "status": "succeeded", "attempt": map[string]any{"content": basis.Input, "input_fragment_index": 0}, "effects": map[string]any{"state_effects": []any{map[string]any{"entity_id": "player", "state_id": "credits", "delta": -1250}, map[string]any{"entity_id": "player", "state_id": "fatigue", "delta": 1}}, "item_transfers": []any{map[string]any{"instance_id": "wrench-1", "from_holder_id": "player", "to_holder_id": "npc:engineer"}}}}
	reply := map[string]any{"local_id": "b2", "kind": "dialogue", "actor_id": "npc:engineer", "offset_minutes": 10, "basis": []string{"beat:b1"}, "content": "登记完成，我会复查这笔交接。", "scope": "public", "recipients": []string{}, "bystanders": []string{}, "projections": []any{}, "effects": map[string]any{"plan_updates": []any{map[string]any{"owner_id": "npc:engineer", "local_plan_id": "receipt", "content": "复查本次登记交接", "status": "active", "review_after_minutes": 30, "basis": []string{"beat:b1"}}}}}
	draft := map[string]any{"schema_revision": "scene-draft.v1", "input_map": []any{map[string]any{"text": basis.Input, "intent_type": "act", "addressee_id": "npc:engineer", "visibility": "public", "beat_ids": []string{"b1", "b2"}, "status": "succeeded", "unexecuted_reason": ""}}, "beats": []any{action, reply}, "narrative_blocks": []any{map[string]any{"text": "你支付登记费，将扳手交给林澈。他登记后答应复查交接。", "beat_ids": []string{"b1", "b2"}}}, "elapsed_minutes": 10, "stop": map[string]any{"reason": "completed", "content": "登记与交付完成。"}, "progress_updates": []any{map[string]any{"type": "development", "id": "maintenance-pressure", "status": "deferred", "content": "当前运输机状况没有新变化。", "offset_minutes": 10, "basis": []string{"material:" + g.revision + ":maintenance-development"}, "beat_ids": []string{}}}}
	return model.TextResponse{Text: wire.MarshalJSON(draft)}, nil
}

func TestSceneV4CandidateCommitsMoneyItemsPlansAndSources(t *testing.T) {
	packRoot, dataRoot := repairInformationFixture(t), t.TempDir()
	g := &sceneTradeGenerator{revision: "repair-station.test.v1"}
	a := openRepairInformationApp(t, dataRoot, packRoot, g)
	w := createRepairInformationWorld(t, a, "candidate-v4-create")
	snapshot := readContextSnapshot(t, a, w.WorldID)
	path, _, err := a.worldRecord(t.Context(), w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, snapshot, "candidate-trade", "我支付登记费，并把检修扳手交给林澈。")
	out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil {
		t.Fatal(err)
	}
	if report.CoreCalls != 1 || g.calls != 1 {
		t.Fatalf("trade core calls: %+v", report)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	store.Close()
	after := requireSceneTradeWorld(t, a, w.WorldID)
	var plan wiaworld.PersonalPlan
	for _, p := range after.OpenProgress.Plans {
		if strings.HasSuffix(p.ID, ":receipt") {
			plan = p
		}
	}
	if plan.OwnerID != "npc:engineer" || len(plan.SourceIDs) != 1 || !strings.HasSuffix(plan.SourceIDs[0], ":projection:npc:engineer") {
		t.Fatalf("plan origin is not durable owned perception: %+v", plan)
	}
	status, err := a.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	copy, err := a.SaveAs(t.Context(), w.WorldID, "候选交易另存", "candidate-trade-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), Options{DataRoot: dataRoot, UserID: LocalUserID, StoryPacksPath: filepath.Dir(packRoot), Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, id := range []string{w.WorldID, copy.TargetWorldID} {
		saved := requireSceneTradeWorld(t, reopened, id)
		if !slices.ContainsFunc(saved.OpenProgress.Plans, func(p wiaworld.PersonalPlan) bool {
			return p.ID == plan.ID && slices.Equal(p.SourceIDs, plan.SourceIDs)
		}) || saved.OpenProgress.DevelopmentChecks["maintenance-pressure"] == "" {
			t.Fatal("world assessment or plan source lost after copy/restart")
		}
		_, err := reopened.Correct(t.Context(), id, memory.CorrectionRequest{RequestKey: "protect-scene-trade", ExpectedEpoch: saved.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: run.RunID + ":scene:1:action:result:1", Replacement: "这次付款没有发生。"})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("candidate protected settlement source became correctable", err)
		}
	}
}

func requireSceneTradeWorld(t *testing.T, a *App, id string) turn.Snapshot {
	t.Helper()
	saved, err := a.ReadWorld(t.Context(), id, 20)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Summary.Clock != "2190-01-01 00:05" || saved.States["player"]["credits"].Value.Integer != 8750 || saved.States["player"]["fatigue"].Value.Integer != 6 || saved.Items["wrench-1"].HolderID != "npc:engineer" || saved.Items["access-card-1"].HolderID != "player" || len(saved.OpenProgress.Plans) != 2 {
		t.Fatalf("candidate committed world inconsistent: %+v", saved.Summary)
	}
	return saved
}

func commitSceneCandidate(t *testing.T, store *storage.WorldStore, run wiaworld.Run, out turn.Output) error {
	t.Helper()
	_, err := commitTurn(t.Context(), store, run, out.Narrative, out.Events, out.Perceptions, out.Memories, out.Clock, out.Scene, out.SceneLocation, out.SceneVersion, out.SceneCharacters, out.SceneViews, out.PositionChanges, out.StateChanges, out.RelationshipChanges, out.ItemTransfers, out.ActionResolution, out.PlotProgress, out.OpenProgress, out.GeneratedEvents)
	return err
}

func TestSceneCandidateUsesExistingCommitAndSurvivesCopyRestart(t *testing.T) {
	g := &sceneCommitGenerator{}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "候选兼容", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := readContextSnapshot(t, a, w.WorldID)
	path, _, err := a.worldRecord(t.Context(), w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, snapshot, "candidate-commit")
	out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil {
		t.Fatal(err)
	}
	if g.calls != 1 || report.CoreCalls != 1 {
		t.Fatalf("ordinary candidate calls: %+v", report)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	committed, found, err := store.ReadRun(t.Context(), run.RunID)
	if err != nil || !found || committed.Status != "completed" || committed.MessageSeq == 0 {
		t.Fatal("committed run lookup failed", err)
	}
	if err := commitSceneCandidate(t, store, run, out); !errors.Is(err, ErrWorldBusy) {
		t.Fatal("completed run was applied twice", err)
	}
	store.Close()
	status, err := a.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	copy, err := a.SaveAs(t.Context(), w.WorldID, "候选另存", "candidate-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	root := a.DataRoot()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), Options{DataRoot: root, UserID: LocalUserID, Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, id := range []string{w.WorldID, copy.TargetWorldID} {
		saved := readContextSnapshot(t, reopened, id)
		if saved.Summary.TurnSeq != 1 || saved.Summary.Clock != snapshot.Summary.Clock || !strings.Contains(wire.MarshalJSON(saved.Perceptions["npc:mercenary"]), "帮你查看登记") {
			t.Fatal("candidate state or personal projection lost after restart")
		}
	}
}

func TestSceneCandidateStaleEpochRejectsWholeCommit(t *testing.T) {
	g := &sceneCommitGenerator{}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "候选陈旧", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := readContextSnapshot(t, a, w.WorldID)
	path, _, err := a.worldRecord(t.Context(), w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, snapshot, "candidate-stale")
	out, _, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InTx(t.Context(), func(tx *storage.WorldTx) error { return tx.SetMeta(t.Context(), "context_epoch", "2") }); err != nil {
		t.Fatal(err)
	}
	if err := commitSceneCandidate(t, store, run, out); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("stale candidate committed", err)
	}
	saved, err := turn.LoadSnapshot(t.Context(), store, 40)
	if err != nil || saved.Summary.TurnSeq != 0 || saved.Summary.EventHead != snapshot.Summary.EventHead || saved.SceneVersion != snapshot.SceneVersion {
		t.Fatal("stale candidate partially changed world", err)
	}
}

func TestSceneCurrentV4PackFitsCandidateContextAndPreservesWorld(t *testing.T) {
	core, intent := 0, 0
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		if strings.Contains(req.System, "结构化回合意图") {
			intent++
			return `{"intent_type":"speak","addressee_id":"npc:tailor","visibility":"public"}`, nil
		}
		if !strings.Contains(req.System, "集中创作") {
			return "", errors.New("unexpected current-pack model purpose")
		}
		core++
		_, header, _ := strings.Cut(req.Input, "本轮依据：")
		var basis struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal([]byte(header), &basis); err != nil {
			return "", err
		}
		beat := map[string]any{"local_id": "b1", "kind": "dialogue", "actor_id": "npc:tailor", "offset_minutes": 0, "basis": []string{"input:0"}, "content": "我先回忆一下最后见面的情况。", "scope": "public", "recipients": []string{}, "bystanders": []string{}, "projections": []any{}, "effects": map[string]any{}}
		d := map[string]any{"schema_revision": "scene-draft.v1", "input_map": []any{map[string]any{"text": basis.Input, "intent_type": "speak", "addressee_id": "npc:tailor", "visibility": "public", "beat_ids": []string{"b1"}, "status": "succeeded", "unexecuted_reason": ""}}, "beats": []any{beat}, "narrative_blocks": []any{map[string]any{"text": "你听见马丁答应先回忆最后一次见面的情况。", "beat_ids": []string{"b1"}}}, "elapsed_minutes": 0, "stop": map[string]any{"reason": "completed", "content": "得到回应。"}, "progress_updates": []any{map[string]any{"type": "development", "id": "case-pressure", "status": "deferred", "content": "失踪调查尚无新变化。", "offset_minutes": 0, "basis": []string{"material:mist-embers.pack.v9:case-development"}, "beat_ids": []string{}}}}
		return wire.MarshalJSON(d), nil
	})
	root := t.TempDir()
	installTestPacks(t, root)
	logger := &recordingLogger{}
	a, err := Open(t.Context(), Options{DataRoot: root, UserID: LocalUserID, Generator: g, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w := createPackWorld(t, a, "mist-embers")
	snapshot := readContextSnapshot(t, a, w.WorldID)
	path, _, err := a.worldRecord(t.Context(), w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	addressed := snapshot
	addressed.Characters = slices.Clone(snapshot.Characters)
	for _, c := range snapshot.Characters {
		if c.EntityID == "npc:tailor" {
			addressed.Characters[0] = c
		}
	}
	run := insertSceneCandidateRun(t, store, addressed, "current-pack-candidate", "我问最后一次见面发生了什么。")
	out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	for _, line := range strings.Split(logger.String(), "\n") {
		if start := strings.Index(line, " input_tokens="); start >= 0 && strings.Contains(line, `purpose="scene"`) {
			fields, _, _ := strings.Cut(line[start:], " selected_source_ids=")
			t.Log("candidate context:", fields)
		}
	}
	if err != nil || report.CoreCalls > 2 || report.Repairs != 0 || intent != 1 || core != report.CoreCalls {
		t.Fatal("current frozen pack cannot form a bounded scene", report, err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	if after.Summary.Clock != "1358-03-29 09:00" || wire.MarshalJSON(after.States) != wire.MarshalJSON(snapshot.States) || wire.MarshalJSON(after.Items) != wire.MarshalJSON(snapshot.Items) || len(after.OpenProgress.Plans) != len(snapshot.OpenProgress.Plans) || after.OpenProgress.DevelopmentChecks["case-pressure"] == "" {
		t.Fatal("ordinary scene changed current money, items, clock or plans")
	}
}
