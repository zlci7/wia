package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func contextFixture() turn.Snapshot {
	s := turn.Snapshot{Summary: wiaworld.WorldSummary{GameID: GameID, WorldID: "fixture", Scene: "不应共享的全知旧场景"}, SceneVersion: 1, Characters: lanternDefinition().Characters, Perceptions: map[string][]wiaworld.Perception{}, Memories: map[string][]wiaworld.Memory{}, Sources: map[string]turn.SourceMetadata{}}
	s.SceneViews = []turn.SceneView{{Recipient: "player", Version: 1, Content: "玩家情境", SourceIDs: []string{"opening"}}}
	for _, character := range s.Characters {
		s.SceneViews = append(s.SceneViews, turn.SceneView{Recipient: character.EntityID, Version: 1, Content: character.Name + "的情境", SourceIDs: []string{"opening"}})
	}
	return s
}

func readContextSnapshot(t *testing.T, a *App, id string) turn.Snapshot {
	t.Helper()
	path, _, err := a.worldRecord(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	s, err := turn.LoadInputSnapshot(context.Background(), store, 40)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSourceMetadataSurvivesGlobalWindowAndRejectsMissing(t *testing.T) {
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(context.Background(), "来源窗口", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: "one", Input: "跟老板问好"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, r.RunID); done.Status != "completed" {
		t.Fatalf("%+v", done)
	}
	path, _, _ := a.worldRecord(context.Background(), w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	for i := 0; i < 45; i++ {
		_, err = store.Database().Exec(`INSERT INTO events(seq,event_id,event_type,actor_id,target_id,content,run_id,stage,scene_version,source_type,created_at) SELECT COALESCE(MAX(seq),0)+1,?,'noise','','','noise','fixture',1,1,'fixture',? FROM events`, fmt.Sprintf("noise:%d", i), wire.NowText())
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := turn.LoadSnapshot(context.Background(), store, 40)
	if err != nil {
		t.Fatal(err)
	}
	text := turn.JoinPerceptions(s, s.Perceptions["npc:mercenary"])
	if !strings.Contains(text, "沈岚（客栈老板）") {
		t.Fatalf("speaker lost: %s", text)
	}
	if _, ok := turn.EventByID(s.Events, r.RunID+":input"); ok {
		t.Fatal("fixture did not push event outside window")
	}
	if _, err := store.Database().Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES('npc:mercenary','missing','fixture','missing',1,1,?)`, wire.NowText()); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.LoadInputSnapshot(context.Background(), store, 40); !errors.Is(err, turn.ErrContextSourceMissing) {
		t.Fatalf("missing=%v", err)
	}
}

type sceneUpdateGenerator struct {
	base      *scriptedGenerator
	recipient string
}

func (g sceneUpdateGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	response, err := g.base.GenerateText(ctx, req)
	if err != nil {
		return response, err
	}
	if strings.Contains(req.System, "场景协调 Agent") {
		var host hostResult
		if err := json.Unmarshal([]byte(response.Text), &host); err != nil {
			return response, err
		}
		start := strings.Index(req.Input, "场景来源(JSON)：") + len("场景来源(JSON)：")
		end := strings.Index(req.Input[start:], "\n")
		var sources []sceneSource
		_ = json.Unmarshal([]byte(req.Input[start:start+end]), &sources)
		for _, source := range sources {
			if strings.HasSuffix(source.ID, ":input") {
				recipient := g.recipient
				if recipient == "" {
					recipient = "npc:mercenary"
				}
				host.SceneUpdates = []sceneUpdate{{Content: "隐藏原文", SourceIDs: []string{source.ID}, Recipients: []string{recipient}}}
			}
		}
		data, _ := json.Marshal(host)
		response.Text = string(data)
	}
	return response, nil
}

func TestUnauthorizedSceneUpdateFailsWithoutPartialCommit(t *testing.T) {
	g := &scriptedGenerator{}
	a := newTestApp(t, sceneUpdateGenerator{base: g})
	w, err := a.createFixtureWorld(context.Background(), "私密场景", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.ReadWorld(context.Background(), w.WorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	beforeSnapshot := readContextSnapshot(t, a, w.WorldID)
	r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: "private", Input: "私下对老板说隐藏原文"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, r.RunID); done.Status != "failed" || done.Reason != "coordination_generation_failed" {
		t.Fatalf("%+v", done)
	}
	after, err := a.ReadWorld(context.Background(), w.WorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Events, after.Events) || !reflect.DeepEqual(before.Memories, after.Memories) {
		t.Fatal("partial commit")
	}
	afterSnapshot := readContextSnapshot(t, a, w.WorldID)
	if !reflect.DeepEqual(beforeSnapshot.Perceptions, afterSnapshot.Perceptions) || !reflect.DeepEqual(beforeSnapshot.SceneViews, afterSnapshot.SceneViews) || beforeSnapshot.Summary.Clock != afterSnapshot.Summary.Clock || beforeSnapshot.Summary.TurnSeq != afterSnapshot.Summary.TurnSeq || beforeSnapshot.SceneVersion != afterSnapshot.SceneVersion {
		t.Fatal("failed repair advanced world state")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	coordinationCalls := 0
	for _, req := range g.requests {
		if strings.Contains(req, "场景协调 Agent") {
			coordinationCalls++
		}
	}
	if coordinationCalls != 2 {
		t.Fatalf("coordination calls=%d", coordinationCalls)
	}
}

func TestMissingContextSourceBlocksGenerationNotReadingHistory(t *testing.T) {
	g := &scriptedGenerator{}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(context.Background(), "缺失来源", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := a.worldRecord(context.Background(), w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Database().Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES('npc:mercenary','missing','observed','交谈迹象',1,1,?)`, wire.NowText())
	store.Database().Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadWorld(context.Background(), w.WorldID, 100); err != nil {
		t.Fatalf("history unavailable: %v", err)
	}
	r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: "missing", Input: "打招呼"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, r.RunID); done.Status != "failed" || done.Reason != "context_source_missing" {
		t.Fatalf("%+v", done)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.requests) != 0 {
		t.Fatal("model called with incomplete sources")
	}
}

func TestPrivateSceneViewsSurviveCopyAndRuntimeRestart(t *testing.T) {
	g := sceneUpdateGenerator{base: &scriptedGenerator{}, recipient: "npc:innkeeper"}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(context.Background(), "场景保存", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: "private", Input: "私下向老板说隐藏原文"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, r.RunID); done.Status != "completed" {
		t.Fatalf("%+v", done)
	}
	before := readContextSnapshot(t, a, w.WorldID)
	if turn.SceneFor(before, "npc:innkeeper") != "隐藏原文" || strings.Contains(turn.SceneFor(before, "npc:mercenary"), "隐藏原文") {
		t.Fatal("invalid scoped scene")
	}
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	op, err := a.SaveAs(context.Background(), w.WorldID, "分支", "scene-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && op.Status != "ready" {
		op, err = a.CopyOperation(context.Background(), op.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.Status != "ready" {
		t.Fatalf("%+v", op)
	}
	root := a.DataRoot()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), Options{DataRoot: root, UserID: LocalUserID, Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, id := range []string{w.WorldID, op.TargetWorldID} {
		after := readContextSnapshot(t, reopened, id)
		if !reflect.DeepEqual(before.Characters, after.Characters) {
			t.Fatal("character initial concerns changed across restart/copy")
		}
		if !reflect.DeepEqual(before.SceneViews, after.SceneViews) {
			t.Fatal("scene provenance changed across restart/copy")
		}
	}
}
