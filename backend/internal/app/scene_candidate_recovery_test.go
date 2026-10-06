package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
)

func TestSceneCandidatePreservesFrozenV1V2Contracts(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			root := packFixture(t, GameID)
			rewritePack(t, root, func(p map[string]any) { p["schema_version"] = version })
			pack, err := loadPack(root)
			if err != nil {
				t.Fatal(err)
			}
			g := &sceneCommitGenerator{}
			a := newTestApp(t, g)
			a.SetPack(GameID, pack)
			w := createPackWorld(t, a, GameID)
			before := readContextSnapshot(t, a, w.WorldID)
			path, _, _ := a.worldRecord(t.Context(), w.WorldID)
			store, err := storage.OpenWorldDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			run := insertSceneCandidateRun(t, store, before, "legacy-schema")
			out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
			if err != nil || report.CoreCalls != 1 {
				t.Fatal("legacy candidate failed", report, err)
			}
			if err := commitSceneCandidate(t, store, run, out); err != nil {
				t.Fatal(err)
			}
			after := readContextSnapshot(t, a, w.WorldID)
			if after.Summary.Clock != "第 1 日 19:00" || after.Definition.Capabilities["spatial"] != 0 || wire.MarshalJSON(before.Definition) != wire.MarshalJSON(after.Definition) || len(after.PositionSources) != 0 {
				t.Fatal("old save gained an undeclared clock or spatial contract")
			}
		})
	}
}

func TestSceneGuidedFrozenNodesSettleThroughTerminalInSQL(t *testing.T) {
	a := newTestApp(t, &sceneCommitGenerator{})
	w, err := a.createFixtureWorld(t.Context(), "冻结节点集中续接", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		before := readContextSnapshot(t, a, w.WorldID)
		node, due, ok := turn.NextPlotNode(before)
		if !ok || before.Summary.StoryEnded {
			t.Fatal("frozen node sequence ended early")
		}
		minute, _ := plot.ClockMinute(before.Summary.Clock)
		elapsed := due - minute
		g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
			_, header, _ := strings.Cut(req.Input, "本轮依据：")
			var basis struct {
				Input string `json:"input"`
			}
			if err := json.Unmarshal([]byte(header), &basis); err != nil {
				return "", err
			}
			observe := map[string]any{"local_id": "b1", "kind": "observation", "actor_id": "player", "offset_minutes": elapsed, "basis": []string{"input:0"}, "content": "玩家等待当前窗口。", "recipients": []string{"player"}, "bystanders": []string{}, "projections": []any{map[string]any{"recipient": "player", "content": "我等到当前窗口。"}}, "effects": map[string]any{}}
			world := map[string]any{"local_id": "b2", "kind": "world_change", "actor_id": "world", "offset_minutes": elapsed, "basis": []string{"definition:" + before.Plot.Revision + ":" + node.ID}, "content": "当前外部节点已发生：" + node.ID, "recipients": []string{"player"}, "bystanders": []string{}, "projections": []any{map[string]any{"recipient": "player", "content": "我获知当前外部变动。"}}, "effects": map[string]any{}}
			progress := map[string]any{"type": "legacy_node", "id": node.ID, "status": "occurred", "content": "当前节点已结算。", "offset_minutes": elapsed, "basis": []string{"beat:b2"}, "beat_ids": []string{"b2"}}
			if node.Terminal {
				progress["ending"] = "当夜调查告一段落。"
			}
			return wire.MarshalJSON(map[string]any{"schema_revision": "scene-draft.v1", "input_map": []any{map[string]any{"text": basis.Input, "intent_type": "act", "addressee_id": "npc:innkeeper", "visibility": "public", "beat_ids": []string{"b1", "b2"}, "status": "succeeded", "unexecuted_reason": ""}}, "beats": []any{observe, world}, "narrative_blocks": []any{map[string]any{"text": "你等到外部变动，并获知当前结果。", "beat_ids": []string{"b1", "b2"}}}, "elapsed_minutes": elapsed, "stop": map[string]any{"reason": "completed", "content": "当前节点已结算。"}, "progress_updates": []any{progress}}), nil
		})
		path, _, _ := a.worldRecord(t.Context(), w.WorldID)
		store, err := storage.OpenWorldDB(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		run := insertSceneCandidateRun(t, store, before, fmt.Sprintf("guided-candidate-%d", index), "我等待下一次外部变动。")
		out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
		if err != nil || report.CoreCalls != 1 {
			t.Fatal("guided candidate failed", report, err)
		}
		if err := commitSceneCandidate(t, store, run, out); err != nil {
			t.Fatal(err)
		}
		store.Close()
		after := readContextSnapshot(t, a, w.WorldID)
		if after.PlotProgress.Nodes[node.ID].Status != "occurred" || after.Summary.StoryEnded != node.Terminal || after.PlotProgress.Version != before.PlotProgress.Version+1 {
			t.Fatal("guided node progress or ending lost in SQL", after.PlotProgress)
		}
	}
	final := readContextSnapshot(t, a, w.WorldID)
	if final.Summary.Clock != "第 1 日 20:00" || final.Summary.TurnSeq != 3 || final.PlotProgress.Ending == "" {
		t.Fatal("guided sequence did not settle once", final.Summary)
	}
}

