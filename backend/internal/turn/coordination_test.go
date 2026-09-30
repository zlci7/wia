package turn

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type coordinationTestHost struct{}

func (coordinationTestHost) LogStage(string, wiaworld.Run, Stage, string, string, int, string, []string, string, int, time.Duration) {
}

type coordinationResultGenerator struct{ result hostResult }

func (g coordinationResultGenerator) GenerateText(context.Context, model.TextRequest) (model.TextResponse, error) {
	return model.TextResponse{Text: wire.MarshalJSON(g.result)}, nil
}

type recallProbe struct {
	requests []model.TextRequest
	always   bool
}

func (g *recallProbe) GenerateText(_ context.Context, request model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, request)
	if len(g.requests) == 1 || g.always {
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":"","recall_query":"铜钥匙"}`}, nil
	}
	return model.TextResponse{Text: `{"speech":"我记得这件事。","action_intent":"","silent":false,"memory":"重新想起约定。"}`}, nil
}

func TestPlotPresenceRejectsUnresolvedMovement(t *testing.T) {
	present := false
	events := []wiaworld.Event{{EventID: "a", ActorID: "npc:a", EventType: "npc_action_intent"}}
	for _, status := range []string{"failed", "not_executed"} {
		outcomes := []plotActionResult{{hostActionResult: hostActionResult{ActionID: "a", Status: status}, ActorInScene: &present}}
		if _, err := plotActionPresence([]string{"npc:a"}, events, outcomes); err == nil {
			t.Fatal("unresolved movement accepted")
		}
	}
}

func TestHostOutcomesRequireEveryActionAndValidRecipients(t *testing.T) {
	run := wiaworld.Run{RunID: "r"}
	actor := wiaworld.Character{EntityID: "npc:actor"}
	out := Output{Events: []wiaworld.Event{{EventID: "action", ActorID: actor.EntityID, EventType: "npc_action_intent", RunID: "r", Stage: 1}}}
	if _, err := appendHostOutcomes(&out, run, []wiaworld.Character{actor}, nil, nil); err == nil {
		t.Fatal("missing outcome accepted")
	}
	invalid := []hostActionResult{{ActionID: "action", Status: "not_executed", Content: "重复", Recipients: []string{"foreign"}}}
	if _, err := appendHostOutcomes(&out, run, []wiaworld.Character{actor}, nil, invalid); err == nil {
		t.Fatal("unknown recipient accepted")
	}
}

func TestSceneUpdatesEnforceEverySourceRecipient(t *testing.T) {
	snapshot := Snapshot{
		Summary:      wiaworld.WorldSummary{WorldID: "fixture"},
		SceneVersion: 1,
		Characters:   []wiaworld.Character{{EntityID: "npc:a"}, {EntityID: "npc:b"}},
		SceneViews: []SceneView{
			{Recipient: "player", Content: "大厅", SourceIDs: []string{"opening"}, Version: 1},
			{Recipient: "npc:a", Content: "柜台", SourceIDs: []string{"opening"}, Version: 1},
			{Recipient: "npc:b", Content: "门边", SourceIDs: []string{"opening"}, Version: 1},
		},
	}
	run := wiaworld.Run{RunID: "run"}
	intent := TurnIntent{Visibility: "private", AddresseeID: "npc:a"}
	output := Output{Events: []wiaworld.Event{{EventID: "run:input", RunID: "run", Stage: 1, EventType: "player_attempt", ActorID: "player", Content: "私密信件位置"}}}
	for _, test := range []struct {
		name            string
		ids, recipients []string
		valid           bool
	}{
		{"player", []string{"run:input"}, []string{"player"}, true},
		{"recipient", []string{"run:input"}, []string{"npc:a"}, true},
		{"observer", []string{"run:input"}, []string{"npc:b"}, false},
		{"other view", []string{"view:npc:a"}, []string{"npc:b"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := hostResult{SceneUpdates: []sceneUpdate{{Content: "私密信件位置", SourceIDs: test.ids, Recipients: test.recipients}}}
			_, err := applySceneUpdates(snapshot, run, intent, output, host)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t err=%v", test.valid, err)
			}
		})
	}
}

