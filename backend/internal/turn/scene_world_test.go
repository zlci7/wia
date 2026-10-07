package turn

import (
	"errors"
	"fmt"
	"gameagent/backend/internal/model"
	"slices"
	"strings"
	"testing"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func sceneWorldBeat(id, content, basis string, offset int) sceneBeat {
	return sceneBeat{LocalID: id, Kind: "world_change", ActorID: "world", OffsetMinutes: offset, Basis: []string{basis}, Content: content, Recipients: []string{"player"}, Bystanders: []string{}, Projections: outcomeProjectionFixture(content, []string{"player"})}
}

func TestSceneWorldStructuredGateUsesProgramEvidenceAndOccurrencePremise(t *testing.T) {
	for _, onUnmet := range []string{"defer", "skip"} {
		t.Run(onUnmet, func(t *testing.T) {
			s := sceneContextFixture()
			minute, _ := plot.ClockMinute(s.Summary.Clock)
			node := plot.Node{ID: "pickup", AtMinute: minute, Audience: []string{"player"}, Requirements: []plot.FactCondition{{Kind: "item_at", FactID: "unique-tool", LocationID: "workplace"}}, OnUnmet: onUnmet}
			s.Plot = &plot.Definition{Revision: "gate.v1", Nodes: []plot.Node{node}}
			s.PlotProgress.Nodes = map[string]plot.NodeState{}
			run := wiaworld.Run{RunID: "gate", Input: "我查看交接。"}
			id := "definition:gate.v1:pickup"
			b := sceneWorldBeat("b1", "工具交给了玩家。", id, 0)
			b.Effects.ItemTransfers = []sceneItemTransfer{{InstanceID: "unique-tool", FromLocationID: "workplace", ToHolderID: "player"}}
			d := sceneComplete(run.Input, b)
			d.ProgressUpdates = []sceneProgress{{Type: "legacy_node", ID: node.ID, Status: "occurred", Content: "工具交接完成。", Basis: []string{id, "beat:b1"}, BeatIDs: []string{"b1"}}}
			out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
			if err != nil || out.Items["unique-tool"].HolderID != "player" || !slices.ContainsFunc(out.PlotProgress.Nodes[node.ID].Evidence, func(id string) bool { return strings.HasPrefix(id, "fact:item_at:") }) {
				t.Fatal("occurrence rechecked the premise it had already changed", err)
			}
			item := s.Items["unique-tool"]
			item.LocationID, item.HolderID = "", "npc:a"
			s.Items["unique-tool"] = item
			if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
				t.Fatal("unmet structured gate permitted an occurrence")
			}
			observe := sceneObservation("b1", "player", "工具仍由甲保管。", "input:0")
			d = sceneComplete(run.Input, observe)
			status := "deferred"
			if onUnmet == "skip" {
				status = "skipped"
			}
			d.ProgressUpdates = []sceneProgress{{Type: "legacy_node", ID: node.ID, Status: status, Content: "工具不在交接处。", Basis: []string{id}, BeatIDs: []string{}}}
			out, err = sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
			if err != nil || out.PlotProgress.Nodes[node.ID].Status != status {
				t.Fatal("program evidence missing from a valid unmet assessment", err)
			}
		})
	}
}

