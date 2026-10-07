package turn

import (
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func sceneContextFixture() Snapshot {
	s := interactionSnapshot("港口办事处")
	for _, owner := range []string{"player", "npc:a", "npc:b"} {
		r := memory.MemorySource{ID: "same-record", Scope: owner, Seq: 1, RunID: "earlier", EventID: owner + ":heard", Content: owner + "答应核对明日航班。"}
		s.LongMemory[owner] = MemoryContext{Tail: []memory.MemorySource{r}, Archive: []memory.MemorySource{r}}
	}
	return s
}

func TestSceneContextRetainsAllOwnersAndNormalizesSeparately(t *testing.T) {
	s := sceneContextFixture()
	run := wiaworld.Run{RunID: "scene", Input: "我问他们航班的约定。"}
	selected := selectedSceneEntities(s, run)
	s.sceneEntities = selected
	m, available := selectStoryMaterials(s, "scene", "", composeScene(s, run, selected))
	if len(available) != 0 {
		t.Fatal("unexpected frozen fixture files")
	}
	request, report, err := (ContextComposer{Scope: ContextScope{Purpose: "scene", SelectedEntityIDs: selected}}).Build(m, m.System, 4096)
	if err != nil {
		t.Fatal(err)
	}
	ledger := sceneLedger(s, selected, report.SelectedSources)
	for _, owner := range []string{"player", "npc:a", "npc:b"} {
		alias := scenePersonalID(owner, "same-record")
		if !strings.Contains(request.Input, owner+"答应核对明日航班") || !strings.Contains(request.Input, alias) {
			t.Fatalf("owner view overwritten: %s", owner)
		}
		ids, err := ledger.resolve(owner, []string{alias}, false)
		if err != nil || len(ids) != 1 || ids[0] != owner+":heard" {
			t.Fatalf("owner normalization: %s %v %v", owner, ids, err)
		}
	}
	if _, err := ledger.resolve("npc:a", []string{scenePersonalID("npc:b", "same-record")}, false); err == nil {
		t.Fatal("one owner's alias grants another owner's knowledge")
	}
	if report.InputTokens > 12000 || strings.Index(request.Input, "稳定人物档案") > strings.Index(request.Input, "本轮依据") {
		t.Fatal("shared capacity or stable/dynamic ordering violated")
	}
}

func TestSceneDirectoryHasSelectedOwnerAccessWithoutProvidingTheBody(t *testing.T) {
	s := sceneContextFixture()
	s.sceneEntities = []string{"player", "npc:a"}
	s.Definition.Materials = []story.Material{
		{ID: "a-private", Visibility: "owner", OwnerID: "npc:a", Summary: "本人资料", Body: "PRIVATE_A", Delivery: "detail"},
		{ID: "b-private", Visibility: "owner", OwnerID: "npc:b", Summary: "另一个人资料", Body: "PRIVATE_B", Delivery: "detail"},
		{ID: "unrelated", Visibility: "author", Summary: "远处材料", Body: "UNREAD_BODY", Delivery: "detail"},
	}
	m, available := selectStoryMaterials(s, "scene", "", composeScene(s, wiaworld.Run{Input: "我问甲。"}, s.sceneEntities))
	request, report, err := (ContextComposer{}).Build(m, m.System, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := available["a-private"]; !ok {
		t.Fatal("selected owner directory absent")
	}
	if _, ok := available["b-private"]; ok || strings.Contains(request.Input, "PRIVATE_B") {
		t.Fatal("unselected private owner leaked")
	}
	if strings.Contains(request.Input, "UNREAD_BODY") {
		t.Fatal("unrelated directory body automatically loaded")
	}
	if !strings.HasSuffix(request.Input, m.Final) {
		t.Fatal("current input must follow the directory and retrieval notice")
	}
	l := sceneLedger(s, s.sceneEntities, report.SelectedSources)
	if _, err := l.resolve("player", []string{s.Definition.Materials[2].SourceID(s.Definition.Revision)}, true); err == nil {
		t.Fatal("directory reference counted as provided body")
	}
}

func TestSceneOpeningAssessmentProvidesRequiredBodyAndKeepsOtherPlansPrivate(t *testing.T) {
	s := sceneOpenFixture()
	s.sceneEntities = []string{"player", "npc:a"}
	s.Definition.Materials = []story.Material{{ID: "pressure", Visibility: "author", Delivery: "core", Summary: "队列压力", Body: "CURRENT_PRESSURE"}}
	s.Definition.Progression.Developments = []plot.Development{{ID: "queue", MaterialIDs: []string{"pressure"}}}
	s.OpenProgress.Plans = []wiaworld.PersonalPlan{{ID: "remote-plan", OwnerID: "npc:c", Content: "UNSELECTED_PRIVATE_PLAN", Status: "active", NextCheck: 9999999999}}
	m, _ := selectStoryMaterials(s, "scene", "", composeScene(s, wiaworld.Run{Input: "我问甲。"}, s.sceneEntities))
	req, report, err := (ContextComposer{Scope: ContextScope{Purpose: "scene", SelectedEntityIDs: s.sceneEntities}}).Build(m, m.System, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Input, "opening_assessment") || strings.Count(req.Input, "CURRENT_PRESSURE") != 1 || strings.Contains(req.Input, "UNSELECTED_PRIVATE_PLAN") {
		t.Fatal("current assessment or owner selection violated")
	}
	ledger := sceneLedger(s, s.sceneEntities, report.SelectedSources)
	if _, err := ledger.resolve("world", []string{s.Definition.Materials[0].SourceID(s.Definition.Revision)}, true); err != nil {
		t.Fatal("required assessment body has no authorized source", err)
	}
}

func TestSceneUsesOneBudgetAndKeepsCompleteRequiredGroups(t *testing.T) {
	s := sceneContextFixture()
	selected := []string{"player", "npc:a", "npc:b"}
	m := composeScene(s, wiaworld.Run{Input: "检查约定"}, selected)
	_, _, err := (ContextComposer{Window: model.WindowLimits{ContextTokens: 2048, OutputTokens: 1024}}).Build(m, m.System, 512)
	if err == nil {
		t.Fatal("required shared context silently cut into per-owner fragments")
	}
}

func TestSceneContextPriorityPreservesStablePrefixAndLatestInput(t *testing.T) {
	m := Material{Prefix: []Section{{Name: "stable", Text: "STABLE"}}, Required: "CURRENT_INPUT", Optional: []Section{{Name: "important", Text: strings.Repeat("known ", 60), Priority: 200}, {Name: "detail", Text: strings.Repeat("detail ", 400), Priority: 50}}}
	request, report, err := (ContextComposer{Window: model.WindowLimits{ContextTokens: 800, OutputTokens: 100}}).Build(m, "system", 100)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(request.Input, "detail ") || !strings.Contains(request.Input, "known ") || !strings.HasPrefix(request.Input, "STABLE") || !strings.HasSuffix(request.Input, "CURRENT_INPUT") || report.Excluded != 1 {
		t.Fatalf("selection: %v", report)
	}
}
