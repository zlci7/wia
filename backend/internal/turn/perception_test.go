package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestCoordinationProjectionContractAndRepairIdentifyMissingRecipients(t *testing.T) {
	snapshot := spatialFixture()
	material := composeCoordination(snapshot, wiaworld.Run{RunID: "contract", Input: "检查门口。"}, TurnIntent{}, nil, nil, nil)
	fields := strings.SplitN(material.Required, "完整字段类型：", 2)
	if len(fields) != 2 || !strings.Contains(strings.SplitN(fields[1], "scene_updates", 2)[0], "projections 对象数组") {
		t.Fatal("the complete outcome field contract omitted personal projections")
	}
	outcome := hostActionResult{ActionID: "contract:action", Projections: []actionProjection{{Recipient: "player", Content: "看见门口。"}}}
	_, err := actionProjectionText(outcome, map[string]bool{"player": true, "npc:b": true, "npc:a": true})
	var detail *GenerationError
	if !errors.As(err, &detail) || detail.Code != "action_projection_missing" || detail.Expected != "action_id=contract:action; missing-recipient-ids=npc:a,npc:b" {
		t.Fatalf("repair did not identify the exact action and missing observers: %v", err)
	}
}

const privateInput = "我悄悄告诉记者：暗号是银色渡鸦。"
const leaveInput = "随后走到街上，再进入诊所。"
const greetingInput = "我向看守问好。"

func TestMixedIntentPreservesThePlayersExplicitOpeningAddressee(t *testing.T) {
	snapshot := spatialFixture()
	snapshot.Positions["npc:watcher"] = "office"
	snapshot.Characters[1].InScene = true
	first, later := "我低声说暗号是银色渡鸦。", "再向看守询问昨夜的情况。"
	run := wiaworld.Run{RunID: "explicit-mixed", Input: first + later, AddresseeID: "npc:reporter"}
	wanted := TurnIntent{IntentType: "speak", AddresseeID: "npc:watcher", Visibility: "private", Fragments: []InputFragment{
		{Text: first, ActorID: "player", IntentType: "speak", AddresseeID: "npc:watcher", Visibility: "private"},
		{Text: later, ActorID: "player", IntentType: "speak", AddresseeID: "npc:watcher", Visibility: "public"},
	}}
	g := &materialTestGenerator{responses: []string{wire.MarshalJSON(wanted)}}
	intent, repairs, err := New(&rosterHost{}, Deps{}).resolveTurnIntent(context.Background(), g, snapshot, run)
	if err != nil || repairs != 0 {
		t.Fatalf("explicit mixed target: repairs=%d err=%v", repairs, err)
	}
	if intent.AddresseeID != "npc:reporter" || intent.Fragments[0].AddresseeID != "npc:reporter" || intent.Fragments[1].AddresseeID != "npc:watcher" {
		t.Fatal("the model displaced the player's explicit opening target or later target")
	}
	if intent.Visibility != "private" || intent.Fragments[1].Visibility != "public" {
		t.Fatal("binding the opening target changed the conversation scopes")
	}
}

func TestMixedIntentRetainsItsLaterPreparedRule(t *testing.T) {
	snapshot := spatialFixture()
	rule := ruleTestSnapshot().Definition.ActionRules[0]
	rule.Conditions[0].LocationID = "clinic"
	snapshot.Definition.ActionRules = []story.ActionRule{rule}
	last := "然后尝试潜入诊所内室。"
	run := wiaworld.Run{RunID: "retry-mixed", Input: privateInput + leaveInput + last, PreparedActionRuleID: "risk"}
	wanted := TurnIntent{IntentType: "act", AddresseeID: "npc:reporter", Visibility: "public", ActionRuleID: "risk", Fragments: []InputFragment{
		{Text: privateInput, ActorID: "player", IntentType: "speak", AddresseeID: "npc:reporter", Visibility: "private"},
		{Text: leaveInput, ActorID: "player", IntentType: "act", Visibility: "public"},
		{Text: last, ActorID: "player", IntentType: "act", Visibility: "public", ActionRuleID: "risk"},
	}}
	response := wire.MarshalJSON(wanted)
	g := &materialTestGenerator{responses: []string{response, response}}
	intent, repairs, err := New(&rosterHost{}, Deps{}).resolveTurnIntent(context.Background(), g, snapshot, run)
	if err != nil || repairs != 0 {
		t.Fatalf("retry evaluated a later rule against the opening position: repairs=%d err=%v", repairs, err)
	}
	if intent.AddresseeID != "npc:reporter" || intent.Visibility != "private" || intent.ActionRuleID != "" || intent.Fragments[2].ActionRuleID != "risk" {
		t.Fatal("aggregate metadata displaced a later segment's prepared rule")
	}
	if len(g.requests) != 1 || !strings.Contains(g.requests[0].Input, `"ID":"risk"`) {
		t.Fatal("the saved rule was absent from the retry's input")
	}
}