func TestSceneOpenAssessmentAlsoPreservesDueGeneratedEvent(t *testing.T) {
	s := sceneOpenFixture()
	minute, _ := plot.ClockMinute(s.Summary.Clock)
	s.Definition.Materials = []story.Material{{ID: "pressure", Visibility: "author", Delivery: "core", Body: "没有新变化。"}}
	s.Definition.Progression.Developments = []plot.Development{{ID: "queue", MaterialIDs: []string{"pressure"}, LocationIDs: []string{"workplace"}}}
	s.Definition.EventGeneration = &plot.EventGenerationPolicy{MaxActive: 1, Locations: []string{"workplace"}}
	s.GeneratedEvents.Active = []GeneratedEvent{{Node: plot.Node{ID: "pending", AtMinute: minute, Audience: []string{"player"}}, StartID: "opening", TriggerID: "opening", Premise: "渡轮即将靠岸。", Location: "workplace"}}
	run := wiaworld.Run{RunID: "dual-assessment", Input: "我观察港口。"}
	a := sceneObservation("b1", "player", "这里没有新压力。", "input:0")
	b := sceneWorldBeat("b2", "渡轮靠岸。", "definition:generated.v1:pending", 0)
	d := sceneComplete(run.Input, a, b)
	d.ProgressUpdates = []sceneProgress{{Type: "development", ID: "queue", Status: "deferred", Content: "压力没有变化。", Basis: []string{s.Definition.Materials[0].SourceID(s.Definition.Revision)}, BeatIDs: []string{}}, {Type: "generated_event", ID: "pending", Status: "occurred", Content: "渡轮已经靠岸。", Basis: []string{"beat:b2"}, BeatIDs: []string{"b2"}}}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil || out.GeneratedEvents.Completed != 1 || out.OpenProgress.DevelopmentChecks["queue"] == "" {
		t.Fatal("open assessment concealed a due generated continuation", err)
	}
	d.Beats = []sceneBeat{a}
	d.NarrativeBlocks[0].BeatIDs = []string{"b1"}
	d.InputMap[0].BeatIDs = []string{"b1"}
	d.ProgressUpdates = d.ProgressUpdates[:1]
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
		t.Fatal("pending generated assessment silently omitted")
	}
}

func TestSceneLegacyTransitionKeepsActualPresenceAndEarlierSpeech(t *testing.T) {
	s := sceneContextFixture()
	s.Definition.Capabilities["spatial"] = 0
	s.Characters[2].InScene = false
	run := wiaworld.Run{RunID: "legacy-transition", Input: "我先询问，再去码头。"}
	a := sceneDialogue("b1", "npc:a", refusalSpeech, "input:0")
	move := scenePlayerAction("b2", "我来到码头。")
	move.OffsetMinutes = 5
	move.Effects.LegacyScene = &sceneLegacyScene{Content: "夜间码头", Characters: []string{"npc:c"}}
	c := sceneDialogue("b3", "npc:c", "你来这里找谁？", scenePersonalID("npc:c", "definition:"+s.Definition.Revision+":npc:c"))
	c.OffsetMinutes = 5
	d := sceneComplete(run.Input, a, move, c)
	_, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d)
	var expansion *sceneActorExpansion
	if !errors.As(err, &expansion) || expansion.EntityID != "npc:c" {
		t.Fatal("legacy arrival did not require frozen identity material", err)
	}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d)
	if err != nil || out.Scene != "夜间码头" || !slices.Equal(out.SceneCharacters, []string{"npc:c"}) {
		t.Fatal("legacy scene transition lost", err)
	}
	for _, p := range out.Perceptions {
		if p.RecipientID == "npc:c" && strings.Contains(p.Content, refusalSpeech) {
			t.Fatal("legacy arrival received earlier dialogue")
		}
	}
	d.Beats[2].Basis = []string{"beat:b1"}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d); err == nil {
		t.Fatal("legacy late arrival used earlier dialogue")
	}
	d.Beats[2].Basis = []string{scenePersonalID("npc:c", "definition:"+s.Definition.Revision+":npc:c")}
	s.Definition.Capabilities["spatial"] = 1
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d); err == nil {
		t.Fatal("spatial world used legacy presence as a route")
	}
}

