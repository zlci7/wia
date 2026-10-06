package turn

import (
	"slices"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestSceneMixedInputKeepsFailedAndIndependentLaterSteps(t *testing.T) {
	for _, status := range []string{"succeeded", "failed", "partial"} {
		t.Run(status, func(t *testing.T) {
			s := sceneContextFixture()
			parts := []string{"先问清登记。", "再检查账簿。", "最后记下发现。"}
			run := wiaworld.Run{RunID: "mixed-" + status, Input: strings.Join(parts, "")}
			a := sceneDialogue("b1", "npc:a", "登记在柜台上。", "input:0")
			b := scenePlayerAction("b2", "检查账簿的结果已确定。")
			b.Basis, b.Status, b.Attempt.InputFragmentIndex = []string{"input:1", "beat:b1"}, sceneString(status), sceneIndex(1)
			b.OffsetMinutes = 2
			c := scenePlayerAction("b3", "将实际发现记入记事本。")
			c.Basis, c.Attempt.InputFragmentIndex, c.OffsetMinutes = []string{"input:2", "beat:b2"}, sceneIndex(2), 3
			d := sceneComplete(run.Input, a, b, c)
			d.InputMap = []sceneInput{{Text: parts[0], IntentType: "speak", Visibility: "public", BeatIDs: []string{"b1"}, Status: "succeeded"}, {Text: parts[1], IntentType: "act", Visibility: "public", BeatIDs: []string{"b2"}, Status: status}, {Text: parts[2], IntentType: "act", Visibility: "public", BeatIDs: []string{"b3"}, Status: "succeeded"}}
			out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b"}, d)
			if err != nil {
				t.Fatal(err)
			}
			first, ok := EventByID(out.Events, run.RunID+":scene:2:action:result:1")
			if !ok || first.SourceType != "action_"+status {
				t.Fatal("earlier action outcome lost", first)
			}
			last, ok := EventByID(out.Events, run.RunID+":scene:3:action")
			if !ok || len(last.BasisEventIDs) != 2 || !slices.Contains(last.BasisEventIDs, first.EventID+":projection:player") {
				t.Fatal("independent later step did not use its actual preceding experience", last)
			}
			if out.Clock != "2189-12-31 23:58" {
				t.Fatal("mixed steps advanced time more than once", out.Clock)
			}
			for _, e := range out.Events {
				if e.ActorID == "npc:b" && (e.EventType == "npc_speech" || e.EventType == "npc_action_intent") {
					t.Fatal("silent listener was forced to act")
				}
			}
		})
	}
}

func TestSceneUnreachableStepStaysBeforeIndependentLaterInput(t *testing.T) {
	s := sceneContextFixture()
	s.Definition.Locations = []story.Location{{ID: "workplace", Kind: "place", Connections: []string{"dock"}}, {ID: "dock", Kind: "place"}}
	parts := []string{"先问清登记。", "再去码头。", "然后私下问甲是否愿意帮忙。", "最后看看码头。"}
	run := wiaworld.Run{RunID: "unreachable-later", Input: strings.Join(parts, "")}
	a := sceneDialogue("b1", "npc:a", "登记在柜台上。", "input:0")
	b := scenePlayerAction("b2", "抵达码头。")
	b.Basis, b.Attempt.InputFragmentIndex, b.OffsetMinutes = []string{"input:1"}, sceneIndex(1), 5
	b.Effects.Movements = []sceneMovement{{EntityID: "player", From: "workplace", To: "dock", Route: []string{"workplace", "dock"}}}
	c := sceneObservation("b3", "player", "码头尚没有新动静。", "input:3")
	c.OffsetMinutes = 5
	d := sceneComplete(run.Input, a, b, c)
	d.InputMap = []sceneInput{{Text: parts[0], IntentType: "speak", Visibility: "public", BeatIDs: []string{"b1"}, Status: "succeeded"}, {Text: parts[1], IntentType: "act", Visibility: "public", BeatIDs: []string{"b2"}, Status: "succeeded"}, {Text: parts[2], IntentType: "speak", Visibility: "private", AddresseeID: "npc:a", BeatIDs: []string{}, Status: "not_executed", UnexecutedReason: "甲仍在办事处，无法私下交谈。"}, {Text: parts[3], IntentType: "observe", Visibility: "public", BeatIDs: []string{"b3"}, Status: "succeeded"}}
	d.NarrativeBlocks[0].Text = "你到了码头；甲仍在办事处，无法继续私聊。你转而查看眼前的情况。"
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil {
		t.Fatal(err)
	}
	first, later := -1, -1
	for index, e := range out.Events {
		if e.EventID == run.RunID+":part:3:input" {
			first = index
		}
		if e.EventID == run.RunID+":part:4:input" {
			later = index
		}
	}
	if first < 0 || later <= first || out.Positions["player"] != "dock" {
		t.Fatal("unexecuted step was recorded after later input", first, later)
	}
	result, ok := EventByID(out.Events, run.RunID+":part:3:player-action:result:1")
	if !ok || result.SourceType != "action_not_executed" || result.Content != d.InputMap[2].UnexecutedReason {
		t.Fatal("unexecuted reason did not enter durable results", result)
	}
	for _, p := range out.Perceptions {
		if p.RecipientID == "npc:a" && (strings.Contains(p.Content, parts[2]) || strings.Contains(p.Content, parts[3])) {
			t.Fatal("departed player kept communicating with the old room")
		}
	}
	d.InputMap[0].BeatIDs, d.InputMap[1].BeatIDs = []string{"b2"}, []string{"b1"}
	if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
		t.Fatal("later input introduced before the earlier fragment")
	}
}