func TestSceneForeignWorldSourceCannotEnterCandidate(t *testing.T) {
	a := newTestApp(t, &sceneCommitGenerator{})
	first, err := a.createFixtureWorld(t.Context(), "来源世界", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, first.WorldID)
	second, err := a.createFixtureWorld(t.Context(), "独立世界", "open", "旅人", "", false)
	if err != nil {
		t.Fatal(err)
	}
	before := readContextSnapshot(t, a, second.WorldID)
	path, _, _ := a.worldRecord(t.Context(), second.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, before, "foreign-world-candidate")
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		if strings.Contains(req.Input, "铜钥匙") || strings.Contains(req.Input, "fixture-history-01") {
			t.Fatal("another world's personal archive entered context")
		}
		base, err := (&sceneCommitGenerator{}).GenerateText(t.Context(), req)
		if err != nil {
			return "", err
		}
		var draft map[string]any
		if err := json.Unmarshal([]byte(base.Text), &draft); err != nil {
			return "", err
		}
		draft["beats"].([]any)[0].(map[string]any)["basis"] = []string{"fixture-history-01:input"}
		return wire.MarshalJSON(draft), nil
	})
	out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err == nil || report.CoreCalls != 2 || report.Repairs != 1 || len(out.Events) != 0 {
		t.Fatal("foreign source granted a cross-world basis", report, err)
	}
	after := readContextSnapshot(t, a, second.WorldID)
	if before.Summary.EventHead != after.Summary.EventHead || before.Summary.MessageHead != after.Summary.MessageHead || after.Summary.TurnSeq != 0 {
		t.Fatal("rejected foreign source changed its target world")
	}
}

func TestSceneUnexecutedReasonPersistsWithoutInventedCommunication(t *testing.T) {
	const input = "我私下告诉老板封套里的秘密。"
	const reason = "交谈被打断，我没有说出这句话。"
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		return wire.MarshalJSON(map[string]any{"schema_revision": "scene-draft.v1", "input_map": []any{map[string]any{"text": input, "intent_type": "speak", "addressee_id": "npc:innkeeper", "visibility": "private", "beat_ids": []string{}, "status": "not_executed", "unexecuted_reason": reason}}, "beats": []any{}, "narrative_blocks": []any{map[string]any{"text": reason, "beat_ids": []string{}}}, "elapsed_minutes": 0, "stop": map[string]any{"reason": "interrupted", "content": reason}, "progress_updates": []any{}}), nil
	})
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "未执行结果", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	before := readContextSnapshot(t, a, w.WorldID)
	path, _, _ := a.worldRecord(t.Context(), w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, before, "unexecuted-scene", input)
	out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil || report.CoreCalls != 1 {
		t.Fatal("unexecuted candidate failed", report, err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	result, ok := turn.EventByID(after.Events, run.RunID+":part:1:player-action:result:1")
	if !ok || result.Content != reason || result.SourceType != "action_not_executed" || after.Summary.Clock != before.Summary.Clock {
		t.Fatal("unexecuted result or clock lost in SQL", result)
	}
	for owner, perceptions := range after.Perceptions {
		for _, p := range perceptions {
			if owner != "player" && strings.Contains(p.Content, "封套里的秘密") {
				t.Fatal("NPC remembered communication that never happened", owner)
			}
		}
	}
	if err := turn.LoadLongMemory(t.Context(), store, &after); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wire.MarshalJSON(after.LongMemory["player"].Tail), reason) {
		t.Fatal("player personal memory lost the unexecuted reason")
	}
}

func TestSceneOptionalDigestFailureKeepsCompletePersonalTail(t *testing.T) {
	summaryCalls, sceneCalls := 0, 0
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		if strings.Contains(req.System, "整理单一接收者") {
			summaryCalls++
			return "", errors.New("fixture digest unavailable")
		}
		sceneCalls++
		base, err := (&sceneCommitGenerator{}).GenerateText(t.Context(), req)
		return base.Text, err
	})
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "可选摘要失败", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, w.WorldID)
	before := readContextSnapshot(t, a, w.WorldID)
	path, _, _ := a.worldRecord(t.Context(), w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, before, "digest-failed-candidate")
	out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil || report.CoreCalls != 1 || summaryCalls != 3 || sceneCalls != 1 {
		t.Fatal("optional failure reset maintenance or blocked valid scene", report, summaryCalls, sceneCalls, err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	if err := turn.LoadLongMemory(t.Context(), store, &after); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"player", "npc:innkeeper", "npc:mercenary"} {
		m := after.LongMemory[owner]
		if m.Digest.Revision != 0 || m.Digest.Through != 0 || len(m.Tail) < 10 || len(m.Tail) != len(m.Archive) {
			t.Fatal("failed optional digest discarded personal history", owner, m.Digest)
		}
	}
}