type orderedPerceptionGenerator struct {
	mu                sync.Mutex
	npcInputs         map[string][]string
	coordinationCalls int
	narrationCalls    int
	failArrival       bool
}

func (g *orderedPerceptionGenerator) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(request.System, "结构化回合意图") {
		intent := TurnIntent{IntentType: "act", Visibility: "public", Fragments: []InputFragment{
			{Text: privateInput, ActorID: "player", IntentType: "speak", AddresseeID: "npc:reporter", Visibility: "private"},
			{Text: leaveInput, ActorID: "player", IntentType: "act", Visibility: "public"},
			{Text: greetingInput, ActorID: "player", IntentType: "speak", AddresseeID: "npc:watcher", Visibility: "public"},
		}}
		return model.TextResponse{Text: wire.MarshalJSON(intent)}, nil
	}
	if strings.Contains(request.System, "重要 NPC") {
		id := "npc:observer"
		if strings.Contains(request.Input, "你的身份：记者") {
			id = "npc:reporter"
		}
		if strings.Contains(request.Input, "你的身份：看守") {
			id = "npc:watcher"
		}
		g.mu.Lock()
		g.npcInputs[id] = append(g.npcInputs[id], request.Input)
		g.mu.Unlock()
		decision := NPCDecision{Silent: true}
		if id == "npc:reporter" && strings.Contains(request.Input, "本轮玩家输入中你实际获知的部分：\n"+privateInput) {
			decision = NPCDecision{Speech: "原件藏在后室。", SpeechVisibility: "private", SpeechRecipients: []string{"player"}}
		}
		return model.TextResponse{Text: wire.MarshalJSON(decision)}, nil
	}
	if strings.Contains(request.System, "场景协调 Agent") {
		match := regexp.MustCompile(`待裁定行动\(JSON\)：(\[[^\n]*\])`).FindStringSubmatch(request.Input)
		if len(match) != 2 {
			return model.TextResponse{}, fmt.Errorf("pending actions missing")
		}
		var actions []wiaworld.Event
		if err := json.Unmarshal([]byte(match[1]), &actions); err != nil {
			return model.TextResponse{}, err
		}
		g.mu.Lock()
		g.coordinationCalls++
		g.mu.Unlock()
		host := hostResult{Scene: "office", SceneCharacters: []string{"npc:reporter", "npc:observer"}, SceneUpdates: []sceneUpdate{}, Movements: []movementResult{}, Outcomes: []hostActionResult{}}
		for _, action := range actions {
			ids := []string{"player", "npc:reporter"}
			content := "耳语已经传达。"
			if action.Content == leaveInput {
				host.TimeMinutes, host.Scene, host.SceneCharacters = 12, "clinic", []string{"npc:watcher"}
				ids = []string{"player", "npc:reporter", "npc:observer", "npc:watcher"}
				content = "玩家从事务所沿街道抵达诊所。"
				host.Movements = append(host.Movements, movementResult{EntityID: "player", From: "office", To: "clinic", Route: []string{"office", "street", "clinic"}, ActionID: action.EventID})
				host.SceneUpdates = append(host.SceneUpdates, sceneUpdate{Content: "诊所入口，看守在场。", SourceIDs: []string{action.EventID}, Recipients: []string{"player"}})
				if g.failArrival {
					return model.TextResponse{}, fmt.Errorf("arrival model unavailable")
				}
			} else if action.Content == greetingInput {
				host.Scene, host.SceneCharacters = "clinic", []string{"npc:watcher"}
				ids = []string{"player", "npc:watcher"}
				content = "玩家向看守问好。"
			}
			projections := []actionProjection{}
			for _, id := range ids {
				text := content
				if id == "npc:watcher" && action.Content == leaveInput {
					text = "我看见玩家进入诊所。"
				}
				projections = append(projections, actionProjection{Recipient: id, Content: text})
			}
			host.Outcomes = append(host.Outcomes, hostActionResult{ActionID: action.EventID, Status: "succeeded", Content: content, Recipients: ids, Projections: projections})
		}
		var body map[string]any
		_ = json.Unmarshal([]byte(wire.MarshalJSON(host)), &body)
		body["movements"] = host.Movements
		return model.TextResponse{Text: wire.MarshalJSON(body)}, nil
	}
	g.mu.Lock()
	g.narrationCalls++
	g.mu.Unlock()
	return model.TextResponse{Text: "你和记者私下交谈，随后沿街抵达诊所，与看守打了招呼。"}, nil
}

