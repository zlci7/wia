package turn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type planSourceRepairGenerator struct{ requests []model.TextRequest }

func (g *planSourceRepairGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, request)
	source := "UNPROVIDED_SECRET_VALUE"
	if len(g.requests) > 1 {
		source = "own-stimulus"
	}
	return model.TextResponse{Text: wire.MarshalJSON(NPCDecision{Silent: true, PlanUpdates: []planUpdate{{ID: "plan", Content: "根据本人获准的新消息重新调查", SourceIDs: []string{source}, Status: "active", ReviewAfterMinutes: 20}}})}, nil
}

func TestPlanSourceRepairNamesProvidedSourcesWithoutLeakingRejectedValues(t *testing.T) {
	snapshot := Snapshot{OpenProgress: &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{{ID: "plan", OwnerID: "npc:a"}}}}
	call := &ContextGenerator{providedSources: []string{"own-stimulus", "material:revision:own-knowledge"}}
	bad := NPCDecision{PlanUpdates: []planUpdate{{ID: "plan", Content: "新计划", SourceIDs: []string{"UNPROVIDED_SECRET_VALUE"}, Status: "active", ReviewAfterMinutes: 20}}}
	err := validatePlanUpdates(snapshot, "npc:a", &bad, call)
	var detail *GenerationError
	if !errors.Is(err, ErrContextSourceMissing) || !errors.As(err, &detail) || detail.Code != "context_source_missing" || detail.Field != "plan_updates.source_ids" || !strings.Contains(detail.Expected, "plan-index=0; source-index=0") {
		t.Fatal("source failure lacks its field and local contract", err)
	}
	logger := &recordingLogger{}
	(&ContextGenerator{logger: logger}).recordJSONValidation(err)
	if strings.Contains(err.Error()+logger.String(), "UNPROVIDED_SECRET_VALUE") {
		t.Fatal("rejected response value entered diagnostics")
	}
	g := &planSourceRepairGenerator{}
	var decision NPCDecision
	repairs, err := GenerateJSONCheckedMetrics(context.Background(), g, "S", "I", &decision, 100, nil, []string{"speech", "action_intent", "silent", "memory"}, func() error { return validatePlanUpdates(snapshot, "npc:a", &decision, call) })
	if err != nil || repairs != 1 || len(g.requests) != 2 || decision.PlanUpdates[0].SourceIDs[0] != "own-stimulus" {
		t.Fatal("source contract did not reach the technical repair", err, repairs)
	}
	for _, text := range []string{"plan_updates.source_ids", "own-stimulus", "material:revision:own-knowledge"} {
		if !strings.Contains(g.requests[1].System, text) {
			t.Fatal("repair omitted provided source contract", text)
		}
	}
	if strings.Contains(g.requests[1].System, "UNPROVIDED_SECRET_VALUE") {
		t.Fatal("rejected source value entered repair instructions")
	}
}

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
			if validatePlanUpdates(snapshot, "npc:a", &NPCDecision{PlanUpdates: []planUpdate{bad}}, call) == nil {
				t.Fatal("invalid plan update accepted")
			}
		})
	}
	if err := validatePlanUpdates(snapshot, "npc:a", &NPCDecision{PlanUpdates: []planUpdate{update}}, call); err != nil {
		t.Fatal(err)
	}
	update.SourceIDs = []string{"material:revision:own-knowledge"}
	if err := validatePlanUpdates(snapshot, "npc:a", &NPCDecision{PlanUpdates: []planUpdate{update}}, call); err != nil {
		t.Fatal("provided owned material was rejected:", err)
	}
}

func TestPlanBasisResolvesOnlyProvidedOwnedHistoryWithoutDroppingEvidence(t *testing.T) {
	snapshot := Snapshot{OpenProgress: &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{{ID: "plan", OwnerID: "npc:a"}}}, LongMemory: map[string]MemoryContext{"npc:a": {
		Digest:  memory.MemoryDigest{Scope: "npc:a", Revision: 2, Sources: []string{"perception:1", "memory:2", "perception:3"}},
		Archive: []memory.MemorySource{{Scope: "npc:a", ID: "perception:1", EventID: "own:projection"}, {Scope: "npc:a", ID: "memory:2", EventID: "own:projection"}, {Scope: "npc:a", ID: "perception:3", EventID: "earlier:own"}, {Scope: "npc:b", ID: "perception:4", EventID: "foreign:event"}, {Scope: "npc:a", ID: "correction:1"}},
	}}}
	call := &ContextGenerator{providedSources: []string{"digest:npc:a:2", "perception:1", "perception:4", "correction:1", "material:r:knowledge"}}
	decision := NPCDecision{PlanUpdates: []planUpdate{{ID: "plan", Content: "本人依据已有经历调整计划", SourceIDs: []string{"digest:npc:a:2", "perception:1", "material:r:knowledge"}, Status: "active", ReviewAfterMinutes: 20}}}
	if err := validatePlanUpdates(snapshot, "npc:a", &decision, call); err != nil || wire.MarshalJSON(decision.PlanUpdates[0].SourceIDs) != `["own:projection","earlier:own","material:r:knowledge"]` {
		t.Fatal("owned digest and record bases did not preserve all durable evidence", err, decision)
	}
	for _, id := range []string{"perception:3", "perception:4", "correction:1"} {
		bad := NPCDecision{PlanUpdates: []planUpdate{{ID: "plan", Content: "本人计划", SourceIDs: []string{id}, Status: "active", ReviewAfterMinutes: 20}}}
		if validatePlanUpdates(snapshot, "npc:a", &bad, call) == nil {
			t.Fatal("unprovided, foreign or non-event record accepted as durable basis", id)
		}
	}
	digest := snapshot.LongMemory["npc:a"]
	digest.Digest.Sources = nil
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("perception:%d", i+10)
		digest.Digest.Sources = append(digest.Digest.Sources, id)
		digest.Archive = append(digest.Archive, memory.MemorySource{Scope: "npc:a", ID: id, EventID: fmt.Sprintf("own:%d", i)})
	}
	snapshot.LongMemory["npc:a"] = digest
	if _, err := canonicalPlanSources(snapshot, "npc:a", []string{"digest:npc:a:2"}); err == nil {
		t.Fatal("large digest silently dropped some of its evidence")
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