func TestSceneCommittedNarrativePrecedesSuggestionAndDiscardsLateBasis(t *testing.T) {
	g := &suggestionProbe{requests: make(chan model.TextRequest, 1), release: make(chan struct{})}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "候选建议分离", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	before := readContextSnapshot(t, a, w.WorldID)
	path, _, _ := a.worldRecord(t.Context(), w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, before, "suggestion-scene")
	out, _, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, &sceneCommitGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	view, err := a.ReadWorld(t.Context(), w.WorldID, 20)
	if err != nil || view.Summary.TurnSeq != 1 || view.Messages[len(view.Messages)-1].Content != out.Narrative || g.calls.Load() != 0 {
		t.Fatal("narrative waited for suggestions", err)
	}
	status, _ := a.Status(t.Context())
	if _, err := a.RequestSuggestions(t.Context(), w.WorldID, SuggestionRequest{Basis: suggestionBasis(view.Summary), ExpectedActiveRevision: status.ActiveRevision}); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-g.requests:
		if !strings.Contains(req.Input, out.Narrative) {
			t.Fatal("suggestions did not use committed narrative")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("suggestion worker did not start")
	}
	if err := store.InTx(t.Context(), func(tx *storage.WorldTx) error {
		return tx.SetMeta(t.Context(), "context_epoch", fmt.Sprint(view.Summary.ContextEpoch+1))
	}); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	a.copyWG.Wait()
	set, err := a.ReadSuggestions(t.Context(), w.WorldID)
	if err != nil || set.Status == "ready" || len(set.Items) > 0 {
		t.Fatal("late suggestions published against obsolete narrative basis", set, err)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	if after.Summary.MessageHead != view.Summary.MessageHead || after.Summary.EventHead != view.Summary.EventHead || after.Summary.TurnSeq != 1 {
		t.Fatal("suggestions repeated committed world effects")
	}
}

func TestSceneCandidateCancelAndSceneVersionRejectAtomicCommit(t *testing.T) {
	for _, change := range []string{"cancel", "scene-version"} {
		t.Run(change, func(t *testing.T) {
			g := &sceneCommitGenerator{}
			a := newTestApp(t, g)
			w, err := a.createFixtureWorld(t.Context(), "候选提交边界", "open", "旅人", "", true)
			if err != nil {
				t.Fatal(err)
			}
			before := readContextSnapshot(t, a, w.WorldID)
			path, _, _ := a.worldRecord(t.Context(), w.WorldID)
			store, err := storage.OpenWorldDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			run := insertSceneCandidateRun(t, store, before, "commit-boundary")
			out, _, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
			if err != nil {
				t.Fatal(err)
			}
			want := error(context.Canceled)
			if change == "cancel" {
				err = a.CancelRun(t.Context(), w.WorldID, run.RunID)
			} else {
				want = ErrVersionConflict
				err = store.InTx(t.Context(), func(tx *storage.WorldTx) error {
					return tx.SetMeta(t.Context(), "scene_version", fmt.Sprint(before.SceneVersion+1))
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := commitSceneCandidate(t, store, run, out); !errors.Is(err, want) {
				t.Fatal("stale or cancelled candidate committed", err)
			}
			after := readContextSnapshot(t, a, w.WorldID)
			if after.Summary.TurnSeq != 0 || after.Summary.Clock != before.Summary.Clock || after.Summary.EventHead != before.Summary.EventHead || after.Summary.MessageHead != before.Summary.MessageHead || wire.MarshalJSON(before.Perceptions) != wire.MarshalJSON(after.Perceptions) {
				t.Fatal("rejected commit changed world or personal memory")
			}
		})
	}
}

func TestSceneOrdinaryTextCorrectionInvalidatesCausalRepliesAndKeepsHistory(t *testing.T) {
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		if strings.Contains(req.System, "整理单一接收者") {
			return `{"content":"个人回顾按有效纠正整理。","states":[]}`, nil
		}
		base, err := (&sceneCommitGenerator{}).GenerateText(t.Context(), req)
		if err != nil {
			return "", err
		}
		var draft map[string]any
		if err := json.Unmarshal([]byte(base.Text), &draft); err != nil {
			return "", err
		}
		second := map[string]any{"local_id": "b2", "kind": "dialogue", "actor_id": "npc:mercenary", "offset_minutes": 0, "basis": []string{"beat:b1"}, "content": "老板答应查登记，我可以等你们核对。", "scope": "public", "recipients": []string{}, "bystanders": []string{}, "projections": []any{}, "effects": map[string]any{}}
		draft["beats"] = append(draft["beats"].([]any), second)
		draft["input_map"].([]any)[0].(map[string]any)["beat_ids"] = []string{"b1", "b2"}
		return wire.MarshalJSON(draft), nil
	})
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "候选文本纠正", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	before := readContextSnapshot(t, a, w.WorldID)
	path, _, _ := a.worldRecord(t.Context(), w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	run := insertSceneCandidateRun(t, store, before, "ordinary-correction")
	out, _, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	store.Close()
	before = readContextSnapshot(t, a, w.WorldID)
	root := run.RunID + ":npc:innkeeper:speech:1"
	_, err = a.Correct(t.Context(), w.WorldID, memory.CorrectionRequest{RequestKey: "scene-speech", ExpectedEpoch: before.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: root, Replacement: "我目前不能查看登记。"})
	if err != nil {
		t.Fatal(err)
	}
	if job := waitMemory(t, a, w.WorldID); job.Status != "completed" {
		t.Fatal(job)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	if wire.MarshalJSON(before.Messages) != wire.MarshalJSON(after.Messages) || after.Summary.ContextEpoch != before.Summary.ContextEpoch+1 {
		t.Fatal("correction rewrote original story or failed to advance epoch")
	}
	following, ok := turn.EventByID(after.Events, run.RunID+":npc:mercenary:speech:2")
	if !ok || !strings.Contains(following.Content, "已失效") {
		t.Fatal("reply dependent on the corrected projection stayed effective", following)
	}
	store, err = storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events, err := store.LoadEvents(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	original, ok := turn.EventByID(events, root)
	if !ok || original.Content != "我会帮你查看登记。" {
		t.Fatal("original source is no longer traceable", original, err)
	}
}

func TestSceneCandidateReadsPublishedDigestsAcrossWorldCopyAndRestart(t *testing.T) {
	digests, scenes := 0, 0
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		if strings.Contains(req.System, "整理单一接收者") {
			digests++
			_, rest, _ := strings.Cut(req.Input, "接收者：")
			owner, _, _ := strings.Cut(rest, "\n")
			return wire.MarshalJSON(map[string]any{"content": "有效个人摘要 owner=" + owner, "states": []any{}}), nil
		}
		scenes++
		for _, owner := range []string{"player", "npc:innkeeper", "npc:mercenary"} {
			if !strings.Contains(req.Input, "有效个人摘要 owner="+owner) {
				t.Fatal("scene did not read the latest owner's digest", owner)
			}
		}
		base, err := (&sceneCommitGenerator{}).GenerateText(t.Context(), req)
		if err != nil {
			return "", err
		}
		var draft map[string]any
		if err := json.Unmarshal([]byte(base.Text), &draft); err != nil {
			return "", err
		}
		// Reference the published digest so its complete personal coverage is
		// normalized into actual event dependencies in the SQL commit.
		draft["beats"].([]any)[0].(map[string]any)["basis"] = []string{"personal:npc%3Ainnkeeper:digest%3Anpc%3Ainnkeeper%3A1"}
		return wire.MarshalJSON(draft), nil
	})
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(t.Context(), "摘要续玩", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, w.WorldID)
	before := readContextSnapshot(t, a, w.WorldID)
	path, _, _ := a.worldRecord(t.Context(), w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	run := insertSceneCandidateRun(t, store, before, "digest-first")
	out, _, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	store.Close()
	status, _ := a.Status(t.Context())
	copy, err := a.SaveAs(t.Context(), w.WorldID, "摘要副本", "digest-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := a.DataRoot()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), Options{DataRoot: dataRoot, UserID: LocalUserID, Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for index, id := range []string{w.WorldID, copy.TargetWorldID} {
		saved := readContextSnapshot(t, reopened, id)
		if saved.Summary.TurnSeq != 1 || saved.Summary.Clock != before.Summary.Clock {
			t.Fatal("copy or restart changed authoritative state")
		}
		path, _, _ := reopened.worldRecord(t.Context(), id)
		store, err := storage.OpenWorldDB(path)
		if err != nil {
			t.Fatal(err)
		}
		run := insertSceneCandidateRun(t, store, saved, fmt.Sprintf("digest-resume-%d", index))
		out, report, err := reopened.turnService().BuildSceneCandidate(t.Context(), store, run, g)
		if err != nil || report.CoreCalls != 1 {
			t.Fatal("saved digest could not resume scene", report, err)
		}
		if err := commitSceneCandidate(t, store, run, out); err != nil {
			t.Fatal(err)
		}
		store.Close()
	}
	if digests != 3 || scenes != 3 {
		t.Fatalf("stable digests were regenerated unnecessarily: digest=%d scene=%d", digests, scenes)
	}
}