func TestSceneSelectedWorldMaterialExpansionSharesReadQuota(t *testing.T) {
	for _, alreadyRead := range []bool{false, true} {
		t.Run(wire.MarshalJSON(alreadyRead), func(t *testing.T) {
			s := sceneOpenFixture()
			s.Definition.Materials = []story.Material{{ID: "pressure", Visibility: "author", Delivery: "detail", Summary: "船期变更依据", Body: "WORLD_NOTICE_BODY"}, {ID: "extra", Visibility: "author", Delivery: "detail", Summary: "额外资料", Body: "EXTRA_BODY"}}
			for index := range 4 {
				s.Definition.Materials = append(s.Definition.Materials, story.Material{ID: fmt.Sprintf("owner-detail-%d", index), Visibility: "owner", OwnerID: "npc:a", Delivery: "detail", Summary: "甲的记录", Body: "PERSONAL_DETAIL", EntityIDs: []string{"npc:a"}, ItemIDs: []string{"unique-tool"}})
			}
			minute, _ := plot.ClockMinute(s.Summary.Clock)
			s.Definition.Progression.ExternalSchedules = []plot.ExternalSchedule{{ID: "queue", MaterialID: "pressure", AtMinute: minute + 1}}
			run := wiaworld.Run{RunID: "world-material", Input: "我问现在有什么变化。"}
			b := sceneWorldBeat("b1", "船期有所变化。", s.Definition.Materials[0].SourceID(s.Definition.Revision), 1)
			d := sceneComplete(run.Input, b)
			d.ProgressUpdates = []sceneProgress{{Type: "external_schedule", ID: "queue", Status: "occurred", Content: "船期发生变化。", OffsetMinutes: 1, Basis: []string{"beat:b1"}, BeatIDs: []string{"b1"}}}
			g := &sceneSequenceGenerator{reply: func(index int, req model.TextRequest) (string, error) {
				if alreadyRead && index == 1 {
					return `{"needs_material":["extra"]}`, nil
				}
				if !alreadyRead && index == 2 && !strings.Contains(req.Input, "WORLD_NOTICE_BODY") {
					t.Fatal("selected assessment body absent from continuation")
				}
				return wire.MarshalJSON(d), nil
			}}
			out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, run)
			if (err != nil) != alreadyRead || report.CoreCalls != 2 || report.ContextSupplements != 1 || report.Repairs != 0 {
				t.Fatalf("world supplement quota: %+v %v", report, err)
			}
			if alreadyRead && len(out.Events) != 0 {
				t.Fatal("exhausted world supplement returned partial state")
			}
		})
	}
}

func sceneOpenFixture() Snapshot {
	s := sceneContextFixture()
	s.Definition.Progression = &plot.OpenDefinition{}
	s.OpenProgress = &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{}, ExternalApplied: map[string]string{}, DevelopmentChecks: map[string]string{}}
	return s
}

func TestSceneExternalScheduleUsesActualTimeAndRequiresAssessment(t *testing.T) {
	s := sceneOpenFixture()
	minute, _ := plot.ClockMinute(s.Summary.Clock)
	s.Definition.Materials = []story.Material{{ID: "notice", Visibility: "author", Delivery: "core", Body: "六分钟后公布新的船期。"}}
	s.Definition.Progression.ExternalSchedules = []plot.ExternalSchedule{{ID: "timetable", AtMinute: minute + 6, MaterialID: "notice"}}
	run := wiaworld.Run{RunID: "schedule", Input: "我等待通知。"}
	a := sceneObservation("b1", "player", "还在等候。", "input:0")
	a.OffsetMinutes = 1
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, a)); err != nil {
		t.Fatal("future schedule became mandatory too early", err)
	}
	a.OffsetMinutes = 6
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, a)); err == nil {
		t.Fatal("due schedule silently omitted")
	}
	id := s.Definition.Materials[0].SourceID(s.Definition.Revision)
	b := sceneWorldBeat("b2", "新的船期已经张贴。", id, 6)
	d := sceneComplete(run.Input, a, b)
	d.ProgressUpdates = []sceneProgress{{Type: "external_schedule", ID: "timetable", Status: "occurred", Content: "通知已公布。", OffsetMinutes: 6, Basis: []string{id, "beat:b2"}, BeatIDs: []string{"b2"}}}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil || out.OpenProgress.ExternalApplied["timetable"] == "" || len(s.OpenProgress.ExternalApplied) != 0 || out.Clock != "2190-01-01 00:01" {
		t.Fatalf("actual schedule: %+v %v", out.OpenProgress, err)
	}
	if _, ok := EventByID(out.Events, out.OpenProgress.ExternalApplied["timetable"]); !ok {
		t.Fatal("applied schedule source not compiled")
	}
}

