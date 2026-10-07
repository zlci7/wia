package turn

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	wiaworld "gameagent/backend/internal/world"
)

func TestSceneNestedBusinessFailureRetainsCorrectionField(t *testing.T) {
	raw := strings.Replace(sceneDraftFixture(t), `"scope": "public",`, `"scope":"public","status":"succeeded",`, 1)
	_, err := decodeSceneResponse(raw)
	var failure *GenerationError
	if !errors.As(err, &failure) || failure.Code != "scene_dialogue_invalid" || failure.Field != "beats.status" || failure.Expected != "omitted-for-dialogue" {
		t.Fatalf("nested correction lost its contract: %v", err)
	}
}

func sceneDraftFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/scene-investigation.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSceneDraftContract(t *testing.T) {
	response, err := decodeSceneResponse(sceneDraftFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if response.Draft == nil || response.Resolution != nil || response.Material != nil {
		t.Fatalf("wrong response kind: %+v", response)
	}
	if err := validateSceneInput(response.Draft, wiaworld.Run{Input: "我检查账簿和收据，记下发现。"}); err != nil {
		t.Fatal(err)
	}
	if response.Draft.InputMap[0].End != len([]rune(response.Draft.InputMap[0].Text)) {
		t.Fatal("input offsets not computed from original run")
	}
	for _, bad := range []string{"我检查账簿。", "我检查账簿和收据，记下发现。然后拿走它。"} {
		if err := validateSceneInput(response.Draft, wiaworld.Run{Input: bad}); err == nil {
			t.Fatal("rewritten or uncovered input accepted")
		}
	}
}

func TestSceneDraftRejectsIncompleteOrContradictoryResponses(t *testing.T) {
	base := sceneDraftFixture(t)
	cases := map[string]string{
		"duplicate":              strings.Replace(base, `"schema_revision":`, `"schema_revision":"scene-draft.v1","schema_revision":`, 1),
		"truncated":              base[:len(base)/2],
		"unknown":                strings.Replace(base, `"schema_revision":`, `"world_state":{},"schema_revision":`, 1),
		"mixed material":         strings.Replace(base, `"schema_revision":`, `"needs_material":["file"],"schema_revision":`, 1),
		"mixed checkpoint":       strings.Replace(base, `"schema_revision":`, `"needs_resolution":{},"schema_revision":`, 1),
		"nested missing zero":    strings.Replace(base, `"offset_minutes": 0,`, "", 1),
		"nested null":            strings.Replace(base, `"effects": {}`, `"effects":null`, 1),
		"unknown kind":           strings.Replace(base, `"dialogue"`, `"thought"`, 1),
		"future basis":           strings.Replace(base, `"personal:shopkeeper:receipt"`, `"beat:b2"`, 1),
		"self basis":             strings.Replace(base, `"personal:shopkeeper:receipt"`, `"beat:b1"`, 1),
		"public audience":        strings.Replace(base, `"recipients": []`, `"recipients":["player"]`, 1),
		"dialogue status":        strings.Replace(base, `"scope": "public",`, `"scope":"public","status":"succeeded",`, 1),
		"missing stop":           strings.Replace(base, `"reason": "player_choice",`, "", 1),
		"elapsed mismatch":       strings.Replace(base, `"elapsed_minutes": 2`, `"elapsed_minutes":3`, 1),
		"unknown narrative node": strings.Replace(base, `"beat_ids": [`, `"beat_ids": ["absent",`, 1),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeSceneResponse(text); err == nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
}

func TestSceneSupplementalResponseContracts(t *testing.T) {
	for _, text := range []string{`{"needs_material":[]}`, `{"needs_material":["a","a"]}`, `{"needs_material":["a","b","c","d","e"]}`, `{"needs_material":["a"],"beats":[]}`, `{"needs_resolution":{"rule_id":"r","input_fragment_index":0,"prefix_beats":[],"prefix_elapsed_minutes":null}}`} {
		if _, err := decodeSceneResponse(text); err == nil {
			t.Fatalf("invalid response accepted: %s", text)
		}
	}
	if response, err := decodeSceneResponse(`{"needs_material":["a"]}`); err != nil || len(response.Material) != 1 {
		t.Fatalf("read: %+v %v", response, err)
	}
	if response, err := decodeSceneResponse(`{"needs_resolution":{"rule_id":"r","input_fragment_index":0,"prefix_beats":[],"prefix_elapsed_minutes":0}}`); err != nil || response.Resolution == nil {
		t.Fatalf("checkpoint: %+v %v", response, err)
	}
}

func TestSceneActionFieldsAndOriginalFragments(t *testing.T) {
	var draft map[string]any
	if err := json.Unmarshal([]byte(sceneDraftFixture(t)), &draft); err != nil {
		t.Fatal(err)
	}
	beats := draft["beats"].([]any)
	action := beats[1].(map[string]any)
	action["kind"], action["status"] = "action_result", "succeeded"
	action["attempt"] = map[string]any{"content": "检查账簿和收据", "input_fragment_index": 0}
	marshal := func() string {
		data, err := json.Marshal(draft)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if _, err := decodeSceneResponse(marshal()); err != nil {
		t.Fatal(err)
	}
	delete(action["attempt"].(map[string]any), "input_fragment_index")
	if _, err := decodeSceneResponse(marshal()); err == nil {
		t.Fatal("unbound player action accepted")
	}
	action["actor_id"] = "shopkeeper"
	if _, err := decodeSceneResponse(marshal()); err != nil {
		t.Fatal(err)
	}
	action["attempt"].(map[string]any)["input_fragment_index"] = 0
	if _, err := decodeSceneResponse(marshal()); err == nil {
		t.Fatal("NPC action bound to player's original words")
	}
}
