package turn

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestSceneAllContinuationsShareFourCoreCalls(t *testing.T) {
	s, run, parts, move, store := sceneCheckpointFixture(t)
	s.Definition.Materials = []story.Material{{ID: "reference", Delivery: "detail", Visibility: "author", Summary: "远处规则参考", Body: "RULE_REFERENCE"}}
	core := 0
	g := &sceneSequenceGenerator{reply: func(_ int, req model.TextRequest) (string, error) {
		if strings.Contains(req.System, "结构化回合意图") {
			return wire.MarshalJSON(TurnIntent{IntentType: "act", Visibility: "public", Fragments: parts}), nil
		}
		core++
		switch core {
		case 1:
			return `{"needs_material":["reference"]}`, nil
		case 2:
			if !strings.Contains(req.Input, "RULE_REFERENCE") {
				t.Fatal("frozen supplement disappeared")
			}
			return wire.MarshalJSON(map[string]any{"needs_resolution": sceneResolutionRequest{RuleID: "risk", InputFragmentIndex: 1, PrefixBeats: []sceneBeat{move}, PrefixElapsedMinutes: 2}}), nil
		case 3:
			return `{"schema_revision":`, nil
		case 4:
			prepared, found, err := store.ReadActionResolutionForInput(t.Context(), run.InputID)
			if err != nil || !found {
				t.Fatal("prepared result missing", err)
			}
			status := "succeeded"
			if prepared.Roll > prepared.Target {
				status = "failed"
			}
			b := sceneBeat{LocalID: "b2", Kind: "action_result", ActorID: "player", OffsetMinutes: 3, Basis: []string{"input:1", "beat:b1"}, Content: "结果已确定。", Recipients: []string{"player"}, Bystanders: []string{}, Projections: outcomeProjectionFixture("结果已确定。", []string{"player"}), Status: sceneString(status), Attempt: &sceneAttempt{Content: "尝试风险行动", InputFragmentIndex: sceneIndex(1)}}
			d := sceneComplete(run.Input, move, b)
			d.InputMap = []sceneInput{{Text: parts[0].Text, IntentType: "act", Visibility: "public", BeatIDs: []string{"b1"}, Status: "succeeded"}, {Text: parts[1].Text, IntentType: "act", Visibility: "public", BeatIDs: []string{"b2"}, Status: status, ActionRuleID: "risk"}}
			return wire.MarshalJSON(d), nil
		default:
			t.Fatal("fifth core call escaped the attempt budget")
			return "", nil
		}
	}}
	out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), store, g, s, run)
	if err != nil || report.CoreCalls != 4 || report.ContextSupplements != 1 || report.ResolutionChecks != 1 || report.Repairs != 1 || out.ActionResolution == nil {
		t.Fatalf("one attempt: %+v %v", report, err)
	}
}

func TestSceneForeignOwnerMemoryFailsBeforeProvider(t *testing.T) {
	s := sceneContextFixture()
	s.LongMemory["npc:a"] = MemoryContext{Tail: []memory.MemorySource{{Scope: "npc:b", Content: "FOREIGN_PRIVATE_BODY"}}}
	g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) {
		t.Fatal("foreign memory reached the provider")
		return "", nil
	}}
	_, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, wiaworld.Run{Input: "问好"})
	if !errors.Is(err, ErrContextSourceMissing) || report.CoreCalls != 0 {
		t.Fatalf("owner isolation: %v %v", report, err)
	}
}

func TestScenePlayerPrivateSpeechKeepsRawScopeWithoutSpatialCapability(t *testing.T) {
	s := sceneContextFixture()
	delete(s.Definition.Capabilities, "spatial")
	run := wiaworld.Run{RunID: "private-player", Input: "我私下告诉甲一个消息。", AddresseeID: "npc:a"}
	b := sceneDialogue("b1", "player", "这是给你的消息。", "input:0")
	b.Scope, b.Recipients = sceneString("private"), []string{"npc:a"}
	d := sceneComplete(run.Input, b)
	d.InputMap[0].Visibility, d.InputMap[0].AddresseeID = "private", "npc:a"
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range out.Perceptions {
		if p.RecipientID == "npc:b" && strings.Contains(p.Content, "这是给你的消息") {
			t.Fatal("player private speech broadcast")
		}
	}
	d.Beats[0].Scope, d.Beats[0].Recipients = sceneString("public"), []string{}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d); err == nil {
		t.Fatal("private player input changed into public dialogue")
	}
}

func TestScenePlansKeepOwnerBasisAndOneUpdatePerTurn(t *testing.T) {
	s := sceneContextFixture()
	s.Definition.Progression = &plot.OpenDefinition{}
	s.OpenProgress = &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{}, ExternalApplied: map[string]string{}, DevelopmentChecks: map[string]string{}}
	run := wiaworld.Run{RunID: "plans", Input: "我请甲核对明天的船期。", BaseContextEpoch: 1}
	b := sceneDialogue("b1", "npc:a", "我会去核对。", "input:0")
	b.Effects.PlanUpdates = []scenePlanUpdate{{OwnerID: "npc:a", LocalPlanID: "check", Content: "核对船期", Status: "active", ReviewAfterMinutes: 30, Basis: []string{"input:0"}}}
	d := sceneComplete(run.Input, b)
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d)
	if err != nil {
		t.Fatal(err)
	}
	p := out.OpenProgress.Plans[0]
	if p.ID != "npc:a:plans:check" || p.Version != 1 || slices.Contains(p.SourceIDs, "input:0") || len(s.OpenProgress.Plans) != 0 {
		t.Fatalf("plan owner or durable basis: %+v", p)
	}
	d.Beats[0].Effects.PlanUpdates[0].Basis = []string{scenePersonalID("npc:b", "same-record")}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d); err == nil {
		t.Fatal("foreign personal basis accepted for a plan")
	}
	d.Beats[0].Effects.PlanUpdates[0].Basis = []string{"input:0"}
	d.Beats = append(d.Beats, sceneDialogue("b2", "npc:a", "还是同一计划。", "beat:b1"))
	d.Beats[1].Effects.PlanUpdates = slices.Clone(d.Beats[0].Effects.PlanUpdates)
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d); err == nil {
		t.Fatal("same plan was updated twice in one candidate")
	}
}

func TestSceneFuturePlanSelectionDoesNotGrantEarlyActions(t *testing.T) {
	s := sceneContextFixture()
	s.Characters[2].InScene = false
	s.Positions["npc:c"] = "other-place"
	minute, _ := plot.ClockMinute(s.Summary.Clock)
	s.OpenProgress = &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{{ID: "npc:c:later", OwnerID: "npc:c", Content: "检查", SourceIDs: []string{"opening"}, NextCheck: minute + 20, Status: "active", Version: 1}}}
	run := wiaworld.Run{RunID: "future", Input: "我留在原处。"}
	selected := selectedSceneEntities(s, run)
	if !slices.Contains(selected, "npc:c") {
		t.Fatal("forecast fixture did not select the future owner")
	}
	b := sceneObservation("b1", "npc:c", "我检查了远处房间。", scenePersonalID("npc:c", "definition:"+s.Definition.Revision+":npc:c"))
	d := sceneComplete(run.Input, b)
	if _, err := sceneCompileFixture(t, s, run, selected, d); err == nil {
		t.Fatal("forecast selection enabled an early off-scene action")
	}
}