func TestSceneDevelopmentNoChangeAdvancesCursorWithoutVisibleWorldEvent(t *testing.T) {
	s := sceneOpenFixture()
	s.Definition.Materials = []story.Material{{ID: "pressure", Visibility: "author", Delivery: "core", Body: "港口平静，排队没有新变化。"}}
	s.Definition.Progression.Developments = []plot.Development{{ID: "queue", MaterialIDs: []string{"pressure"}, LocationIDs: []string{"workplace"}}}
	run := wiaworld.Run{RunID: "no-change", Input: "我查看排队情况。"}
	b := sceneObservation("b1", "player", "这里仍然平静。", "input:0")
	d := sceneComplete(run.Input, b)
	d.ProgressUpdates = []sceneProgress{{Type: "development", ID: "queue", Status: "deferred", Content: "没有新的合理变化。", Basis: []string{s.Definition.Materials[0].SourceID(s.Definition.Revision)}, BeatIDs: []string{}}}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil || out.OpenProgress.DevelopmentCursor != "queue" || out.OpenProgress.DevelopmentChecks["queue"] == "" {
		t.Fatal("development assessment lost", err)
	}
	for _, e := range out.Events {
		if e.EventType == "plot_result" || e.EventType == "world_assessed" {
			t.Fatal("deferred development became a world occurrence")
		}
	}
}

func TestScenePlanReviewFallbackAndExplicitUpdateShareOneVersion(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "updated"}[explicit], func(t *testing.T) {
			s := sceneOpenFixture()
			minute, _ := plot.ClockMinute(s.Summary.Clock)
			s.OpenProgress.Plans = []wiaworld.PersonalPlan{{ID: "npc:a:check", OwnerID: "npc:a", Content: "核对船期", Status: "active", NextCheck: minute, LastCheck: minute - 1, Version: 2, SourceIDs: []string{"npc:a:heard"}}}
			run := wiaworld.Run{RunID: "plan-review", Input: "我问进度。", BaseContextEpoch: 1}
			b := sceneDialogue("b1", "npc:a", "还需要核对。", "input:0")
			if explicit {
				b.Effects.PlanUpdates = []scenePlanUpdate{{OwnerID: "npc:a", ID: "npc:a:check", Content: "先核对新通知", Status: "active", ReviewAfterMinutes: 60, Basis: []string{"input:0"}}}
			}
			d := sceneComplete(run.Input, b)
			d.ProgressUpdates = []sceneProgress{{Type: "personal_plan", ID: "npc:a:check", Status: "occurred", Content: "已复查本人计划。", Basis: []string{"input:0"}, BeatIDs: []string{"b1"}}}
			out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
			if err != nil {
				t.Fatal(err)
			}
			p := out.OpenProgress.Plans[0]
			delay := 30
			if explicit {
				delay = 60
			}
			if p.Version != 3 || p.LastCheck != minute || p.NextCheck != minute+delay || s.OpenProgress.Plans[0].Version != 2 {
				t.Fatalf("plan review double-applied: %+v", p)
			}
		})
	}
}

