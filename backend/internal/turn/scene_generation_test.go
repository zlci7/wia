package turn

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type sceneSequenceGenerator struct {
	requests []model.TextRequest
	reply    func(int, model.TextRequest) (string, error)
}

func (g *sceneSequenceGenerator) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, req)
	text, err := g.reply(len(g.requests), req)
	return model.TextResponse{Text: text}, err
}

func TestSceneGenerationOrdinaryInteractionUsesOneCoreCall(t *testing.T) {
	s := sceneContextFixture()
	run := wiaworld.Run{RunID: "single", Input: "我请他们核对登记。"}
	draft := sceneComplete(run.Input, sceneDialogue("b1", "npc:a", refusalSpeech, "input:0"), sceneDialogue("b2", "npc:b", "可以先核对登记。", "beat:b1"))
	g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) { return wire.MarshalJSON(draft), nil }}
	out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, run)
	if err != nil || len(g.requests) != 1 || report.CoreCalls != 1 || report.Repairs != 0 || out.Narrative == "" {
		t.Fatalf("one coherent scene: %v %v", report, err)
	}
	if strings.Contains(g.requests[0].System, "雾都") || strings.Contains(g.requests[0].System, "马丁") {
		t.Fatal("generic prompt contains a script constant")
	}
}

func TestSceneGenerationReadAndCorrectionShareOneAttempt(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			s := sceneContextFixture()
			s.Definition.Materials = []story.Material{{ID: "archive", Delivery: "detail", Visibility: "author", Summary: "远处档案", Body: "ARCHIVE_BODY"}}
			run := wiaworld.Run{RunID: "read", Input: "我检查档案。"}
			beat := sceneObservation("b1", "player", "我发现登记时间。", s.Definition.Materials[0].SourceID(s.Definition.Revision))
			g := &sceneSequenceGenerator{reply: func(index int, req model.TextRequest) (string, error) {
				switch index {
				case 1:
					return `{"needs_material":["archive"]}`, nil
				case 2:
					if !strings.Contains(req.Input, "ARCHIVE_BODY") {
						t.Fatal("authorized body not supplied")
					}
					return `{"schema_revision":`, nil
				default:
					if success {
						return wire.MarshalJSON(sceneComplete(run.Input, beat)), nil
					}
					return `{"schema_revision":`, nil
				}
			}}
			out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, run)
			if (err == nil) != success || report.CoreCalls != 3 || report.ContextSupplements != 1 || report.Repairs != 1 {
				t.Fatalf("shared quota: %v %v", report, err)
			}
			if !success && (out.Narrative != "" || len(out.Events) > 0) {
				t.Fatal("failed correction returned partial scene")
			}
		})
	}
}

func TestSceneGenerationRepeatedOrUnauthorizedReadFailsWithoutResettingQuota(t *testing.T) {
	s := sceneContextFixture()
	s.Definition.Materials = []story.Material{{ID: "archive", Delivery: "detail", Visibility: "author", Summary: "远处档案", Body: "ARCHIVE_BODY"}}
	for _, id := range []string{"archive", "forbidden"} {
		g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) { return fmt.Sprintf(`{"needs_material":[%q]}`, id), nil }}
		_, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, wiaworld.Run{Input: "检查档案"})
		if err == nil || report.CoreCalls > 2 || report.Repairs != 0 {
			t.Fatalf("read quota: %v %v", report, err)
		}
	}
}

