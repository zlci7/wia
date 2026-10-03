package content

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gameagent/backend/internal/plot"
)

func TestWorldInformationValidatesCalendarAndKnownPlaces(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"unsupported calendar", func(v map[string]any) { v["calendar"] = map[string]any{"kind": "invented"} }},
		{"missing calendar", func(v map[string]any) { delete(v, "calendar") }},
		{"invalid date", func(v map[string]any) { v["clock"] = "2026-02-29 09:00" }},
		{"relative date with calendar", func(v map[string]any) { v["clock"] = "第 1 日 09:00" }},
		{"missing capability", func(v map[string]any) { delete(v["requires"].(map[string]any), "world_info") }},
		{"unknown place", func(v map[string]any) { v["player"].(map[string]any)["known_locations"] = []string{"absent"} }},
		{"duplicate known place", func(v map[string]any) {
			v["player"].(map[string]any)["known_locations"] = []string{"reception", "reception"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := v4TemplateRoot(t)
			changeV4JSON(t, root, "story.json", tc.change)
			if _, err := Load(root); err == nil {
				t.Fatal("invalid world information accepted")
			}
		})
	}
}

func TestDatedScheduleCompilationPreservesAuthoredOffsets(t *testing.T) {
	root := v4TemplateRoot(t)
	changeV4JSON(t, root, "narrative/progression.json", func(v map[string]any) {
		v["external_schedules"] = []any{map[string]any{"id": "notice", "at_minute": 570, "material_id": "world-truth"}}
	})
	pack, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := plot.ClockMinute("2026-01-01 09:30")
	if got := pack.Definition.Progression.ExternalSchedules[0].AtMinute; got != want {
		t.Fatalf("schedule=%d want=%d", got, want)
	}
	var source struct {
		ExternalSchedules []plot.ExternalSchedule `json:"external_schedules"`
	}
	if err := json.Unmarshal(exportPackageFiles(pack)["narrative/progression.json"], &source); err != nil {
		t.Fatal(err)
	}
	if source.ExternalSchedules[0].AtMinute != 570 {
		t.Fatal("export must preserve authored offsets")
	}
	copyRoot := t.TempDir()
	for name, body := range exportPackageFiles(pack) {
		target := filepath.Join(copyRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	copy, err := Load(copyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if copy.Definition.Progression.ExternalSchedules[0].AtMinute != want || copy.Digest != pack.Digest {
		t.Fatal("roundtrip double-shifted schedule or changed package")
	}
}