func TestSceneLegacyTerminalAndGeneratedContinuation(t *testing.T) {
	s := sceneContextFixture()
	minute, _ := plot.ClockMinute(s.Summary.Clock)
	s.Summary.Mode = "guided"
	s.Plot = &plot.Definition{Revision: "legacy.v1", Facts: "渡轮靠岸。", Nodes: []plot.Node{{ID: "arrival", AtMinute: minute, Audience: []string{"player"}, Terminal: true}}}
	s.PlotProgress = plot.Progress{Version: 2, Nodes: map[string]plot.NodeState{}}
	run := wiaworld.Run{RunID: "legacy", Input: "我等待渡轮。"}
	id := "definition:legacy.v1:arrival"
	b := sceneWorldBeat("b1", "渡轮靠岸。", id, 0)
	d := sceneComplete(run.Input, b)
	d.ProgressUpdates = []sceneProgress{{Type: "legacy_node", ID: "arrival", Status: "occurred", Content: "抵达渡轮。", Basis: []string{id, "beat:b1"}, BeatIDs: []string{"b1"}, Ending: "登船离开"}}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil || out.PlotProgress.Ending != "登船离开" || out.PlotProgress.Version != 3 || s.PlotProgress.Ending != "" {
		t.Fatal("frozen terminal semantics lost", err)
	}
	s.Summary.Mode = "open"
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
		t.Fatal("open world given a guided ending")
	}
	s.Plot = nil
	s.Definition.EventGeneration = &plot.EventGenerationPolicy{MaxActive: 2, Locations: []string{"workplace"}}
	s.GeneratedEvents.Active = []GeneratedEvent{{Node: plot.Node{ID: "pending", AtMinute: minute, Audience: []string{"player"}}, StartID: "start", TriggerID: "trigger", Premise: "渡轮正在靠岸。", Location: "workplace"}}
	id = "definition:generated.v1:pending"
	b = sceneWorldBeat("b1", "渡轮已经靠岸。", id, 0)
	d = sceneComplete(run.Input, b)
	d.ProgressUpdates = []sceneProgress{{Type: "generated_event", ID: "pending", Status: "occurred", Content: "事件收束。", Basis: []string{id, "beat:b1"}, BeatIDs: []string{"b1"}}}
	out, err = sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil || len(out.GeneratedEvents.Active) != 0 || out.GeneratedEvents.Completed != 1 || len(s.GeneratedEvents.Active) != 1 {
		t.Fatal("generated event continuation lost", err)
	}
}

func TestSceneArrivalEventOfferKeepsTriggerAndScope(t *testing.T) {
	s := sceneContextFixture()
	s.Definition.Locations = []story.Location{{ID: "workplace", Kind: "place", Connections: []string{"dock"}}, {ID: "dock", Kind: "place"}}
	s.Definition.EventGeneration = &plot.EventGenerationPolicy{MaxActive: 1, CooldownTurns: 2, Locations: []string{"dock"}, Participants: []string{"npc:a"}}
	run := wiaworld.Run{RunID: "offer", InputID: "offer-input", Input: "我去码头查看。"}
	move := scenePlayerAction("b1", "我抵达码头。")
	move.OffsetMinutes = 5
	move.Effects.Movements = []sceneMovement{{EntityID: "player", From: "workplace", To: "dock", Route: []string{"workplace", "dock"}}}
	quiet, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, move))
	if err != nil || quiet.GeneratedEvents == nil || len(quiet.GeneratedEvents.Active) != 0 || quiet.GeneratedEvents.LastOfferTurn != s.GeneratedEvents.LastOfferTurn {
		t.Fatal("arrival without an offer invented a pending event or cooldown", err)
	}
	b := sceneWorldBeat("b2", "渡轮晚点的通知刚刚张贴。", "beat:b1", 5)
	d := sceneComplete(run.Input, move, b)
	d.EventOffer = &sceneEventOffer{TriggerBeatID: "b1", Kind: "arrival", Location: "dock", Condition: "下一班渡轮到来。", Development: "可以询问工作人员，也可以继续等候。", AfterMinutes: 10, InitialBeatIDs: []string{"b2"}}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil || len(out.GeneratedEvents.Active) != 1 {
		t.Fatal("qualified offer failed", err)
	}
	e := out.GeneratedEvents.Active[0]
	if e.StartID != sceneWorldEventID(run, 1) || e.TriggerID != run.RunID+":scene:1:action:result:1" || !slices.Equal(e.Node.Audience, []string{"player", "npc:a"}) {
		t.Fatalf("offer provenance lost: %+v", e)
	}
	s.GeneratedEvents.LastOfferTurn, s.Summary.TurnSeq = 1, 1
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
		t.Fatal("event offer cooldown bypassed")
	}
	s.GeneratedEvents.LastOfferTurn, s.Summary.TurnSeq = 0, 0
	next := sceneWorldBeat("b3", "值班员开始整理延误说明。", "beat:b1", 5)
	d = sceneComplete(run.Input, move, b, next)
	d.EventOffer = &sceneEventOffer{TriggerBeatID: "b1", Kind: "arrival", Location: "dock", Condition: "下一班渡轮到来。", Development: "继续观察。", AfterMinutes: 10, InitialBeatIDs: []string{"b3", "b2"}}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
		t.Fatal("reversed event start order accepted")
	}
	d.EventOffer.InitialBeatIDs = []string{"b2", "b3"}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err != nil {
		t.Fatal("chronological event start rejected", err)
	}
	last := sceneObservation("b4", "player", "我在码头继续等待。", "input:0")
	last.OffsetMinutes = 16
	d.Beats = append(d.Beats, last)
	d.ElapsedMinutes = 16
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
		t.Fatal("new pending event boundary silently crossed")
	}
	d = sceneComplete(run.Input, move, b)
	d.EventOffer = &sceneEventOffer{TriggerBeatID: "b1", Kind: "arrival", Location: "dock", Condition: "下一班渡轮到来。", Development: "继续观察。", AfterMinutes: 10, InitialBeatIDs: []string{"b2"}}
	s.GeneratedEvents.Active = slices.Clone(out.GeneratedEvents.Active)
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
		t.Fatal("active event capacity bypassed")
	}
}

