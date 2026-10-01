package turn

import (
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func TestPersonalPlansAreCurrentOwnedStateRatherThanInitialFiles(t *testing.T) {
	snapshot := materialTestSnapshot()
	snapshot.Definition.Materials = append(snapshot.Definition.Materials, story.Material{ID: "old-plan", Purpose: "npc_plan", Visibility: "owner", OwnerID: "npc:a", Delivery: "core", Summary: "OLD_PLAN_DIRECTORY", Body: "OLD_INITIAL_PLAN"})
	snapshot.OpenProgress = &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{{ID: "plan", OwnerID: "npc:a", Content: "CURRENT_CANCELLED_PLAN", Status: "cancelled", SourceIDs: []string{"basis"}, Version: 2}, {ID: "foreign-plan", OwnerID: "npc:b", Content: "FOREIGN_PLAN", Status: "active", SourceIDs: []string{"foreign-basis"}, Version: 1}}}
	material, _ := selectStoryMaterials(snapshot, "npc", "npc:a", Material{System: "S", Required: "request"})
	request, _, err := (ContextComposer{}).Build(material, material.System, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request.Input, "CURRENT_CANCELLED_PLAN") || strings.Contains(request.Input, "OLD_INITIAL_PLAN") || strings.Contains(request.Input, "OLD_PLAN_DIRECTORY") || strings.Contains(request.Input, "FOREIGN_PLAN") {
		t.Fatal("a plan file restored an old intention or exposed another person's plan")
	}
}

func TestPlanDueSelectionIsBoundedAndDoesNotPrescribeActions(t *testing.T) {
	plans := []wiaworld.PersonalPlan{{ID: "c", OwnerID: "npc:c", NextCheck: 50, Status: "active", LastCheck: -1}, {ID: "b", OwnerID: "npc:b", NextCheck: 40, Status: "active", LastCheck: -1}, {ID: "a", OwnerID: "npc:a", NextCheck: 40, Status: "active", LastCheck: -1}, {ID: "paused", OwnerID: "npc:d", NextCheck: 0, Status: "paused", LastCheck: -1}}
	owners := plot.DuePlanOwners(plans, 50, 2)
	if strings.Join(owners, ",") != "npc:a,npc:b" {
		t.Fatalf("due owners=%v", owners)
	}
	plans[2].LastCheck = 50
	if got := strings.Join(plot.DuePlanOwners(plans, 50, 2), ","); got != "npc:b,npc:c" {
		t.Fatalf("repeated check was selected: %s", got)
	}
}

func TestPlanUpdateRequiresItsOwnersProvidedSources(t *testing.T) {
	snapshot := materialTestSnapshot()
	snapshot.OpenProgress = &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{{ID: "plan", OwnerID: "npc:a"}, {ID: "foreign", OwnerID: "npc:b"}}}
	snapshot.Perceptions = map[string][]wiaworld.Perception{"npc:a": {{SourceEventID: "old-own-history", Content: "Earlier knowledge omitted from this request."}}}
	call := &ContextGenerator{providedSources: []string{"own-stimulus", "material:revision:own-knowledge"}}
	update := planUpdate{ID: "plan", Content: "I will review the changed clinic", SourceIDs: []string{"own-stimulus"}, Status: "active", ReviewAfterMinutes: 20}
	for _, test := range []struct {
		name   string
		mutate func(*planUpdate)
	}{
		{"owner", func(p *planUpdate) { p.ID = "foreign" }},
		{"source", func(p *planUpdate) { p.SourceIDs = []string{"author-secret"} }},
		{"unprovided-history", func(p *planUpdate) { p.SourceIDs = []string{"old-own-history"} }},
		{"unprovided-stimulus", func(p *planUpdate) { p.SourceIDs = []string{"unprovided-stimulus"} }},
		{"time", func(p *planUpdate) { p.ReviewAfterMinutes = 0 }},
		{"status", func(p *planUpdate) { p.Status = "success" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := update
			test.mutate(&bad)
			if validatePlanUpdates(snapshot, "npc:a", NPCDecision{PlanUpdates: []planUpdate{bad}}, call) == nil {
				t.Fatal("invalid plan update accepted")
			}
		})
	}
	if err := validatePlanUpdates(snapshot, "npc:a", NPCDecision{PlanUpdates: []planUpdate{update}}, call); err != nil {
		t.Fatal(err)
	}
	update.SourceIDs = []string{"material:revision:own-knowledge"}
	if err := validatePlanUpdates(snapshot, "npc:a", NPCDecision{PlanUpdates: []planUpdate{update}}, call); err != nil {
		t.Fatal("provided owned material was rejected:", err)
	}
}

func TestLegacyWorldEvaluationUsesLatestItemPlacement(t *testing.T) {
	snapshot := Snapshot{Definition: story.Definition{Capabilities: map[string]int{"items": 1}}, Plot: &plot.Definition{Revision: "legacy"}}
	output := Output{Clock: "第 1 日 10:00", Items: map[string]wiaworld.ItemInstance{"mirror": {InstanceID: "mirror", HolderID: "player", SourceEvent: "transfer"}}}
	material := composePlot(snapshot, wiaworld.Run{}, plot.Node{ID: "review"}, &output)
	if !strings.Contains(material.Required, `"holder_id":"player"`) || !strings.Contains(material.Required, `"source_event_id":"transfer"`) {
		t.Fatal("world evaluation omitted latest item placement or source")
	}
}

func TestPrivateResultBystandersAndUnsupportedMovementAreRejected(t *testing.T) {
	_, err := prepareCoordination(Snapshot{}, wiaworld.Run{RunID: "r"}, TurnIntent{Visibility: "private", AddresseeID: "npc:a"}, Output{}, hostResult{Outcomes: []hostActionResult{{ActionID: "r:player-action", Recipients: []string{"player", "npc:a"}, Bystanders: []string{"bystander:x"}}}})
	if err == nil {
		t.Fatal("private player outcome leaked through bystanders")
	}
	if validateMechanicCapabilities(Snapshot{}, hostResult{Movements: []movementResult{{EntityID: "npc:a"}}}) == nil {
		t.Fatal("unsupported movement was silently ignored")
	}
}

func TestFormalMistMaterialsFitNPCAndWorldRequestsWithoutSecretsLeaking(t *testing.T) {
	pack, err := content.Load(filepath.Join("..", "content", "packs", "mist-embers"))
	if err != nil {
		t.Fatal(err)
	}
	def := pack.Definition
	progress, err := InitialOpenProgress(def)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{Definition: def, Characters: def.Characters, Positions: def.InitialLocations, Summary: wiaworld.WorldSummary{Clock: def.Clock}, OpenProgress: progress}
	snapshot.SceneViews = initialSceneViews(snapshot)
	for _, character := range def.Characters {
		material := composeNPC(snapshot, def, character, "", "observe", StageInput{NewStimulus: "自己的计划到达检查时间。"}, "", 5)
		call := NewContextGenerator(Deps{}, "owner", &materialTestGenerator{responses: []string{`{}`}}, material, snapshot, wiaworld.Run{}, "npc", character.EntityID, 5, "T").(*ContextGenerator)
		request, _, err := call.composer.Build(call.material, material.System+call.systemSuffix, structuredTurnOutputTokens)
		if err != nil {
			t.Fatalf("%s context: %v", character.EntityID, err)
		}
		if strings.Contains(request.Input, "银镜真实能力、代价") || strings.Contains(request.Input, "作者真实结果") || strings.Contains(request.Input, "开场时在废弃诊所") {
			t.Fatalf("author item material leaked to %s", character.EntityID)
		}
	}
}
