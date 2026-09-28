package storyapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"gameagent/runtime/internal/model"
)

type generatedTestGenerator struct {
	mu            sync.Mutex
	requests      []model.TextRequest
	failNarration bool
	noCandidate   bool
	elapsed       int
}

type backgroundTalkGenerator struct{ actionConsistencyGenerator }

func (g backgroundTalkGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(req.System, "结构化回合意图") {
		return model.TextResponse{Text: `{"intent_type":"speak","addressee_id":"","visibility":"public"}`}, nil
	}
	return g.actionConsistencyGenerator.GenerateText(ctx, req)
}

func TestBackgroundConversationHasResolvedInteraction(t *testing.T) {
	a := newTestApp(t, backgroundTalkGenerator{actionConsistencyGenerator{status: "succeeded"}})
	withoutWorldEvents(a)
	w := createPackWorld(t, a, "orbital-repair")
	r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: "bystander", Input: "向登记工单的值班员询问公开值班表。"})
	if err != nil {
		t.Fatal(err)
	}
	if r = waitRun(t, a, w.WorldID, r.RunID); r.Status != "completed" {
		t.Fatal(r)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	found := false
	for _, e := range s.Events {
		if e.RunID == r.RunID && e.EventType == "player_action_result" {
			found = true
		}
	}
	if !found {
		t.Fatal("background conversation has no place to record its outcome")
	}
}

func withoutWorldEvents(a *App) {
	p := a.packs["orbital-repair"]
	p.Definition.Plot, p.Definition.EventGeneration = nil, nil
	a.packs["orbital-repair"] = p
}

func (g *generatedTestGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	fail, noCandidate, elapsed := g.failNarration, g.noCandidate, g.elapsed
	g.mu.Unlock()
	if strings.Contains(req.System, "开放世界事件协调器") {
		if noCandidate {
			return model.TextResponse{Text: `{"candidates":[]}`}, nil
		}
		var trigger Event
		raw := strings.SplitN(strings.SplitN(req.Input, "已确认触发结果：", 2)[1], "\n", 2)[0]
		if err := json.Unmarshal([]byte(raw), &trigger); err != nil {
			return model.TextResponse{}, err
		}
		c := eventCandidate{Condition: "五分钟后核对窗口结束", Development: "有核对则登记结果，没有则暂存待查；不替玩家接受任务。", AfterMinutes: 5,
			Initial: plotResolution{Status: "occurred", Content: "一个配送标签待核对，编号为蓝三。", SourceIDs: []string{trigger.EventID}, Projections: []plotProjection{{Recipient: "player", Content: "值班员请你核对一张配送标签。", Scene: "你在设备检修间，看见待核对的标签。"}}, DecisionRequests: []string{}}}
		return model.TextResponse{Text: marshalJSON(map[string]any{"candidates": []eventCandidate{c}})}, nil
	}
	if strings.Contains(req.System, "世界剧情协调器") {
		return (&plotTestGenerator{}).GenerateText(ctx, req)
	}
	if strings.Contains(req.System, "场景协调 Agent") {
		response, err := (actionConsistencyGenerator{status: "succeeded"}).GenerateText(ctx, req)
		if err != nil {
			return response, err
		}
		var host hostResult
		if err := json.Unmarshal([]byte(response.Text), &host); err != nil {
			return model.TextResponse{}, err
		}
		host.TimeMinutes = 1
		if elapsed > 0 {
			host.TimeMinutes = elapsed
		}
		if len(host.Outcomes) > 0 {
			host.EventOpportunity = &eventOpportunity{Kind: "arrival", Location: "workshop", ActionID: host.Outcomes[0].ActionID}
			host.Outcomes[0].Content = "你抵达设备检修间。"
			host.SceneUpdates = []sceneUpdate{{Content: "你在设备检修间。", SourceIDs: []string{host.Outcomes[0].ActionID}, Recipients: []string{"player"}}}
		}
		return model.TextResponse{Text: marshalJSON(host)}, nil
	}
	if strings.Contains(req.System, "玩家正文 Agent") && fail {
		return model.TextResponse{}, errors.New("injected narration failure")
	}
	return (actionConsistencyGenerator{status: "succeeded"}).GenerateText(ctx, req)
}