func TestSceneWorldStimulusGrantsOnlyDeliveredPersonalProjection(t *testing.T) {
	s := sceneOpenFixture()
	s.Positions["npc:c"] = "dock"
	s.Characters[2].InScene = false
	s.Definition.Materials = []story.Material{{ID: "notice", Visibility: "author", Delivery: "core", Body: "通知只送到码头值班人员。"}}
	minute, _ := plot.ClockMinute(s.Summary.Clock)
	s.Definition.Progression.ExternalSchedules = []plot.ExternalSchedule{{ID: "notice", AtMinute: minute, MaterialID: "notice"}}
	run := wiaworld.Run{RunID: "stimulus", Input: "我留在办事处观察。"}
	b := sceneWorldBeat("b1", "码头收到船期通知。", s.Definition.Materials[0].SourceID(s.Definition.Revision), 0)
	b.Recipients = []string{"npc:c"}
	b.Projections = outcomeProjectionFixture("我收到船期通知。", []string{"npc:c"})
	a := sceneObservation("b2", "player", "办事处暂时平静。", "input:0")
	c := sceneDialogue("b3", "npc:c", "我先把新船期登记好。", "beat:b1")
	d := sceneComplete(run.Input, b, a, c)
	d.NarrativeBlocks[0].BeatIDs = []string{"b2"}
	d.ProgressUpdates = []sceneProgress{{Type: "external_schedule", ID: "notice", Status: "occurred", Content: "已通知码头。", Basis: []string{"beat:b1"}, BeatIDs: []string{"b1"}}}
	_, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d)
	var expansion *sceneActorExpansion
	if !errors.As(err, &expansion) || expansion.EntityID != "npc:c" {
		t.Fatal("stimulated offscene owner did not require identity context", err)
	}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range out.Perceptions {
		if p.RecipientID == "player" && strings.Contains(p.Content, "我收到船期通知") {
			t.Fatal("offscene private result reached player")
		}
		if p.RecipientID == "npc:c" && p.SourceEventID == sceneWorldEventID(run, 0)+":projection:npc:c" && p.SourceType != "plot_observed" {
			t.Fatal("world projection kind lost")
		}
	}
	d.Beats[2].Basis = []string{"input:0"}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d); err == nil {
		t.Fatal("world stimulus granted unrelated player input")
	}
}

