package storyapp

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
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func contextFixture() worldSnapshot {
	s := worldSnapshot{Summary: wiaworld.WorldSummary{GameID: GameID, WorldID: "fixture", Scene: "不应共享的全知旧场景"}, SceneVersion: 1, Characters: lanternDefinition().Characters, Perceptions: map[string][]wiaworld.Perception{}, Memories: map[string][]wiaworld.Memory{}, Sources: map[string]sourceMetadata{}}
	s.SceneViews = initialSceneViews(s)
	return s
}

func readContextSnapshot(t *testing.T, a *App, id string) worldSnapshot {
	t.Helper()
	path, _, err := a.worldRecord(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	s, err := loadTurnSnapshot(context.Background(), store, 40)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSceneUpdatesEnforceEverySourceRecipient(t *testing.T) {
	s := contextFixture()
	run := wiaworld.Run{RunID: "run"}
	intent := turnIntent{Visibility: "private", AddresseeID: "npc:innkeeper"}
	output := turnOutput{Events: []wiaworld.Event{{EventID: "run:input", RunID: "run", Stage: 1, EventType: "player_attempt", ActorID: "player", Content: "私密信件位置"}}}
	for _, test := range []struct {
		name            string
		ids, recipients []string
		valid           bool
	}{
		{"player", []string{"run:input"}, []string{"player"}, true},
		{"recipient", []string{"run:input"}, []string{"npc:innkeeper"}, true},
		{"observer", []string{"run:input"}, []string{"npc:mercenary"}, false},
		{"mixed sources", []string{"view:npc:mercenary", "run:input"}, []string{"npc:mercenary"}, false},
		{"other world", []string{"another:input"}, []string{"player"}, false},
		{"other personal view", []string{"view:npc:innkeeper"}, []string{"npc:mercenary"}, false},
		{"invalid recipient", []string{"run:input"}, []string{"npc:unknown"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := hostResult{SceneUpdates: []sceneUpdate{{Content: "私密信件位置", SourceIDs: test.ids, Recipients: test.recipients}}}
			views, err := applySceneUpdates(s, run, intent, output, host)
			if (err == nil) != test.valid {
				t.Fatalf("err=%v", err)
			}
			if err == nil {
				next := s
				next.SceneViews = views
				if strings.Contains(sceneFor(next, "npc:mercenary"), "私密") {
					t.Fatal("private scene broadcast")
				}
			}
		})
	}
}

func TestScopedRequestsIgnoreLegacyOmniscientSceneAndForeignMemory(t *testing.T) {
	s := contextFixture()
	s.Memories["npc:innkeeper"] = []wiaworld.Memory{{SourceEventID: "secret", Content: "SECRET_MEMORY"}}
	s.SceneViews[1].Content = "PRIVATE_SCENE"
	material := composeNPC(s, lanternDefinition(), s.Characters[1], "npc:innkeeper", "speak", npcStageInput{PlayerPerception: "看见交谈，但未听清"}, "", 1)
	req, _, err := (ContextComposer{}).Build(material, material.System, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET_MEMORY", "PRIVATE_SCENE", "不应共享的全知旧场景"} {
		if strings.Contains(req.Input, secret) {
			t.Fatalf("unexpected source %s", secret)
		}
	}
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
	store, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	for i := 0; i < 45; i++ {
		_, err = store.db.Exec(`INSERT INTO events(seq,event_id,event_type,actor_id,target_id,content,run_id,stage,scene_version,source_type,created_at) SELECT COALESCE(MAX(seq),0)+1,?,'noise','','','noise','fixture',1,1,'fixture',? FROM events`, fmt.Sprintf("noise:%d", i), wire.NowText())
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := loadWorldSnapshot(context.Background(), store, 40)
	if err != nil {
		t.Fatal(err)
	}
	text := joinPerceptions(s, s.Perceptions["npc:mercenary"])
	if !strings.Contains(text, "沈岚（客栈老板）") {
		t.Fatalf("speaker lost: %s", text)
	}
	if _, ok := eventByID(s.Events, r.RunID+":input"); ok {
		t.Fatal("fixture did not push event outside window")
	}
	s.Perceptions["npc:mercenary"] = append(s.Perceptions["npc:mercenary"], wiaworld.Perception{SourceEventID: "missing"})
	if _, err := loadSourceMetadata(context.Background(), store.db, s); !errors.Is(err, ErrContextSourceMissing) {
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
	a := newTestApp(t, sceneUpdateGenerator{base: &scriptedGenerator{}})
	w, err := a.createFixtureWorld(context.Background(), "私密场景", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.ReadWorld(context.Background(), w.WorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: "private", Input: "私下对老板说隐藏原文"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, r.RunID); done.Status != "failed" {
		t.Fatalf("%+v", done)
	}
	after, err := a.ReadWorld(context.Background(), w.WorldID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Events, after.Events) || !reflect.DeepEqual(before.Memories, after.Memories) {
		t.Fatal("partial commit")
	}
}

func TestLegacySceneViewsUseOnlyAuthorizedRecords(t *testing.T) {
	s := contextFixture()
	s.Summary.TurnSeq = 10
	s.Events = []wiaworld.Event{{EventID: "public-result", EventType: "turn_settled", Content: "茶在桌上。"}, {EventID: "secret", EventType: "npc_action_result", Content: "隐藏信件。"}}
	s.Perceptions["npc:mercenary"] = []wiaworld.Perception{{SourceEventID: "public-result", SourceType: "action_succeeded", Content: "茶在桌上。"}}
	s.SceneViews = initialSceneViews(s)
	for _, id := range []string{"player", "npc:mercenary"} {
		if sceneFor(s, id) != "茶在桌上。" {
			t.Fatalf("%s: %s", id, sceneFor(s, id))
		}
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
	store, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES('npc:mercenary','missing','observed','交谈迹象',1,1,?)`, wire.NowText())
	store.db.Close()
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

func TestNarrativeEventsPreservePublicNPCSpeechScope(t *testing.T) {
	events := narrativeEvents([]wiaworld.Event{{EventType: "npc_dialogue", ActorID: "npc:innkeeper", Content: "公开回答"}}, lanternDefinition().Characters, "旅人", wiaworld.NarrativeSettings{})
	if len(events) != 1 || events[0].SpeechScope != "public_current_scene" {
		t.Fatalf("%+v", events)
	}
}

func TestNarrativeEventsPreservePlayerAudibility(t *testing.T) {
	for _, test := range []struct{ source, scope string }{{"player_public", "public_current_scene"}, {"player_private", "private_recipient"}} {
		events := narrativeEvents([]wiaworld.Event{{EventType: "player_attempt", ActorID: "player", SourceType: test.source, Content: "你明白的"}}, nil, "旅人", wiaworld.NarrativeSettings{})
		if len(events) != 1 || events[0].SpeechScope != test.scope {
			t.Fatalf("%+v", events)
		}
	}
}

func TestSceneSourcesRejectForeignRunAndFutureStage(t *testing.T) {
	s := contextFixture()
	events := []wiaworld.Event{
		{EventID: "valid", RunID: "current", Stage: 1, EventType: "player_attempt"},
		{EventID: "foreign", RunID: "foreign", Stage: 1, EventType: "player_attempt"},
		{EventID: "future", RunID: "current", Stage: 9, EventType: "npc_dialogue"},
	}
	sources := sceneSources(s, wiaworld.Run{RunID: "current"}, turnIntent{Visibility: "public"}, events)
	var valid bool
	for _, source := range sources {
		if source.ID == "foreign" || source.ID == "future" {
			t.Fatal("invalid stage/run source admitted")
		}
		if source.ID == "valid" {
			valid = true
		}
	}
	if !valid {
		t.Fatal("current source missing")
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
	if sceneFor(before, "npc:innkeeper") != "隐藏原文" || strings.Contains(sceneFor(before, "npc:mercenary"), "隐藏原文") {
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
