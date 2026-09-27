package storyapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gameagent/runtime/internal/model"
)

func contextFixture() worldSnapshot {
	s := worldSnapshot{Summary: WorldSummary{GameID: GameID, WorldID: "fixture", Scene: "不应共享的全知旧场景"}, SceneVersion: 1, Characters: lanternDefinition().Characters, Perceptions: map[string][]Perception{}, Memories: map[string][]Memory{}, Sources: map[string]sourceMetadata{}}
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
	s, err := loadWorldSnapshot(context.Background(), store, 40)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSceneUpdatesEnforceEverySourceRecipient(t *testing.T) {
	s := contextFixture()
	run := Run{RunID: "run"}
	intent := turnIntent{Visibility: "private", AddresseeID: "npc:innkeeper"}
	output := turnOutput{Events: []Event{{EventID: "run:input", EventType: "player_attempt", ActorID: "player", Content: "私密信件位置"}}}
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
	s.Memories["npc:innkeeper"] = []Memory{{SourceEventID: "secret", Content: "SECRET_MEMORY"}}
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
	w, err := a.CreateWorld(context.Background(), "来源窗口", "guided", "旅人", "", true)
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
		_, err = store.db.Exec(`INSERT INTO events(seq,event_id,event_type,actor_id,target_id,content,run_id,stage,scene_version,source_type,created_at) SELECT COALESCE(MAX(seq),0)+1,?,'noise','','','noise','fixture',1,1,'fixture',? FROM events`, fmt.Sprintf("noise:%d", i), nowText())
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
	s.Perceptions["npc:mercenary"] = append(s.Perceptions["npc:mercenary"], Perception{SourceEventID: "missing"})
	if _, err := loadSourceMetadata(context.Background(), store.db, s); !errors.Is(err, ErrContextSourceMissing) {
		t.Fatalf("missing=%v", err)
	}
}

type invalidSceneGenerator struct{ base *scriptedGenerator }

func (g invalidSceneGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
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
				host.SceneUpdates = []sceneUpdate{{Content: "隐藏原文", SourceIDs: []string{source.ID}, Recipients: []string{"npc:mercenary"}}}
			}
		}
		data, _ := json.Marshal(host)
		response.Text = string(data)
	}
	return response, nil
}

func TestUnauthorizedSceneUpdateFailsWithoutPartialCommit(t *testing.T) {
	a := newTestApp(t, invalidSceneGenerator{base: &scriptedGenerator{}})
	w, err := a.CreateWorld(context.Background(), "私密场景", "guided", "旅人", "", true)
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
	s.Events = []Event{{EventID: "public-result", EventType: "turn_settled", Content: "茶在桌上。"}, {EventID: "secret", EventType: "npc_action_result", Content: "隐藏信件。"}}
	s.Perceptions["npc:mercenary"] = []Perception{{SourceEventID: "public-result", SourceType: "action_succeeded", Content: "茶在桌上。"}}
	s.SceneViews = initialSceneViews(s)
	for _, id := range []string{"player", "npc:mercenary"} {
		if sceneFor(s, id) != "茶在桌上。" {
			t.Fatalf("%s: %s", id, sceneFor(s, id))
		}
	}
}
