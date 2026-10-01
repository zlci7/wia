package content

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
)

func TestLoadV3SpatialContract(t *testing.T) {
	root := writeV3Pack(t, `{"spatial":1}`, `[
        {"id":"district","kind":"region","name":"调查区","connections":[]},
        {"id":"office","kind":"place","parent":"district","name":"事务所","connections":["street"]},
        {"id":"street","kind":"place","parent":"district","name":"街道","connections":["office"]}
      ]`, `[{"bystander_id":"bystander:clerk","name":"书记员","initial_location":"office"}]`)
	pack, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Definition.SchemaVersion != SchemaV3 || pack.Definition.Capabilities["spatial"] != 1 {
		t.Fatalf("capability manifest was not normalized: %+v", pack.Definition)
	}
	for id, want := range map[string]string{"player": "office", "npc:reporter": "office", "bystander:clerk": "office"} {
		if got := pack.Definition.InitialLocations[id]; got != want {
			t.Fatalf("initial location %s = %q, want %q", id, got, want)
		}
	}
	if len(pack.Definition.Locations) != 3 || pack.Definition.Locations[0].Kind != "region" || !pack.Definition.Locations[1].Public {
		t.Fatalf("locations were not normalized: %+v", pack.Definition.Locations)
	}
}

func TestV3MinimalTemplateLoads(t *testing.T) {
	pack, err := Load(filepath.Join("..", "..", "..", "docs", "phase12", "templates", "story-pack-v3-minimal"))
	if err != nil {
		t.Fatal(err)
	}
	if pack.Definition.Summary.ID != "spatial-example" || pack.Definition.InitialLocations["npc:bookseller"] != "bookshop" {
		t.Fatalf("template normalization: %+v", pack.Definition)
	}
}