func TestAuthoredProgressConsumesGeneratedEventRound(t *testing.T) {
	node := plot.Node{ID: "authored", AtMinute: 5, Audience: []string{"player"}}
	snapshot := Snapshot{
		Summary:         wiaworld.WorldSummary{Clock: "第 1 日 00:00"},
		Plot:            &plot.Definition{Revision: "r", Nodes: []plot.Node{node}},
		PlotProgress:    plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{"authored": {Status: "deferred", NextCheck: 5}}},
		GeneratedEvents: GeneratedEventState{Active: []GeneratedEvent{}},
		Definition:      story.Definition{EventGeneration: &plot.EventGenerationPolicy{MaxActive: 1, CooldownTurns: 2, Locations: []string{"workshop"}}},
	}
	out := Output{Clock: "第 1 日 00:05", PlotProgress: &plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{"authored": {Status: "deferred", NextCheck: 5}}}}
	service := New(coordinationTestHost{}, Deps{})
	visible, err := service.advanceGeneratedEvents(context.Background(), nil, snapshot, wiaworld.Run{RunID: "r"}, &eventOpportunity{Kind: "arrival", Location: "workshop", ActionID: "not-evaluated"}, &out)
	if err != nil || len(visible) != 0 || out.GeneratedEvents.LastOfferTurn != 0 {
		t.Fatalf("authored round leaked into generated events: visible=%v state=%+v err=%v", visible, out.GeneratedEvents, err)
	}
}

func TestGeneratedEventOpportunityRejectsInvalidScope(t *testing.T) {
	snapshot := Snapshot{Summary: wiaworld.WorldSummary{Clock: "第 1 日 00:00", TurnSeq: 10}, Definition: story.Definition{EventGeneration: &plot.EventGenerationPolicy{MaxActive: 1, CooldownTurns: 2, Locations: []string{"workshop"}}}}
	service := New(coordinationTestHost{}, Deps{})
	for _, opportunity := range []eventOpportunity{{Kind: "arrival", Location: "outside", ActionID: "x"}, {Kind: "refresh", Location: "workshop", ActionID: "x"}} {
		out := Output{Clock: snapshot.Summary.Clock}
		if _, err := service.advanceGeneratedEvents(context.Background(), nil, snapshot, wiaworld.Run{RunID: "r"}, &opportunity, &out); err == nil {
			t.Fatal("invalid opportunity accepted")
		}
	}
}

func TestWaitingRequiresAvailableInterruptionEvidence(t *testing.T) {
	snapshot := Snapshot{Summary: wiaworld.WorldSummary{Clock: "第 1 日 19:00"}, Plot: &plot.Definition{}, PlotProgress: plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{}}}
	snapshot.Events = []wiaworld.Event{{EventID: "committed-danger", RunID: "previous", Stage: 4, EventType: "plot_result"}}
	events := []wiaworld.Event{{EventID: "current-danger", RunID: "run", Stage: 1, EventType: "npc_dialogue"}}
	for _, test := range []struct {
		name    string
		minutes int
		ids     []string
		valid   bool
	}{
		{"boundary", 60, nil, true},
		{"shortened", 1, nil, false},
		{"wrong source", 1, []string{"another-run"}, false},
		{"current", 1, []string{"current-danger"}, true},
		{"committed", 1, []string{"committed-danger"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := hostResult{TimeMinutes: test.minutes, Scene: "客栈", SceneCharacters: []string{}, Outcomes: []hostActionResult{}, SceneUpdates: []sceneUpdate{}, InterruptSources: test.ids}
			service := New(coordinationTestHost{}, Deps{})
			_, _, err := service.coordinateTurn(context.Background(), coordinationResultGenerator{result}, snapshot, wiaworld.Run{RunID: "run"}, TurnIntent{IntentType: "act", WaitMinutes: 60}, Output{Events: events})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t err=%v", test.valid, err)
			}
		})
	}
}

func TestRequestedWaitIsAnUpperBound(t *testing.T) {
	snapshot := Snapshot{Summary: wiaworld.WorldSummary{Clock: "第 1 日 19:00"}, PlotProgress: plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{}}}
	for _, minutes := range []int{5, 30} {
		result := hostResult{TimeMinutes: minutes, Scene: "原地", SceneCharacters: []string{}, Outcomes: []hostActionResult{}, SceneUpdates: []sceneUpdate{}}
		service := New(coordinationTestHost{}, Deps{})
		_, _, err := service.coordinateTurn(context.Background(), coordinationResultGenerator{result}, snapshot, wiaworld.Run{RunID: "wait"}, TurnIntent{IntentType: "act", WaitMinutes: 5}, Output{})
		if (err == nil) != (minutes == 5) {
			t.Fatalf("minutes=%d err=%v", minutes, err)
		}
	}
}

