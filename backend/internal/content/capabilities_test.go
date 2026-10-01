package content

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
)

func TestModelStateDefinitionsAcceptTypedUpdates(t *testing.T) {
	for _, test := range []struct {
		kind, initial string
		values        []string
	}{{"integer", "2", nil}, {"boolean", "false", nil}, {"enum", `"ready"`, []string{"ready", "tired"}}} {
		t.Run(test.kind, func(t *testing.T) {
			min, max := 0, 100
			definition := PackStateDefinition{ID: "condition", Name: "Condition", Type: test.kind, Default: json.RawMessage(test.initial), Scope: "all", Projection: "self", Knowledge: "owner", UpdatePolicy: story.StateUpdatePolicy{Kind: "model"}, EnumValues: test.values}
			if test.kind == "integer" {
				definition.Minimum, definition.Maximum = &min, &max
			}
			if _, _, err := compileStateDefinitions([]PackStateDefinition{definition}); err != nil {
				t.Fatal(err)
			}
			definition.UpdatePolicy.MaxChangePerTurn = 10
			if _, _, err := compileStateDefinitions([]PackStateDefinition{definition}); err == nil {
				t.Fatal("ambiguous model budget accepted")
			}
		})
	}
}

func TestUnknownRuleReferencesFailWithEmptyRuleCatalog(t *testing.T) {
	condition := plot.FactCondition{Kind: "rule_result", FactID: "missing", Status: "succeeded"}
	if err := validateFactReferences(condition, story.Definition{}, nil, false); err == nil {
		t.Fatal("missing rule accepted")
	}
	root := writeV3Pack(t, `{"spatial":1,"rules":1}`, `[{"id":"office","kind":"place","name":"Office","connections":[]}]`, `[]`)
	path := filepath.Join(root, "story.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pack map[string]any
	if err = json.Unmarshal(data, &pack); err != nil {
		t.Fatal(err)
	}
	pack["plot"] = map[string]any{"revision": "plot.v1", "nodes": []any{map[string]any{"id": "event", "after": []string{}, "at_minute": 600, "condition": "When appropriate", "development": "Continue", "audience": []string{"player"}, "requirements": []plot.FactCondition{condition}, "on_unmet": "skip", "terminal": false}}}
	data, err = json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(root); err == nil || !strings.Contains(err.Error(), "unknown condition rule missing") {
		t.Fatalf("dangling rule load error=%v", err)
	}
}

func TestMistEmbersNarrativeActionsDoNotRequireSuccessFlags(t *testing.T) {
	loaded, err := Load(filepath.Join("packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	def := loaded.Definition
	if len(def.ActionRules) != 1 || def.ActionRules[0].ID != "clinic-stealth" || def.ActionRules[0].Risk == nil {
		t.Fatalf("rules=%+v", def.ActionRules)
	}
	if len(def.ActionRules[0].SuccessEffects)+len(def.ActionRules[0].FailureEffects) != 0 {
		t.Fatal("ordinary consequences remain fixed")
	}
	if def.Plot != nil || def.Progression == nil || len(def.Progression.Developments) != 1 || len(def.Progression.InitialPlans) != 2 {
		t.Fatal("open-world pressure and personal plans are not configured independently")
	}
	for _, state := range def.StateDefinitions {
		if state.UpdatePolicy.Kind != "model" {
			t.Fatalf("state policy=%+v", state)
		}
	}
}