func TestMistEmbersStageCRulesAreCompiledAsData(t *testing.T) {
	pack, err := Load(filepath.Join("packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	if pack.Definition.Capabilities["rules"] != 1 || len(pack.Definition.ActionRules) != 1 || pack.Definition.Progression == nil || len(pack.Definition.Progression.InitialPlans) != 2 {
		t.Fatal("fixed risk rules and independent personal plans were not compiled")
	}
	for _, id := range []string{"investigation_strain", "mirror_dissonance"} {
		definition, ok := stateByID(pack.Definition.StateDefinitions, id)
		if !ok || definition.UpdatePolicy.Kind != "model" {
			t.Fatalf("rule state %s=%+v", id, definition)
		}
	}
}

func TestActionRuleMayDependOnAnotherDeclaredRule(t *testing.T) {
	pack := StoryPack{ActionRules: []story.ActionRule{
		{ID: "first", Name: "第一条", Guidance: "先执行。", SuccessText: "完成。"},
		{ID: "second", Name: "第二条", Guidance: "随后执行。", SuccessText: "完成。", Conditions: []plot.FactCondition{{Kind: "rule_result", FactID: "first", Status: "succeeded"}}},
	}}
	rules, err := compileActionRules(pack, story.Definition{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules=%+v", rules)
	}

	pack.ActionRules[1].Conditions[0].FactID = "missing"
	if _, err = compileActionRules(pack, story.Definition{}, nil); err == nil || !strings.Contains(err.Error(), "unknown condition rule missing") {
		t.Fatalf("unknown cross-rule reference error=%v", err)
	}
}

func TestRuleStateReferencesMustMatchIntegerTypeAndEntityScope(t *testing.T) {
	minimum, maximum := 0, 10
	playerState := story.StateDefinition{ID: "player_meter", Type: "integer", Minimum: &minimum, Maximum: &maximum, Scope: "player", UpdatePolicy: story.StateUpdatePolicy{Kind: "rule_only"}}
	npcState := story.StateDefinition{ID: "npc_meter", Type: "integer", Minimum: &minimum, Maximum: &maximum, Scope: "npc", UpdatePolicy: story.StateUpdatePolicy{Kind: "rule_only"}}
	flagState := story.StateDefinition{ID: "flag", Type: "boolean", Scope: "all", UpdatePolicy: story.StateUpdatePolicy{Kind: "readonly"}}
	definition := story.Definition{StateDefinitions: []story.StateDefinition{playerState, npcState, flagState}}
	characters := map[string]bool{"npc:reporter": true}

	for _, tc := range []struct {
		name string
		rule story.ActionRule
	}{
		{
			name: "effect cannot target npc-only state through player actor",
			rule: story.ActionRule{ID: "effect_scope", Name: "效果", Guidance: "执行", SuccessText: "完成", SuccessEffects: []story.RuleEffect{{Kind: "state_delta", EntityID: "actor", StateID: "npc_meter", Delta: 1}}},
		},
		{
			name: "condition cannot read npc-only state through player actor",
			rule: story.ActionRule{ID: "condition_scope", Name: "条件", Guidance: "执行", SuccessText: "完成", Conditions: []plot.FactCondition{{Kind: "state_at_least", EntityID: "actor", FactID: "npc_meter", Value: 1}}},
		},
		{
			name: "threshold condition requires integer state",
			rule: story.ActionRule{ID: "condition_type", Name: "类型", Guidance: "执行", SuccessText: "完成", Conditions: []plot.FactCondition{{Kind: "state_at_least", EntityID: "actor", FactID: "flag", Value: 1}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := compileActionRules(StoryPack{ActionRules: []story.ActionRule{tc.rule}}, definition, characters); err == nil {
				t.Fatal("invalid state reference was accepted")
			}
		})
	}

	if err := validateFactReferences(plot.FactCondition{Kind: "state_at_least", EntityID: "npc:reporter", FactID: "player_meter", Value: 1}, definition, characters, false); err == nil {
		t.Fatal("plot condition read a player-only state from an NPC")
	}
}

func TestLoadV3AuthorDefinedMechanicsWithoutBuiltInStateNames(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "npcs"), 0o755); err != nil {
		t.Fatal(err)
	}
	story := `{"schema_version":3,"requires":{"spatial":1,"state":1,"relations":1,"items":1},"game_id":"custom-mechanics","revision":"custom-mechanics.v1","mode":"open","title":"自定义机制","description":"测试","gameplay":"测试","background":"","rules":"","author_facts":"","player":{"name":"旅人","profile":"","editable":true,"initial_state":{"ritual_stability":61}},"opening":"开始。","initial_location":"hall","clock":"第 1 日 09:00","locations":[{"id":"hall","kind":"place","name":"大厅","connections":[]}],"npcs":["npcs/warden.json"],"bystanders":[],"state_definitions":[{"id":"ritual_stability","name":"仪式稳定度","type":"integer","minimum":0,"maximum":100,"default":50,"scope":"all","projection":"self","knowledge":"owner","update_policy":{"kind":"bounded_proposal","max_change_per_turn":7}}],"relation_definitions":[{"id":"confidence","name":"确信","minimum":-20,"maximum":20,"default":0,"max_change_per_turn":3,"projection":"hidden"}],"initial_relations":[{"subject_id":"npc:warden","target_id":"player","relation_type":"confidence","value":2}],"item_definitions":[{"id":"token","name":"凭证","projection":"holder"}],"item_instances":[{"instance_id":"token-1","definition_id":"token","holder_id":"player"}]}`
	npc := `{"definition_id":"warden","revision":"warden.v1","entity_id":"npc:warden","name":"守门人","role":"守门人","profile":"守门。","knowledge":"","initial_concerns":"","initial_location":"hall","initial_state":{"ritual_stability":44}}`
	if err := os.WriteFile(filepath.Join(root, "story.json"), []byte(story), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "npcs", "warden.json"), []byte(npc), 0o644); err != nil {
		t.Fatal(err)
	}
	pack, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := pack.Definition.InitialStates["player"]["ritual_stability"].Integer; got != 61 {
		t.Fatalf("player state=%d", got)
	}
	if got := pack.Definition.InitialStates["npc:warden"]["ritual_stability"].Integer; got != 44 {
		t.Fatalf("npc state=%d", got)
	}
	if pack.Catalog.Player.InitialState != nil {
		t.Fatalf("catalog leaked initial state: %+v", pack.Catalog.Player.InitialState)
	}
	if len(pack.Definition.RelationDefinitions) != 1 || len(pack.Definition.InitialRelations) != 1 || len(pack.Definition.InitialItems) != 1 {
		t.Fatalf("mechanics were not compiled: %+v", pack.Definition)
	}
}