func TestSceneUnexecutedInputHasPersonalResultWithoutCommunication(t *testing.T) {
	s := sceneContextFixture()
	run := wiaworld.Run{RunID: "blocked-input", Input: "我私下告诉甲封套中的秘密。"}
	b := sceneObservation("b1", "player", "我没有开口。", scenePersonalID("player", "same-record"))
	d := sceneComplete(run.Input, b)
	d.InputMap[0] = sceneInput{Text: run.Input, IntentType: "speak", Visibility: "private", AddresseeID: "npc:a", Status: "not_executed", BeatIDs: []string{}, UnexecutedReason: "交谈被打断，我没有说出这句话。"}
	out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range out.Perceptions {
		if p.RecipientID != "player" && strings.Contains(p.Content, "封套中的秘密") {
			t.Fatal("unexecuted speech reached an NPC", p)
		}
	}
	result, ok := EventByID(out.Events, run.RunID+":part:1:player-action:result:1")
	if !ok || result.SourceType != "action_not_executed" || result.Content != d.InputMap[0].UnexecutedReason {
		t.Fatal("unexecuted input lacks a traceable result", result)
	}
	for _, status := range []string{"succeeded", "failed", "partial"} {
		d.InputMap[0].Status = status
		if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, d); err == nil {
			t.Fatal("executed input without associated outcome accepted", status)
		}
	}
}

func TestScenePlanPauseCompletionAndCancellationKeepCurrentSources(t *testing.T) {
	for _, status := range []string{"paused", "completed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			s := sceneOpenFixture()
			minute, _ := plot.ClockMinute(s.Summary.Clock)
			s.OpenProgress.Plans = []wiaworld.PersonalPlan{{ID: "npc:a:current", OwnerID: "npc:a", Content: "复查登记", SourceIDs: []string{"opening"}, NextCheck: minute + 60, LastCheck: -1, Status: "active", Version: 3}}
			run := wiaworld.Run{RunID: "plan-state", Input: "我告诉甲本次登记已结束。", BaseContextEpoch: 1}
			b := sceneDialogue("b1", "npc:a", "我会按当前结果处理这项计划。", "input:0")
			b.Effects.PlanUpdates = []scenePlanUpdate{{OwnerID: "npc:a", ID: "npc:a:current", Status: status, Content: "本次登记计划已" + status, Basis: []string{"input:0"}}}
			out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a"}, sceneComplete(run.Input, b))
			if err != nil {
				t.Fatal(err)
			}
			p := out.OpenProgress.Plans[0]
			if p.Status != status || p.Version != 4 || p.NextCheck != minute || p.LastCheck != minute || slices.Contains(p.SourceIDs, "opening") || s.OpenProgress.Plans[0].Status != "active" {
				t.Fatal("current plan or update source was reset", p)
			}
			if owners := plot.DuePlanOwners(out.OpenProgress.Plans, minute+120, 2); len(owners) != 0 {
				t.Fatal("inactive plan remained due", owners)
			}
		})
	}
}

func TestSceneMandatoryMemoryCapacityFailurePrecedesProvider(t *testing.T) {
	s := sceneContextFixture()
	s.LongMemory["player"] = MemoryContext{Tail: []memory.MemorySource{{Scope: "player", ID: "large", EventID: "opening", RunID: "large", Seq: 1, Content: strings.Repeat("完整重要经历", 20000)}}}
	g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) {
		t.Fatal("mandatory capacity failure reached provider")
		return "", nil
	}}
	out, _, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, wiaworld.Run{Input: "继续。"})
	if err == nil || len(out.Events) > 0 || len(g.requests) > 0 || len(s.LongMemory["player"].Tail[0].Content) == 0 {
		t.Fatal("capacity failure discarded input or returned partial world", err)
	}
}

func TestSceneUnknownEntitiesFailWithinOneCorrection(t *testing.T) {
	for _, kind := range []string{"actor", "target", "item", "state"} {
		t.Run(kind, func(t *testing.T) {
			s := sceneContextFixture()
			run := wiaworld.Run{RunID: "unknown-" + kind, Input: "我查看柜台上的物品。"}
			b := scenePlayerAction("b1", "查看柜台。")
			switch kind {
			case "actor":
				b.ActorID, b.Attempt = "npc:invented", &sceneAttempt{Content: "查看柜台"}
			case "target":
				b.TargetID = "npc:invented"
			case "item":
				b.Effects.ItemTransfers = []sceneItemTransfer{{InstanceID: "invented", FromLocationID: "workplace", ToHolderID: "player"}}
			case "state":
				b.Effects.StateEffects = []sceneStateEffect{{EntityID: "player", StateID: "invented", Delta: 1}}
			}
			g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) {
				return wire.MarshalJSON(sceneComplete(run.Input, b)), nil
			}}
			before := wire.MarshalJSON(s)
			out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, s, run)
			if err == nil || report.CoreCalls != 2 || report.Repairs != 1 || len(out.Events) != 0 || wire.MarshalJSON(s) != before {
				t.Fatalf("invented %s escaped the bounded rejection: %+v %v", kind, report, err)
			}
		})
	}
}

