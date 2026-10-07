package content

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gameagent/backend/internal/story"
)

func TestStartingOptionsWorkForOtherSettingsAndRoundTrip(t *testing.T) {
	root := v4TemplateRoot(t)
	changeV4JSON(t, root, "story.json", func(v map[string]any) {
		v["title"] = "港口协商"
		v["starting_options"] = []any{
			map[string]any{"id": "visitor", "title": "来访者", "description": "普通来访者"},
			map[string]any{"id": "worker", "title": "港口工人", "description": "从自己的工作出发", "player": map[string]any{"name": "工人", "profile": "认识港口日常事务，掌握普通修理技能。", "editable": false, "known_locations": []string{"reception"}}, "opening": "你来商谈修理工作。", "items": []any{}},
		}
	})
	pack, err := Load(root)
	if err != nil || len(pack.Catalog.StartingOptions) != 2 {
		t.Fatal("generic start", err)
	}
	selected, err := story.WithStartingOption(pack.Definition, "worker")
	if err != nil || selected.Summary.Player.Name != "工人" || selected.Opening != "你来商谈修理工作。" || !reflect.DeepEqual(selected.KnownLocations, []string{"reception"}) {
		t.Fatal("derived definition", err)
	}
	selected.InitialLocations["player"] = "changed"
	if pack.Definition.InitialLocations["player"] != "reception" {
		t.Fatal("mutable package")
	}
	copyRoot := t.TempDir()
	for name, body := range exportPackageFiles(pack) {
		target := filepath.Join(copyRoot, name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	copy, err := Load(copyRoot)
	if err != nil || copy.Digest != pack.Digest || !reflect.DeepEqual(copy.Definition.StartingOptions, pack.Definition.StartingOptions) {
		t.Fatal("start export", err)
	}
	data, _ := json.Marshal(pack.Definition)
	var frozen story.Definition
	if err := json.Unmarshal(data, &frozen); err != nil || !reflect.DeepEqual(frozen.StartingOptions, pack.Definition.StartingOptions) {
		t.Fatal("frozen start", err)
	}
}

func TestStartingOptionsRejectInvalidAuthoredReferences(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
	}{
		{"place", map[string]any{"initial_location": "absent"}},
		{"known place", map[string]any{"player": map[string]any{"name": "访客", "profile": "个人经历", "known_locations": []string{"absent"}}}},
		{"state", map[string]any{"player": map[string]any{"name": "访客", "profile": "个人经历", "initial_state": map[string]any{"invented": true}}}},
		{"item", map[string]any{"items": []any{map[string]any{"instance_id": "x", "definition_id": "absent", "holder_id": "player"}}}},
		{"other owner", map[string]any{"items": []any{map[string]any{"instance_id": "x", "definition_id": "absent", "holder_id": "npc:contact"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := v4TemplateRoot(t)
			changeV4JSON(t, root, "story.json", func(v map[string]any) {
				option := map[string]any{"id": "visitor", "title": "来访者", "description": "普通人"}
				for key, value := range tc.fields {
					option[key] = value
				}
				v["starting_options"] = []any{option}
			})
			if _, err := Load(root); err == nil {
				t.Fatal("invalid start accepted")
			}
		})
	}
}
