package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
)

func currentMistResponseFixture(t *testing.T, name string) []byte {
	t.Helper()
	pack, err := content.Load(filepath.Join("..", "content", "packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "turn", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	// Preserve historical text while binding its source references to this pack.
	return []byte(strings.ReplaceAll(string(raw), "material:mist-embers.pack.v9:", "material:"+pack.Definition.Revision+":"))
}

func TestSceneModelDialogueDurationPreservesProgramWaitBinding(t *testing.T) {
	raw := currentMistResponseFixture(t, "mist-first-model-response.json")
	var draft map[string]any
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	fragment := draft["input_map"].([]any)[0].(map[string]any)
	input := fragment["text"].(string)
	calls := 0
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		if strings.Contains(req.System, "结构化回合意图") {
			return `{"intent_type":"speak","addressee_id":"npc:tailor","visibility":"public","action_rule_id":"","wait_minutes":0}`, nil
		}
		calls++
		if calls == 1 {
			return string(raw), nil
		}
		if !strings.Contains(req.Input, "field=input_map[0].wait_minutes;expected=0") {
			t.Fatal("repair did not identify the mismatched wait binding")
		}
		fragment["wait_minutes"] = 0
		draft["progress_updates"] = []any{map[string]any{"type": "development", "id": "case-pressure", "status": "deferred", "content": "交谈期间尚无外部新变化，保留当前调查压力。", "offset_minutes": 8, "basis": []string{"material:mist-embers.pack.v10:case-development"}, "beat_ids": []string{}}}
		return wire.MarshalJSON(draft), nil
	})
	a := newTestApp(t, g)
	w := createPackWorld(t, a, "mist-embers")
	before := readContextSnapshot(t, a, w.WorldID)
	path, _, err := a.worldRecord(t.Context(), w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, before, "dialogue-duration", input)
	run.AddresseeID = ""
	out, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil || report.Repairs != 1 || report.CoreCalls != 2 || !strings.HasSuffix(out.Clock, "09:08") {
		t.Fatalf("repair and duration: report=%+v clock=%q err=%v", report, out.Clock, err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	if after.Summary.Clock != out.Clock || after.Summary.TurnSeq != before.Summary.TurnSeq+1 || len(after.Perceptions["npc:tailor"]) <= len(before.Perceptions["npc:tailor"]) {
		t.Fatal("repaired dialogue and personal experience did not commit together")
	}
}

func TestSceneFollowupFitsBudgetWithCompleteCommittedExperience(t *testing.T) {
	raw := currentMistResponseFixture(t, "mist-martin-question-scene.json")
	var first struct {
		InputMap []struct {
			Text string `json:"text"`
		} `json:"input_map"`
	}
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}
	input := first.InputMap[0].Text
	reached := errors.New("followup reached provider")
	var narrative string
	var followup model.TextRequest
	followupCalls := 0
	g := sceneCandidateFixtureGenerator(func(req model.TextRequest) (string, error) {
		if strings.Contains(req.System, "结构化回合意图") {
			return `{"intent_type":"speak","addressee_id":"npc:tailor","visibility":"public","action_rule_id":"","wait_minutes":0}`, nil
		}
		if narrative == "" {
			return string(raw), nil
		}
		followupCalls++
		if followupCalls == 1 {
			return `{"schema_revision":`, nil
		}
		followup = req
		return "", reached
	})
	a := newTestApp(t, g)
	w := createPackWorld(t, a, "mist-embers")
	path, _, err := a.worldRecord(t.Context(), w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run := insertSceneCandidateRun(t, store, readContextSnapshot(t, a, w.WorldID), "committed-question", input)
	run.AddresseeID = ""
	out, _, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitSceneCandidate(t, store, run, out); err != nil {
		t.Fatal(err)
	}
	narrative = out.Narrative
	before := readContextSnapshot(t, a, w.WorldID)
	run = insertSceneCandidateRun(t, store, before, "followup", "我想请马丁同意我先去裁缝店看看诺拉的房间和留下的东西，找出那单活计的账目或线索，好确认她打算接的是什么顾客。")
	run.AddresseeID = ""
	_, report, err := a.turnService().BuildSceneCandidate(t.Context(), store, run, g)
	if !errors.Is(err, reached) {
		t.Fatalf("followup could not reach provider: %v", err)
	}
	if report.Repairs != 1 || followupCalls != 2 {
		t.Fatal("followup correction did not share the bounded scene attempt")
	}
	if followup.MaxInputTokens != 12000 || model.FramedTextInputTokens(followup) > 12000 {
		t.Fatal("shared input limit changed")
	}
	if !strings.Contains(followup.Input, wire.MarshalJSON(narrative)) || !strings.Contains(followup.Input, "个人记忆 owner=npc:tailor") || !strings.Contains(followup.Input, "程序解析的原文绑定") || !strings.Contains(followup.Input, "别跟她说这活儿是他请人做的") {
		t.Fatal("complete player narrative, personal experience or program binding lost")
	}
	if !phase13WorldUnchanged(before, readContextSnapshot(t, a, w.WorldID)) {
		t.Fatal("unfinished followup changed world state")
	}
	t.Logf("followup input_tokens=%d", model.FramedTextInputTokens(followup))
}