func TestOrderedInputKeepsPrivateSpeechBeforeMovementAndArrival(t *testing.T) {
	snapshot := spatialFixture()
	snapshot.Summary.Clock = "第 1 日 09:00"
	snapshot.Characters[0].Name = "记者"
	snapshot.Characters[1].Name = "看守"
	snapshot.Characters = append(snapshot.Characters, wiaworld.Character{EntityID: "npc:observer", Name: "旁观者", InScene: true})
	snapshot.Positions["npc:observer"] = "office"
	snapshot.SceneViews = []SceneView{{Recipient: "player", Content: "office"}, {Recipient: "npc:reporter", Content: "office"}, {Recipient: "npc:observer", Content: "office"}, {Recipient: "npc:watcher", Content: "clinic"}}
	snapshot.LongMemory = map[string]MemoryContext{"npc:reporter": {}, "npc:observer": {}, "npc:watcher": {}}
	g := &orderedPerceptionGenerator{npcInputs: map[string][]string{}}
	run := wiaworld.Run{RunID: "ordered", Input: privateInput + leaveInput + greetingInput}
	output, err := New(&rosterHost{}, Deps{}).executeSnapshot(context.Background(), g, snapshot, run)
	if err != nil {
		t.Fatal(err)
	}
	if output.Clock != "第 1 日 09:12" || output.Positions["player"] != "clinic" || g.coordinationCalls != 3 || g.narrationCalls != 1 {
		t.Fatalf("ordered result: clock=%s location=%s calls=%d/%d", output.Clock, output.Positions["player"], g.coordinationCalls, g.narrationCalls)
	}
	for _, id := range []string{"npc:observer", "npc:watcher"} {
		for _, input := range g.npcInputs[id] {
			if strings.Contains(input, "银色渡鸦") || strings.Contains(input, "原件藏在后室") {
				t.Fatalf("private content leaked into %s", id)
			}
		}
	}
	if len(g.npcInputs["npc:watcher"]) != 1 || !strings.Contains(g.npcInputs["npc:watcher"][0], greetingInput) {
		t.Fatal("arrival did not grant only the later fragment")
	}
	rootFound, replyFound := false, false
	for _, event := range output.Events {
		if event.EventID == "ordered:input" {
			rootFound = event.Content == run.Input
		}
		if event.EventType == "npc_dialogue" && event.SourceType == "speech_private" {
			if event.TargetID != "player" {
				t.Fatal("private reply broadened")
			}
			replyFound = true
		}
	}
	if !rootFound || !replyFound {
		t.Fatal("complete input or private reply lost")
	}
	for _, memory := range output.Memories {
		if memory.RecipientID == "npc:watcher" && strings.Contains(memory.Content, "银色渡鸦") {
			t.Fatal("arrival inherited earlier whisper")
		}
	}
	for _, event := range output.VisibleEvents {
		if event.EventType == "npc_speech" || event.EventType == "npc_action_result" || event.EventType == "player_action_result" {
			t.Fatal("author root was narrated")
		}
	}
	first, _, _ := EventByIDStage(output.Events, "ordered:part:1:player-action")
	second, _, _ := EventByIDStage(output.Events, "ordered:part:2:player-action")
	third, _, _ := EventByIDStage(output.Events, "ordered:part:3:player-action")
	if !(first < second && second < third) {
		t.Fatal("stored input stages lost order")
	}
	if !perceivedSource(Snapshot{Events: output.Events, Perceptions: map[string][]wiaworld.Perception{"player": output.Perceptions}}, "player", "ordered:part:2:player-action:result:1") {
		t.Fatal("projection lost structured fact visibility")
	}
}

func EventByIDStage(events []wiaworld.Event, id string) (int, wiaworld.Event, bool) {
	e, ok := EventByID(events, id)
	return e.Stage, e, ok
}