func TestSceneActuallyDueOwnersExpandTogetherWithinOneSupplement(t *testing.T) {
	s := sceneOpenFixture()
	minute, _ := plot.ClockMinute(s.Summary.Clock)
	for index, owner := range []string{"npc:b", "npc:c"} {
		s.Characters[index+1].InScene = false
		s.Positions[owner] = "dock"
		s.OpenProgress.Plans = append(s.OpenProgress.Plans, wiaworld.PersonalPlan{ID: owner + ":check", OwnerID: owner, Content: "复查船期", Status: "active", NextCheck: minute + 10 + index, LastCheck: minute - 1, Version: 1, SourceIDs: []string{"opening"}})
	}
	run := wiaworld.Run{RunID: "due-batch", Input: "我在这里等待二十分钟。"}
	g := &sceneSequenceGenerator{reply: func(index int, req model.TextRequest) (string, error) {
		b := sceneObservation("b1", "player", "办事处仍然平静。", "input:0")
		b.OffsetMinutes = 20
		d := sceneComplete(run.Input, b)
		if index == 2 {
			for _, owner := range []string{"npc:b", "npc:c"} {
				if !strings.Contains(req.Input, "本人当前计划 owner="+owner) {
					t.Fatal("actually due owner missing from batch context", owner)
				}
				d.ProgressUpdates = append(d.ProgressUpdates, sceneProgress{Type: "personal_plan", ID: owner + ":check", Status: "occurred", Content: "已复查当前计划，仍需继续核对。", OffsetMinutes: 20, Basis: []string{scenePersonalID(owner, "opening")}, BeatIDs: []string{}})
			}
		}
		return wire.MarshalJSON(d), nil
	}}
	out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, run)
	if err != nil || report.CoreCalls != 2 || report.ContextSupplements != 1 || report.Repairs != 0 {
		t.Fatal("actual due owners were eagerly loaded or separately retried", report, err)
	}
	for _, plan := range out.OpenProgress.Plans {
		if plan.LastCheck != minute+20 || plan.NextCheck != minute+50 || plan.Version != 2 {
			t.Fatal("due fallback did not use actual review time", plan)
		}
	}
}

func TestSceneUnobservedWorldOccurrenceHasNoInventedPerception(t *testing.T) {
	s := sceneOpenFixture()
	minute, _ := plot.ClockMinute(s.Summary.Clock)
	s.Definition.Materials = []story.Material{{ID: "notice", Visibility: "author", Delivery: "core", Body: "码头贴出了通知，尚无人读取。"}}
	s.Definition.Progression.ExternalSchedules = []plot.ExternalSchedule{{ID: "notice", AtMinute: minute, MaterialID: "notice"}}
	run := wiaworld.Run{RunID: "unobserved", Input: "我在办事处休息。"}
	a := sceneObservation("b1", "player", "办事处没有新动静。", "input:0")
	b := sceneWorldBeat("b2", "码头无人注意的通知已经张贴。", s.Definition.Materials[0].SourceID(s.Definition.Revision), 0)
	b.Recipients, b.Projections = []string{}, []actionProjection{}
	d := sceneComplete(run.Input, a, b)
	d.NarrativeBlocks[0].BeatIDs = []string{"b1"}
	d.ProgressUpdates = []sceneProgress{{Type: "external_schedule", ID: "notice", Status: "occurred", Content: "通知已张贴，尚无已知接收者。", Basis: []string{"beat:b2"}, BeatIDs: []string{"b2"}}}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil || out.OpenProgress.ExternalApplied["notice"] == "" {
		t.Fatal("unobserved occurrence required a fake listener", err)
	}
	for _, p := range out.Perceptions {
		if strings.Contains(p.Content, "码头无人注意") {
			t.Fatal("unobserved occurrence broadcast to a person")
		}
	}
	d.NarrativeBlocks[0].BeatIDs = []string{"b2"}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
		t.Fatal("unobserved occurrence supported player prose")
	}
}