func sceneCheckpointFixture(t *testing.T) (Snapshot, wiaworld.Run, []InputFragment, sceneBeat, *storage.WorldStore) {
	t.Helper()
	s := ruleTestSnapshot()
	s.Definition.Revision = "port.rule.v1"
	s.Definition.Capabilities = map[string]int{"spatial": 1, "state": 1}
	s.Definition.Locations = []story.Location{{ID: "outside", Kind: "place", Connections: []string{"room"}}, {ID: "room", Kind: "place", Connections: []string{"outside"}}}
	s.Summary.Clock = "2189-12-31 23:55"
	s.SceneVersion = 1
	s.Positions["player"] = "outside"
	s.PositionSources = map[string]string{"player": "opening"}
	s.SceneLocation = "outside"
	s.SceneViews = []SceneView{{Recipient: "player", Content: "门外", SourceIDs: []string{"opening"}, Version: 1}}
	s.LongMemory = map[string]MemoryContext{}
	s.Sources = map[string]SourceMetadata{"opening": {ID: "opening"}}
	s.Perceptions = map[string][]wiaworld.Perception{}
	run := wiaworld.Run{RunID: "checkpoint", InputID: "input:checkpoint", Input: "先进入房间。再尝试风险行动。"}
	parts := []InputFragment{{Text: "先进入房间。", ActorID: "player", IntentType: "act", Visibility: "public"}, {Text: "再尝试风险行动。", ActorID: "player", IntentType: "act", Visibility: "public", ActionRuleID: "risk"}}
	move := sceneBeat{LocalID: "b1", Kind: "action_result", ActorID: "player", OffsetMinutes: 2, Basis: []string{"input:0"}, Content: "进入房间。", Recipients: []string{"player"}, Bystanders: []string{}, Projections: []actionProjection{{Recipient: "player", Content: "我进入房间。"}}, Status: sceneString("succeeded"), Attempt: &sceneAttempt{Content: "进入房间", InputFragmentIndex: sceneIndex(0)}, Effects: sceneEffects{Movements: []sceneMovement{{EntityID: "player", From: "outside", To: "room", Route: []string{"outside", "room"}}}}}
	store, err := storage.OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return s, run, parts, move, store
}

func TestSceneCheckpointUsesMovedStateAndFixedResultAcrossRepair(t *testing.T) {
	s, run, parts, move, store := sceneCheckpointFixture(t)
	core := 0
	g := &sceneSequenceGenerator{reply: func(_ int, req model.TextRequest) (string, error) {
		if strings.Contains(req.System, "结构化回合意图") {
			return wire.MarshalJSON(TurnIntent{IntentType: "act", Visibility: "public", Fragments: parts}), nil
		}
		core++
		if core == 1 {
			return wire.MarshalJSON(map[string]any{"needs_resolution": sceneResolutionRequest{RuleID: "risk", InputFragmentIndex: 1, PrefixBeats: []sceneBeat{move}, PrefixElapsedMinutes: 2}}), nil
		}
		prepared, found, err := store.ReadActionResolutionForInput(t.Context(), run.InputID)
		if err != nil || !found {
			t.Fatalf("checkpoint not prepared: %v", err)
		}
		status := "succeeded"
		if prepared.Roll > prepared.Target {
			status = "failed"
		}
		result := sceneBeat{LocalID: "b2", Kind: "action_result", ActorID: "player", OffsetMinutes: 3, Basis: []string{"input:1", "beat:b1"}, Content: "我执行了风险行动。", Recipients: []string{"player"}, Bystanders: []string{}, Projections: []actionProjection{{Recipient: "player", Content: "风险行动结果已确定。"}}, Status: sceneString(status), Attempt: &sceneAttempt{Content: "尝试风险行动", InputFragmentIndex: sceneIndex(1)}}
		d := sceneComplete(run.Input, move, result)
		d.InputMap = []sceneInput{{Text: parts[0].Text, IntentType: "act", Visibility: "public", BeatIDs: []string{"b1"}, Status: "succeeded"}, {Text: parts[1].Text, IntentType: "act", Visibility: "public", BeatIDs: []string{"b2"}, Status: status, ActionRuleID: "risk"}}
		if core == 2 {
			d.Beats[0].Content = "改写前段以改善结果"
		}
		if !strings.Contains(req.Input, "程序已准备判定") {
			t.Fatal("fixed result missing from continuation")
		}
		return wire.MarshalJSON(d), nil
	}}
	out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), store, g, s, run)
	if err != nil {
		t.Fatal(err)
	}
	if report.CoreCalls != 3 || report.ResolutionChecks != 1 || report.Repairs != 1 || out.Positions["player"] != "room" || len(out.PositionChanges) != 1 || s.Positions["player"] != "outside" {
		t.Fatalf("checkpoint: %+v", report)
	}
	prepared, _, err := store.ReadActionResolutionForInput(t.Context(), run.InputID)
	if err != nil {
		t.Fatal(err)
	}
	want := 5
	if prepared.Roll > prepared.Target {
		want = 12
	}
	if out.States["player"]["strain"].Value.Integer != want || out.ActionResolution.Roll != prepared.Roll {
		t.Fatal("fixed rule effects or durable roll changed")
	}
	if _, err := plot.ClockMinute(out.Clock); err != nil {
		t.Fatal(err)
	}
}

func TestSceneGenerationHonorsAlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) {
		t.Fatal("cancelled scene called provider")
		return "", nil
	}}
	_, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(ctx, nil, g, sceneContextFixture(), wiaworld.Run{Input: "问好"})
	if err != context.Canceled || report.CoreCalls != 0 {
		t.Fatalf("cancel: %v %v", report, err)
	}
}

func TestSceneGenerationRejectsResponseAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	run := wiaworld.Run{RunID: "late-cancel", Input: "问好"}
	g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) {
		cancel()
		return wire.MarshalJSON(sceneComplete(run.Input, sceneDialogue("b1", "npc:a", "你好。", "input:0"))), nil
	}}
	out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(ctx, nil, g, sceneContextFixture(), run)
	if err != context.Canceled || report.CoreCalls != 1 || len(out.Events) != 0 {
		t.Fatalf("late cancel: %+v %v", report, err)
	}
}

func TestSceneGenerationArrivalUsesSharedSupplement(t *testing.T) {
	for _, readFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(readFirst), func(t *testing.T) {
			s := sceneContextFixture()
			s.Definition.Locations = []story.Location{{ID: "workplace", Kind: "place", Connections: []string{"dock"}}, {ID: "dock", Kind: "place", Connections: []string{"workplace"}}}
			s.Definition.Materials = []story.Material{{ID: "reference", Visibility: "author", Delivery: "detail", Summary: "远处资料", Body: "FROZEN_REFERENCE"}}
			s.Positions["npc:c"], s.Characters[2].InScene = "dock", false
			run := wiaworld.Run{RunID: "extension", Input: "我去码头查看。"}
			move := scenePlayerAction("b1", "我抵达码头。")
			move.OffsetMinutes = 5
			move.Effects.Movements = []sceneMovement{{EntityID: "player", From: "workplace", To: "dock", Route: []string{"workplace", "dock"}}}
			c := sceneDialogue("b2", "npc:c", "有什么需要？", scenePersonalID("npc:c", "definition:"+s.Definition.Revision+":npc:c"))
			c.OffsetMinutes = 5
			g := &sceneSequenceGenerator{reply: func(i int, req model.TextRequest) (string, error) {
				if readFirst && i == 1 {
					return `{"needs_material":["reference"]}`, nil
				}
				if !readFirst && i == 2 && !strings.Contains(req.Input, "本人标识 npc:c") {
					t.Fatal("participant expansion omitted its identity")
				}
				return wire.MarshalJSON(sceneComplete(run.Input, move, c)), nil
			}}
			out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, run)
			if (err == nil) == readFirst || report.CoreCalls != 2 || report.ContextSupplements != 1 || report.Repairs != 0 {
				t.Fatalf("shared runtime/frozen supplement: %+v %v", report, err)
			}
			if readFirst && len(out.Events) != 0 {
				t.Fatal("exhausted expansion returned partial effects")
			}
		})
	}
}
