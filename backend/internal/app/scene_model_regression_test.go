package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
)

func TestSceneModelDialogueDurationPreservesProgramWaitBinding(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "turn", "testdata", "mist-first-model-response.json"))
	if err != nil {
		t.Fatal(err)
	}
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
		draft["progress_updates"] = []any{map[string]any{"type": "development", "id": "case-pressure", "status": "deferred", "content": "交谈期间尚无外部新变化，保留当前调查压力。", "offset_minutes": 8, "basis": []string{"material:mist-embers.pack.v9:case-development"}, "beat_ids": []string{}}}
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