func TestInputFragmentValidationPreservesOriginalTextAndBounds(t *testing.T) {
	snapshot := spatialFixture()
	run := wiaworld.Run{Input: "甲。乙。"}
	base := TurnIntent{Fragments: []InputFragment{{Text: "甲。", ActorID: "player", IntentType: "speak", Visibility: "public"}, {Text: "乙。", ActorID: "player", IntentType: "act", Visibility: "public"}}}
	if err := validateInputFragments(snapshot, run, &base); err != nil || base.Fragments[1].Start != 2 || base.Fragments[1].End != 4 {
		t.Fatalf("rune spans: %+v %v", base, err)
	}
	for _, text := range []string{"乙。甲。", "甲。漏字乙。"} {
		if err := validateInputFragments(snapshot, wiaworld.Run{Input: text}, &base); err == nil {
			t.Fatal("reordered or missing text accepted")
		}
	}
	bad := base
	bad.Fragments = append([]InputFragment{}, base.Fragments...)
	bad.Fragments[0].ActorID = "npc:reporter"
	if validateInputFragments(snapshot, run, &bad) == nil {
		t.Fatal("model supplied another actor")
	}
}

func TestPrivateNPCSpeechValidatesContactAndUsesOnlyListeners(t *testing.T) {
	snapshot := spatialFixture()
	decision := NPCDecision{Speech: "密语", SpeechVisibility: "private", SpeechRecipients: []string{"npc:watcher"}}
	if validateNPCSpeech(snapshot, "npc:reporter", &decision) == nil {
		t.Fatal("off-scene private reply accepted")
	}
	decision.SpeechRecipients = []string{"player"}
	if err := validateNPCSpeech(snapshot, "npc:reporter", &decision); err != nil {
		t.Fatal(err)
	}
	output := Output{}
	appendNPCSpeech(&output, wiaworld.Run{RunID: "r"}, snapshot.Characters[0], decision, snapshot.Characters, 1, 1)
	if len(PublicReplyStageInputs(output.Perceptions, map[string]string{}, 1)) != 1 {
		t.Fatal("private listener followup missing")
	}
	for _, p := range output.Perceptions {
		if p.RecipientID == "npc:watcher" {
			t.Fatal("unlisted observer heard private speech")
		}
	}
}

func TestActionProjectionsSeparateAuthorAndIndividualObservation(t *testing.T) {
	output := Output{Events: []wiaworld.Event{{EventID: "act", EventType: "npc_action_intent", ActorID: "npc:a"}}}
	outcome := hostActionResult{ActionID: "act", Status: "succeeded", Content: "AUTHOR_SECRET：抽屉里其实藏着契约。", Recipients: []string{"player", "npc:a"}, Bystanders: []string{"bystander:x"}, Projections: []actionProjection{{Recipient: "player", Content: "我看见抽屉被关上。"}, {Recipient: "npc:a", Content: "我把契约收回抽屉。"}, {Recipient: "bystander:x", Content: "那人摆弄了桌子。"}}}
	visible, err := appendHostOutcomes(&output, wiaworld.Run{RunID: "r"}, []wiaworld.Character{{EntityID: "npc:a"}}, []story.Bystander{{BystanderID: "bystander:x"}}, []hostActionResult{outcome})
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || strings.Contains(visible[0].Content, "AUTHOR_SECRET") {
		t.Fatal("author result entered player projection")
	}
	for _, p := range output.Perceptions {
		if strings.Contains(p.Content, "AUTHOR_SECRET") || p.SourceEventID == "act:result:1" {
			t.Fatal("personal observation reused root")
		}
	}
	for _, bad := range []actionProjection{{Recipient: "npc:foreign", Content: "泄漏"}, {Recipient: "player", Content: "重复"}} {
		broken := outcome
		broken.Projections = append(append([]actionProjection{}, outcome.Projections...), bad)
		if _, err := actionProjectionText(broken, map[string]bool{"player": true, "npc:a": true, "bystander:x": true}); err == nil {
			t.Fatal("extra projection accepted")
		}
	}
}

func outcomeProjectionFixture(content string, recipients []string) []actionProjection {
	result := []actionProjection{}
	seen := map[string]bool{}
	for _, id := range recipients {
		if !seen[id] {
			result = append(result, actionProjection{Recipient: id, Content: content})
			seen[id] = true
		}
	}
	return result
}