func TestScenePreparedRuleKeepsSuccessFailureAndRetryIdentity(t *testing.T) {
	for _, status := range []string{"succeeded", "failed"} {
		t.Run(status, func(t *testing.T) {
			s, run, _, _, store := sceneCheckpointFixture(t)
			s.Positions["player"], s.SceneLocation = "room", "room"
			s.SceneViews[0].Content = "房间内"
			run.Input = "尝试风险行动。"
			roll, strain := 1, 5
			if status == "failed" {
				roll, strain = 99, 12
			}
			modifiers := wire.MarshalJSON([]AppliedModifier{{Label: "低负荷", Amount: 10}})
			if _, _, err := store.PrepareActionResolution(t.Context(), storage.ActionResolutionRecord{InputID: run.InputID, RuleID: "risk", Roll: roll, Target: 60, ModifiersJSON: modifiers, CreatedAt: wire.NowText()}); err != nil {
				t.Fatal(err)
			}
			for attempt := range 2 {
				run.RunID = "prepared-" + status + "-" + string(rune('1'+attempt))
				b := scenePlayerAction("b1", "风险行动已经结算。")
				b.Status, b.OffsetMinutes = sceneString(status), 1
				d := sceneComplete(run.Input, b)
				d.InputMap[0].Status, d.InputMap[0].ActionRuleID = status, "risk"
				core := 0
				g := &sceneSequenceGenerator{reply: func(_ int, req model.TextRequest) (string, error) {
					if strings.Contains(req.System, "结构化回合意图") {
						return wire.MarshalJSON(TurnIntent{IntentType: "act", Visibility: "public", ActionRuleID: "risk"}), nil
					}
					core++
					if core == 1 {
						return `{"schema_revision":`, nil
					}
					return wire.MarshalJSON(d), nil
				}}
				out, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), store, g, s, run)
				if err != nil || report.CoreCalls != 2 || report.Repairs != 1 || out.ActionResolution == nil || !out.ActionResolution.Reused || out.ActionResolution.Roll != roll || out.States["player"]["strain"].Value.Integer != strain || len(out.StateChanges) != 1 {
					t.Fatalf("prepared result changed during correction or retry: %+v %v", report, err)
				}
				if attempt == 1 && s.States["player"]["strain"].Value.Integer != 3 {
					t.Fatal("candidate retry mutated the input snapshot")
				}
			}
		})
	}
}

func TestSceneActionsAndObservationsKeepPersonalSourcePermissions(t *testing.T) {
	for _, kind := range []string{"action_result", "observation"} {
		for _, source := range []string{"private-beat", "foreign-record", "private-input"} {
			t.Run(kind+"/"+source, func(t *testing.T) {
				s := sceneContextFixture()
				run := wiaworld.Run{RunID: "personal-action", Input: "我看看他们在忙什么。"}
				a := sceneDialogue("b1", "npc:a", "私下封套约定。", "input:0")
				a.Scope, a.Recipients = sceneString("private"), []string{"npc:b"}
				seen := sceneObservation("b2", "player", "他们低声交谈，我听不清内容。", "input:0")
				c := sceneObservation("b3", "npc:c", "我根据消息查看柜台。", "beat:b1")
				if kind == "action_result" {
					c.Kind, c.Status, c.Attempt = kind, sceneString("succeeded"), &sceneAttempt{Content: "查看柜台"}
				}
				d := sceneComplete(run.Input, a, seen, c)
				d.NarrativeBlocks[0].BeatIDs = []string{"b2"}
				switch source {
				case "foreign-record":
					c.Basis = []string{scenePersonalID("npc:a", "same-record")}
				case "private-input":
					d.InputMap[0].Visibility, d.InputMap[0].AddresseeID = "private", "npc:a"
					c.Basis = []string{"input:0"}
				}
				d.Beats[2] = c
				if out, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d); err == nil || len(out.Events) > 0 {
					t.Fatal("authored action granted a foreign personal source", err)
				}
				if source == "private-beat" {
					d.Beats[2].ActorID, d.Beats[2].Recipients, d.Beats[2].Projections[0].Recipient = "npc:b", []string{"npc:b"}, "npc:b"
					if _, err := sceneCompileFixture(t, s, run, []string{"player", "npc:a", "npc:b", "npc:c"}, d); err != nil {
						t.Fatal("actual private recipient could not use its own projection", err)
					}
				}
			})
		}
	}
}