func TestLegacyPackDoesNotGainSpatialRuntimeState(t *testing.T) {
	pack, err := Load(filepath.Join("packs", "orbital-repair"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := pack.Definition.InitialLocations["player"]; exists {
		t.Fatalf("legacy player position was added: %+v", pack.Definition.InitialLocations)
	}
	if len(pack.Definition.Locations) == 0 || pack.Definition.Locations[0].Kind != "" || pack.Definition.Locations[0].Public {
		t.Fatalf("legacy location shape changed: %+v", pack.Definition.Locations)
	}
}

func TestLoadV3RejectsUnsupportedOrIncompleteSpatialData(t *testing.T) {
	validLocations := `[{"id":"office","kind":"place","name":"事务所","connections":[]}]`
	for _, tc := range []struct {
		name, requires, locations, bystanders, want, code string
	}{
		{"unknown capability", `{"spatial":1,"state":1}`, validLocations, `[]`, "requires", "capability_manifest_unsupported"},
		{"wrong capability version", `{"spatial":2}`, validLocations, `[]`, "requires", "capability_manifest_unsupported"},
		{"region as initial place", `{"spatial":1}`, `[{"id":"office","kind":"region","name":"调查区","connections":[]}]`, `[]`, "initial_location", "reference_invalid"},
		{"bystander without position", `{"spatial":1}`, validLocations, `[{"bystander_id":"bystander:clerk","name":"书记员"}]`, "bystanders.initial_location", "reference_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeV3Pack(t, tc.requires, tc.locations, tc.bystanders))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			var validation *packValidationError
			if !errors.As(err, &validation) || validation.Code != tc.code {
				t.Fatalf("validation error = %#v, want code %q", validation, tc.code)
			}
			issue := packIssue("candidate", err)
			if issue.File != "candidate/story.json" || issue.Field != validation.Field || issue.Code != tc.code {
				t.Fatalf("public issue = %+v", issue)
			}
		})
	}
}

func TestLoadV3RejectsInvalidMechanicsData(t *testing.T) {
	tests := []struct {
		name      string
		requires  string
		mutate    func(map[string]any)
		mutateNPC func(map[string]any)
		field     string
		code      string
	}{
		{
			name:     "capability data mismatch",
			requires: `{"spatial":1}`,
			mutate: func(story map[string]any) {
				story["state_definitions"] = []any{validIntegerStateDefinition()}
			},
			field: "requires capability/data mismatch",
			code:  "capability_manifest_unsupported",
		},
		{
			name:     "duplicate state definition",
			requires: `{"spatial":1,"state":1}`,
			mutate: func(story map[string]any) {
				definition := validIntegerStateDefinition()
				story["state_definitions"] = []any{definition, definition}
			},
			field: "state/relations/items: invalid or duplicate state definition",
			code:  "field_invalid",
		},
		{
			name:     "state default outside range",
			requires: `{"spatial":1,"state":1}`,
			mutate: func(story map[string]any) {
				definition := validIntegerStateDefinition()
				definition["default"] = 11
				story["state_definitions"] = []any{definition}
			},
			field: "state/relations/items: state focus default: integer outside range",
			code:  "field_invalid",
		},
		{
			name:     "state update budget is invalid",
			requires: `{"spatial":1,"state":1}`,
			mutate: func(story map[string]any) {
				definition := validIntegerStateDefinition()
				definition["update_policy"] = map[string]any{"kind": "bounded_proposal", "max_change_per_turn": 0}
				story["state_definitions"] = []any{definition}
			},
			code: "schema_invalid",
		},
		{
			name:     "rule managed state requires rules capability",
			requires: `{"spatial":1,"state":1}`,
			mutate: func(story map[string]any) {
				definition := validIntegerStateDefinition()
				definition["update_policy"] = map[string]any{"kind": "rule_only"}
				story["state_definitions"] = []any{definition}
			},
			field: "requires capability/data mismatch",
			code:  "capability_manifest_unsupported",
		},
		{
			name:     "state visibility is invalid",
			requires: `{"spatial":1,"state":1}`,
			mutate: func(story map[string]any) {
				definition := validIntegerStateDefinition()
				definition["knowledge"] = "everyone"
				story["state_definitions"] = []any{definition}
			},
			code: "schema_invalid",
		},
		{
			name:     "player initial state is unknown",
			requires: `{"spatial":1,"state":1}`,
			mutate: func(story map[string]any) {
				story["state_definitions"] = []any{validIntegerStateDefinition()}
				story["player"].(map[string]any)["initial_state"] = map[string]any{"unknown": 1}
			},
			field: "state/relations/items: state unknown is not defined for player",
			code:  "field_invalid",
		},
		{
			name:     "npc initial state is outside range",
			requires: `{"spatial":1,"state":1}`,
			mutate: func(story map[string]any) {
				story["state_definitions"] = []any{validIntegerStateDefinition()}
			},
			mutateNPC: func(npc map[string]any) {
				npc["initial_state"] = map[string]any{"focus": 99}
			},
			field: "state/relations/items: state focus for npc:reporter: integer outside range",
			code:  "field_invalid",
		},
		{
			name:     "relation references unknown entity",
			requires: `{"spatial":1,"relations":1}`,
			mutate: func(story map[string]any) {
				story["relation_definitions"] = []any{map[string]any{"id": "trust", "name": "信任", "minimum": -10, "maximum": 10, "default": 0, "max_change_per_turn": 2, "projection": "hidden"}}
				story["initial_relations"] = []any{map[string]any{"subject_id": "npc:reporter", "target_id": "npc:missing", "relation_type": "trust", "value": 1}}
			},
			field: "state/relations/items: invalid initial relation",
			code:  "field_invalid",
		},
		{
			name:     "relation is self directed",
			requires: `{"spatial":1,"relations":1}`,
			mutate: func(story map[string]any) {
				story["relation_definitions"] = []any{validRelationDefinition()}
				story["initial_relations"] = []any{map[string]any{"subject_id": "player", "target_id": "player", "relation_type": "trust", "value": 1}}
			},
			field: "state/relations/items: invalid initial relation",
			code:  "field_invalid",
		},
		{
			name:     "relation value is outside range",
			requires: `{"spatial":1,"relations":1}`,
			mutate: func(story map[string]any) {
				story["relation_definitions"] = []any{validRelationDefinition()}
				story["initial_relations"] = []any{map[string]any{"subject_id": "npc:reporter", "target_id": "player", "relation_type": "trust", "value": 11}}
			},
			field: "state/relations/items: invalid initial relation",
			code:  "field_invalid",
		},
		{
			name:     "duplicate directed relation",
			requires: `{"spatial":1,"relations":1}`,
			mutate: func(story map[string]any) {
				story["relation_definitions"] = []any{validRelationDefinition()}
				relation := map[string]any{"subject_id": "npc:reporter", "target_id": "player", "relation_type": "trust", "value": 1}
				story["initial_relations"] = []any{relation, relation}
			},
			field: "state/relations/items: invalid initial relation",
			code:  "field_invalid",
		},
		{
			name:     "item has two owners",
			requires: `{"spatial":1,"items":1}`,
			mutate: func(story map[string]any) {
				story["item_definitions"] = []any{map[string]any{"id": "token", "name": "凭证", "projection": "public"}}
				story["item_instances"] = []any{map[string]any{"instance_id": "token-1", "definition_id": "token", "holder_id": "player", "location_id": "office"}}
			},
			code: "schema_invalid",
		},
		{
			name:     "item references unknown holder",
			requires: `{"spatial":1,"items":1}`,
			mutate: func(story map[string]any) {
				story["item_definitions"] = []any{map[string]any{"id": "token", "name": "凭证", "projection": "public"}}
				story["item_instances"] = []any{map[string]any{"instance_id": "token-1", "definition_id": "token", "holder_id": "npc:missing"}}
			},
			field: "state/relations/items: invalid item instance",
			code:  "field_invalid",
		},
		{
			name:     "duplicate item definition",
			requires: `{"spatial":1,"items":1}`,
			mutate: func(story map[string]any) {
				definition := map[string]any{"id": "token", "name": "凭证", "projection": "public"}
				story["item_definitions"] = []any{definition, definition}
			},
			field: "state/relations/items: invalid item definition",
			code:  "field_invalid",
		},
		{
			name:     "duplicate item instance",
			requires: `{"spatial":1,"items":1}`,
			mutate: func(story map[string]any) {
				story["item_definitions"] = []any{map[string]any{"id": "token", "name": "凭证", "projection": "public"}}
				instance := map[string]any{"instance_id": "token-1", "definition_id": "token", "holder_id": "player"}
				story["item_instances"] = []any{instance, instance}
			},
			field: "state/relations/items: invalid item instance",
			code:  "field_invalid",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeV3Pack(t, tc.requires, `[{"id":"office","kind":"place","name":"事务所","connections":[]}]`, `[]`)
			path := filepath.Join(root, "story.json")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var story map[string]any
			if err = json.Unmarshal(body, &story); err != nil {
				t.Fatal(err)
			}
			tc.mutate(story)
			body, err = json.Marshal(story)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.mutateNPC != nil {
				npcPath := filepath.Join(root, "npcs", "reporter.json")
				npcBody, readErr := os.ReadFile(npcPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var npc map[string]any
				if err = json.Unmarshal(npcBody, &npc); err != nil {
					t.Fatal(err)
				}
				tc.mutateNPC(npc)
				npcBody, err = json.Marshal(npc)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(npcPath, npcBody, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			_, err = Load(root)
			var validation *packValidationError
			if !errors.As(err, &validation) || validation.File != "story.json" || validation.Field != tc.field || validation.Code != tc.code {
				t.Fatalf("validation error = %#v, want field %q", validation, tc.field)
			}
			issue := packIssue("candidate", err)
			if issue.File != "candidate/story.json" || issue.Field != tc.field || issue.Code != tc.code {
				t.Fatalf("public issue = %+v", issue)
			}
		})
	}
}

func validIntegerStateDefinition() map[string]any {
	return map[string]any{"id": "focus", "name": "专注", "type": "integer", "minimum": 0, "maximum": 10, "default": 5, "scope": "all", "projection": "self", "knowledge": "owner", "update_policy": map[string]any{"kind": "readonly"}}
}

func validRelationDefinition() map[string]any {
	return map[string]any{"id": "trust", "name": "信任", "minimum": -10, "maximum": 10, "default": 0, "max_change_per_turn": 2, "projection": "hidden"}
}

func writeV3Pack(t *testing.T, requires, locations, bystanders string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "npcs"), 0o755); err != nil {
		t.Fatal(err)
	}
	story := `{"schema_version":3,"requires":` + requires + `,"game_id":"v3-test","revision":"v3-test.pack.v1","mode":"open","title":"空间测试","description":"测试","gameplay":"移动","background":"","rules":"","author_facts":"","player":{"name":"调查员","profile":"","editable":true},"opening":"你在事务所。","initial_location":"office","clock":"第 1 日 09:00","locations":` + locations + `,"npcs":["npcs/reporter.json"],"bystanders":` + bystanders + `}`
	if err := os.WriteFile(filepath.Join(root, "story.json"), []byte(story), 0o644); err != nil {
		t.Fatal(err)
	}
	npc := `{"definition_id":"reporter","revision":"reporter.v1","entity_id":"npc:reporter","name":"记者","role":"记者","profile":"正在调查。","knowledge":"","initial_concerns":"","initial_location":"office"}`
	if err := os.WriteFile(filepath.Join(root, "npcs", "reporter.json"), []byte(npc), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