func eventTestWorld(t *testing.T, g *generatedTestGenerator) (*App, WorldSummary) {
	t.Helper()
	a := newTestApp(t, g)
	p := a.packs["orbital-repair"]
	p.Definition.Plot = nil
	a.packs["orbital-repair"] = p
	return a, createPackWorld(t, a, "orbital-repair")
}

func generatedTurn(t *testing.T, a *App, w WorldSummary, key string) Run {
	t.Helper()
	r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: key, Input: "走到检修间观察"})
	if err != nil {
		t.Fatal(err)
	}
	return waitRun(t, a, w.WorldID, r.RunID)
}

func TestGeneratedEventsAtomicIdentityPacingAndScope(t *testing.T) {
	g := &generatedTestGenerator{failNarration: true}
	a, w := eventTestWorld(t, g)
	before := readContextSnapshot(t, a, w.WorldID)
	failed := generatedTurn(t, a, w, "fail")
	if failed.Status != "failed" {
		t.Fatal(failed)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	if len(after.GeneratedEvents.Active) != 0 || after.GeneratedEvents.LastOfferTurn != 0 || after.Summary.EventHead != before.Summary.EventHead {
		t.Fatal("partial generated event committed")
	}
	g.mu.Lock()
	g.failNarration = false
	g.mu.Unlock()
	r, err := a.RetryRun(context.Background(), w.WorldID, failed.RunID, "same-input-retry")
	if err != nil {
		t.Fatal(err)
	}
	r = waitRun(t, a, w.WorldID, r.RunID)
	if r.Status != "completed" {
		t.Fatal(r)
	}
	replay, err := a.RetryRun(context.Background(), w.WorldID, failed.RunID, "same-input-retry")
	if err != nil || replay.RunID != r.RunID || r.InputID != failed.InputID {
		t.Fatal("duplicate operation")
	}
	s := readContextSnapshot(t, a, w.WorldID)
	if len(s.GeneratedEvents.Active) != 1 || s.GeneratedEvents.LastOfferTurn != 1 {
		t.Fatal(s.GeneratedEvents)
	}
	root := s.GeneratedEvents.Active[0].StartID
	if root != "generated:"+r.InputID {
		t.Fatal(root)
	}
	for _, scope := range []string{"npc:innkeeper", "npc:mercenary"} {
		for _, p := range s.Perceptions[scope] {
			if strings.Contains(p.Content, "蓝三") || strings.Contains(p.Content, "配送标签") {
				t.Fatal("private event projection broadcast", scope)
			}
		}
	}
	for i := 0; i < 2; i++ {
		if r = generatedTurn(t, a, w, fmt.Sprint(i)); r.Status != "completed" {
			t.Fatal(r)
		}
	}
	s = readContextSnapshot(t, a, w.WorldID)
	if len(s.GeneratedEvents.Active) != 1 || s.GeneratedEvents.LastOfferTurn != 1 {
		t.Fatal("cooldown did not hold", s.GeneratedEvents)
	}
	if r = generatedTurn(t, a, w, "second"); r.Status != "completed" {
		t.Fatal(r)
	}
	s = readContextSnapshot(t, a, w.WorldID)
	if len(s.GeneratedEvents.Active) != 2 {
		t.Fatal("second independent event missing", s.GeneratedEvents)
	}
	for i := 0; i < 2; i++ {
		if r = generatedTurn(t, a, w, fmt.Sprint("settle", i)); r.Status != "completed" {
			t.Fatal(r)
		}
	}
	s = readContextSnapshot(t, a, w.WorldID)
	if s.GeneratedEvents.Completed != 1 || len(s.GeneratedEvents.Active) != 1 || s.Summary.StoryEnded {
		t.Fatal("open event settlement", s.GeneratedEvents)
	}
}

func TestGeneratedEventsEmptyChoiceAndInvalidScope(t *testing.T) {
	g := &generatedTestGenerator{noCandidate: true}
	a, w := eventTestWorld(t, g)
	if r := generatedTurn(t, a, w, "none"); r.Status != "completed" {
		t.Fatal(r)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	if len(s.GeneratedEvents.Active) != 0 || s.GeneratedEvents.LastOfferTurn != 1 {
		t.Fatal(s.GeneratedEvents)
	}
	for _, change := range []func(*EventGenerationPolicy){func(p *EventGenerationPolicy) { p.Locations = []string{"outside"} }, func(p *EventGenerationPolicy) { p.Participants = []string{"npc:invented"} }, func(p *EventGenerationPolicy) { p.MaxActive = 4 }} {
		p := *s.Definition.EventGeneration
		change(&p)
		if validateEventPolicy(&p, s.Definition) == nil {
			t.Fatal("invalid author boundary admitted")
		}
	}
	for _, opportunity := range []eventOpportunity{{Kind: "arrival", Location: "workshop", ActionID: "nonexistent"}, {Kind: "arrival", Location: "outside", ActionID: "x"}, {Kind: "refresh", Location: "workshop", ActionID: "x"}} {
		s.Summary.TurnSeq = 10
		out := turnOutput{Clock: s.Summary.Clock}
		if _, err := a.advanceGeneratedEvents(context.Background(), g, s, Run{RunID: "r"}, &opportunity, &out); err == nil {
			t.Fatal("invalid opportunity accepted")
		}
	}
}

func TestGeneratedEventSettlementFailureIsAtomic(t *testing.T) {
	g := &generatedTestGenerator{}
	a, w := eventTestWorld(t, g)
	if r := generatedTurn(t, a, w, "begin"); r.Status != "completed" {
		t.Fatal(r)
	}
	before := readContextSnapshot(t, a, w.WorldID)
	g.mu.Lock()
	g.elapsed, g.failNarration = 5, true
	g.mu.Unlock()
	failed := generatedTurn(t, a, w, "finish-failed")
	after := readContextSnapshot(t, a, w.WorldID)
	if failed.Status != "failed" || marshalJSON(before.GeneratedEvents) != marshalJSON(after.GeneratedEvents) || before.Summary.Clock != after.Summary.Clock || before.Summary.EventHead != after.Summary.EventHead {
		t.Fatal("failed settlement changed committed story", failed.Status)
	}
	g.mu.Lock()
	g.failNarration = false
	g.mu.Unlock()
	r, err := a.RetryRun(context.Background(), w.WorldID, failed.RunID, "finish-retry")
	if err != nil {
		t.Fatal(err)
	}
	if r = waitRun(t, a, w.WorldID, r.RunID); r.Status != "completed" {
		t.Fatal(r)
	}
	after = readContextSnapshot(t, a, w.WorldID)
	if len(after.GeneratedEvents.Active) != 0 || after.GeneratedEvents.Completed != 1 || after.Summary.StoryEnded {
		t.Fatal(after.GeneratedEvents)
	}
}

func TestOpenAuthoredLinesAreIndependent(t *testing.T) {
	a := newTestApp(t, &packGenerator{})
	w := createPackWorld(t, a, "orbital-repair")
	s := readContextSnapshot(t, a, w.WorldID)
	s.PlotProgress.Nodes["inspection_notice"] = PlotNodeState{Status: "deferred", NextCheck: 560}
	n, due, ok := nextPlotNode(s)
	if !ok || n.ID != "freight_notice" || due != 550 {
		t.Fatal("unrelated freight starved", n, due)
	}
	s.PlotProgress.Nodes["freight_notice"] = PlotNodeState{Status: "occurred", EventID: "notice"}
	s.PlotProgress.Nodes["inspection_notice"] = PlotNodeState{Status: "deferred", NextCheck: 600}
	n, _, _ = nextPlotNode(s)
	if n.ID != "freight_result" {
		t.Fatal("freight depends on inspection", n)
	}
}

func TestDeferredAuthoredEventConsumesWorldRound(t *testing.T) {
	g := &generatedTestGenerator{}
	a := newTestApp(t, g)
	w := createPackWorld(t, a, "orbital-repair")
	s := readContextSnapshot(t, a, w.WorldID)
	out := turnOutput{Clock: "第 1 日 09:05", PlotProgress: &PlotProgress{Version: 1, Nodes: map[string]PlotNodeState{"inspection_notice": {Status: "deferred", NextCheck: 560}}}}
	_, err := a.advanceGeneratedEvents(context.Background(), g, s, Run{RunID: "deferred"}, &eventOpportunity{Kind: "arrival", Location: "workshop", ActionID: "not-evaluated"}, &out)
	if err != nil || len(g.requests) != 0 || out.GeneratedEvents.LastOfferTurn != 0 {
		t.Fatal("authored deferral started a second world-event round", err)
	}
}

func TestGeneratedEventsCopyRestartCorrectionAndIsolation(t *testing.T) {
	ctx := context.Background()
	g := &generatedTestGenerator{}
	a, w := eventTestWorld(t, g)
	if r := generatedTurn(t, a, w, "start"); r.Status != "completed" {
		t.Fatal(r)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	root := s.GeneratedEvents.Active[0].StartID
	op, err := a.SaveAs(ctx, w.WorldID, "事件分支", "event-copy", 1)
	if err != nil {
		t.Fatal(err)
	}
	copy := readContextSnapshot(t, a, op.TargetWorldID)
	if marshalJSON(copy.GeneratedEvents) != marshalJSON(s.GeneratedEvents) {
		t.Fatal("copy omitted event state")
	}
	for i := 0; i < 3; i++ {
		if _, err = a.ReadWorld(ctx, w.WorldID, 100); err != nil {
			t.Fatal(err)
		}
	}
	g.mu.Lock()
	calls := len(g.requests)
	g.mu.Unlock()
	if calls == 0 {
		t.Fatal("no captured requests")
	}
	_, err = a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "event-correction", ExpectedEpoch: s.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: root, Replacement: "这次观察没有发现待核对标签。"})
	if err != nil {
		t.Fatal(err)
	}
	if job := waitMemory(t, a, w.WorldID); job.Status != "completed" {
		t.Fatal(job)
	}
	corrected := readContextSnapshot(t, a, w.WorldID)
	if len(corrected.GeneratedEvents.Active) != 0 {
		t.Fatal("invalidated future still active")
	}
	copy = readContextSnapshot(t, a, op.TargetWorldID)
	if len(copy.GeneratedEvents.Active) != 1 {
		t.Fatal("correction crossed worlds")
	}
	rootDir := a.DataRoot()
	a.Close()
	reopened, err := Open(ctx, Options{DataRoot: rootDir, Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	copy = readContextSnapshot(t, reopened, op.TargetWorldID)
	if len(copy.GeneratedEvents.Active) != 1 || copy.GeneratedEvents.Active[0].StartID != root {
		t.Fatal("restart lost event identity")
	}
	corrected = readContextSnapshot(t, reopened, w.WorldID)
	if len(corrected.GeneratedEvents.Active) != 0 {
		t.Fatal("restart resurrected invalidated event")
	}
	g.mu.Lock()
	afterCalls := len(g.requests)
	g.mu.Unlock()
	if afterCalls != calls {
		t.Fatal("read, copy or restart generated new story")
	}
}

func TestM2RealOpenEvents(t *testing.T) {
	a := closeoutRealApp(t)
	w := createPackWorld(t, a, "orbital-repair")
	inputs := []string{"我去设备检修间，看看公开的工单与周围环境，不操作设备。", "我在安全位置等待半小时，不参加维修，也不替任何人签字。", "我继续安静等待半小时，不接受新任务。", "我继续在安全位置等半小时，不操作设备。", "我继续在安全位置等半小时，不操作设备。", "我走回值班室看看有什么新情况。", "我再去检修间，向登记工单的值班员问好。"}
	for i, input := range inputs {
		s := realPackTurn(t, a, w.WorldID, input, i)
		if s.Summary.StoryEnded {
			t.Fatal("open world ended")
		}
		t.Logf("author_nodes=%d generated_active=%d generated_completed=%d last_offer_turn=%d", len(s.PlotProgress.Nodes), len(s.GeneratedEvents.Active), s.GeneratedEvents.Completed, s.GeneratedEvents.LastOfferTurn)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	if len(s.PlotProgress.Nodes) != 4 {
		t.Fatal("author event lines incomplete")
	}
	if s.GeneratedEvents.LastOfferTurn == 0 {
		t.Fatal("no generation opportunity evaluated")
	}
}