func TestPlotSceneSourcesPreserveAudience(t *testing.T) {
	output := Output{SceneVersion: 1, SceneViews: []SceneView{{Recipient: "player", Content: "客栈", Version: 1}, {Recipient: "npc:a", Content: "柜台", Version: 1}}, Perceptions: []wiaworld.Perception{{RecipientID: "npc:a", SourceEventID: "private", Content: "私人结果", Stage: 4}, {RecipientID: "player", SourceEventID: "public", Content: "铃声", Stage: 4}}}
	sources := plotSceneSources(output, nil)
	for _, id := range []string{"private", "view:npc:a", "future"} {
		candidate := output
		err := applyPlotSceneUpdates(&candidate, sources, []sceneUpdate{{Content: "不应成立", SourceIDs: []string{id}, Recipients: []string{"player"}}})
		if err == nil || candidate.SceneVersion != 1 {
			t.Fatalf("unauthorized view source=%s", id)
		}
	}
	if err := applyPlotSceneUpdates(&output, sources, []sceneUpdate{{Content: "客栈传来铃声", SourceIDs: []string{"public"}, Recipients: []string{"player"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestPlotResolutionReferencesAndAudience(t *testing.T) {
	node := plot.Node{ID: "node", Audience: []string{"player", "npc:a"}}
	snapshot := Snapshot{Plot: &plot.Definition{Revision: "r", Nodes: []plot.Node{node}}, Characters: []wiaworld.Character{{EntityID: "npc:a"}}, PlotProgress: plot.Progress{Nodes: map[string]plot.NodeState{}}}
	base := plotResolution{Status: "occurred", Content: "结果", SourceIDs: []string{"definition:r:node"}, Projections: []plotProjection{}, DecisionRequests: []string{}}
	if err := validatePlotResolution(snapshot, node, Output{}, base); err != nil {
		t.Fatal(err)
	}
	bad := base
	bad.SourceIDs = []string{"other-world:event"}
	if err := validatePlotResolution(snapshot, node, Output{}, bad); !errors.Is(err, ErrContextSourceMissing) {
		t.Fatal(err)
	}
	bad = base
	bad.Projections = []plotProjection{{Recipient: "unknown", Content: "秘密"}}
	if err := validatePlotResolution(snapshot, node, Output{}, bad); err == nil {
		t.Fatal("expanded audience")
	}
}

func TestPlotReferencesRetainedViewsOutsideEventWindow(t *testing.T) {
	nodes := []plot.Node{{ID: "node", Audience: []string{"player"}}}
	snapshot := Snapshot{Plot: &plot.Definition{Revision: "r", Nodes: nodes}, Sources: map[string]SourceMetadata{"old-result": {ID: "old-result"}, "old-plot": {ID: "old-plot"}}, PlotProgress: plot.Progress{Nodes: map[string]plot.NodeState{"prior": {Status: "occurred", EventID: "old-plot"}}}}
	out := Output{SceneViews: []SceneView{{Recipient: "player", Content: "信使已安全上船", SourceIDs: []string{"old-result"}, Version: 1}}}
	result := plotResolution{Status: "occurred", Content: "机会结束", SourceIDs: []string{"old-result", "old-plot"}, Projections: []plotProjection{}, DecisionRequests: []string{}}
	if err := validatePlotResolution(snapshot, nodes[0], out, result); err != nil {
		t.Fatal("retained sourced material rejected", err)
	}
	delete(snapshot.Sources, "old-result")
	result.SourceIDs = []string{"old-result"}
	if !errors.Is(validatePlotResolution(snapshot, nodes[0], out, result), ErrContextSourceMissing) {
		t.Fatal("view ID grants nonexistent source")
	}
}

func TestInitialSceneViewsKeepRecipientLocations(t *testing.T) {
	snapshot := Snapshot{SceneVersion: 1, Definition: story.Definition{Scene: "大厅", Locations: []story.Location{{ID: "counter", Name: "柜台", Description: "一盏灯"}}, InitialLocations: map[string]string{"npc:a": "counter"}}, Characters: []wiaworld.Character{{EntityID: "npc:a"}}}
	views := initialSceneViews(snapshot)
	if len(views) != 2 || views[0].Recipient != "player" || views[1].Recipient != "npc:a" || views[1].Content != "柜台：一盏灯" {
		t.Fatalf("unexpected initial views: %+v", views)
	}
}

func TestSceneRosterTreatsPlayerAsImplicit(t *testing.T) {
	characters := []wiaworld.Character{{EntityID: "npc:a"}, {EntityID: "npc:b"}}
	ids := NormalizeSceneCharacters([]string{"player", "npc:a", "npc:b"})
	if err := validateSceneCharacters(ids, characters); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "npc:a" || ids[1] != "npc:b" {
		t.Fatalf("normalized scene roster = %v", ids)
	}
}

func TestSceneSourcesRejectForeignRunAndFutureStage(t *testing.T) {
	events := []wiaworld.Event{
		{EventID: "valid", RunID: "current", Stage: 1, EventType: "player_attempt"},
		{EventID: "foreign", RunID: "foreign", Stage: 1, EventType: "player_attempt"},
		{EventID: "future", RunID: "current", Stage: 9, EventType: "npc_dialogue"},
	}
	sources := sceneSources(Snapshot{}, wiaworld.Run{RunID: "current"}, TurnIntent{Visibility: "public"}, events)
	found := false
	for _, source := range sources {
		if source.ID == "foreign" || source.ID == "future" {
			t.Fatal("invalid stage/run source admitted")
		}
		found = found || source.ID == "valid"
	}
	if !found {
		t.Fatal("current source missing")
	}
}

func TestAutonomousSilentActionHasIndependentChannel(t *testing.T) {
	var output Output
	actor := wiaworld.Character{EntityID: "npc:a", Name: "甲"}
	appendNPCDecisionOutput(&output, wiaworld.Run{RunID: "autonomy"}, actor, NPCDecision{Silent: true, ActionIntent: "检查门闩", Memory: "我准备检查门闩"}, []wiaworld.Character{actor}, "autonomy:input", 1, 1)
	actions := 0
	for _, event := range output.Events {
		if event.EventType == "npc_dialogue" {
			t.Fatal("silent action invented speech")
		}
		if event.EventType == "npc_action_intent" {
			actions++
		}
	}
	if actions != 1 {
		t.Fatalf("actions=%d", actions)
	}
}

func TestNPCRecallRoundTripAndStageFiveMemory(t *testing.T) {
	character := wiaworld.Character{EntityID: "npc:a", Name: "甲", Role: "掌柜", InScene: true}
	snapshot := Snapshot{
		Summary:    wiaworld.WorldSummary{WorldID: "w"},
		Characters: []wiaworld.Character{character},
		LongMemory: map[string]MemoryContext{
			"npc:a": {Archive: []memory.MemorySource{{ID: "personal-old", Seq: 1, Content: "铜钥匙须在柜台归还"}}},
			"npc:b": {Archive: []memory.MemorySource{{ID: "other-secret", Seq: 1, Content: "他人的铜钥匙秘密"}}},
		},
		Perceptions: map[string][]wiaworld.Perception{"npc:a": {{SourceEventID: "current-done", Content: "此前已完成添茶，不是新提案。", SourceType: "action_result"}}},
		Sources:     map[string]SourceMetadata{"current-done": {ID: "current-done", Actor: "npc:a", Kind: "npc_action_result"}},
	}
	definition := story.Definition{Characters: []wiaworld.Character{character}}
	input := map[string]StageInput{"npc:a": {NewStimulus: "新的铃声"}}
	service := New(coordinationTestHost{}, Deps{})
	generator := &recallProbe{}
	if err := service.decideNPCs(context.Background(), generator, snapshot, definition, wiaworld.Run{RunID: "probe"}, "npc:a", "speak", input, nil, map[string]NPCDecision{}, 5); err != nil {
		t.Fatal(err)
	}
	if len(generator.requests) != 2 || strings.Contains(generator.requests[0].Input, "柜台归还") || !strings.Contains(generator.requests[1].Input, "柜台归还") || strings.Contains(generator.requests[1].Input, "他人的铜钥匙秘密") {
		t.Fatal("retrieval scope or execution incorrect")
	}
	if !strings.Contains(generator.requests[1].Input, "此前已完成添茶") {
		t.Fatal("stage five lost current results")
	}
	generator = &recallProbe{always: true}
	if err := service.decideNPCs(context.Background(), generator, snapshot, definition, wiaworld.Run{RunID: "bounded"}, "npc:a", "speak", input, nil, map[string]NPCDecision{}, 5); err == nil || len(generator.requests) != 3 {
		t.Fatal("unbounded recall", err, len(generator.requests))
	}
}
