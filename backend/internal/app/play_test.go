package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
)

type playProbe struct {
	positions      map[string]string
	requests       chan model.TextRequest
	coreGate       chan struct{}
	suggestionGate chan struct{}
	invalid        atomic.Bool
	calls          atomic.Int32
}

func (*playProbe) SupportsTextStreaming() bool { return true }
func (g *playProbe) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	g.calls.Add(1)
	if g.requests != nil {
		g.requests <- request
	}
	var text string
	gate := g.coreGate
	if strings.Contains(request.System, "玩家行动建议助手") {
		gate = g.suggestionGate
		text = `{"items":["我问来访者最后一次见面发生了什么。","我仔细核对桌上的照片，寻找能够确认的细节。","我请书记员核对记录，听听她对这次来访的看法。"]}`
	} else {
		data, _ := json.Marshal(map[string]any{"scene_changes": map[string]any{"elapsed_minutes": 2, "positions": g.positions}, "narrative": "你听见来访者低声回答。\n书记员留在桌旁。"})
		text = string(data)
		if g.invalid.Load() {
			text = `{"narrative":"不合格候选正文","scene_changes":{"positions":{"player":"unknown-place"}}}`
		}
	}
	if request.OnDelta != nil && request.Streams() {
		request.OnDelta(model.TextDelta{Text: text[:len(text)-1]})
	}
	if gate != nil {
		select {
		case <-ctx.Done():
			return model.TextResponse{}, ctx.Err()
		case <-gate:
		}
	}
	if err := ctx.Err(); err != nil {
		return model.TextResponse{}, err
	}
	return model.TextResponse{Text: text, Diagnostic: model.TextDiagnostic{InputKnown: true, InputTokens: 100, OutputKnown: true, OutputTokens: 80}}, nil
}

func playFixture(t *testing.T, g *playProbe) (*App, PlayView) {
	t.Helper()
	a := newTestApp(t, g)
	pack, ok := a.Pack("mist-embers")
	if !ok {
		t.Fatal("pack missing")
	}
	g.positions = pack.Definition.InitialLocations
	view, err := a.CreatePlay(t.Context(), PlayCreateRequest{GameID: "mist-embers", Revision: pack.Definition.Revision, RequestKey: "create-play"})
	if err != nil {
		t.Fatal(err)
	}
	return a, view
}

func awaitPlay(t *testing.T, a *App, id string, done func(PlayView) bool) PlayView {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		view, err := a.ReadPlay(id)
		if err != nil {
			t.Fatal(err)
		}
		if done(view) {
			return view
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("play did not settle")
	return PlayView{}
}

func TestPlayPublishesOnlyCurrentPublicResources(t *testing.T) {
	a, view := playFixture(t, &playProbe{})
	pack, _ := a.Pack("mist-embers")
	if view.Player.Name != pack.Definition.Summary.Player.Name || view.World.Clock != view.Clock || view.World.Location == nil || len(view.Locations) == 0 {
		t.Fatal("player and world information missing")
	}
	var cash bool
	for _, state := range view.States {
		if state.StateID == "mirror_dissonance" {
			t.Fatal("hidden state published")
		}
		if state.StateID == "cash" {
			cash = state.Value.Integer == 8352 && state.DisplayValue != ""
		}
	}
	if !cash {
		t.Fatal("authored currency is missing")
	}
	for _, item := range view.Items {
		if item.LocationID == "abandoned-clinic" {
			t.Fatal("distant item published")
		}
	}
	if len(view.Items) != 5 {
		t.Fatal("opening possessions and visible desk items missing", view.Items)
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "personal_knowledge") || strings.Contains(string(encoded), "author_truth") || strings.Contains(string(encoded), "initial_states") {
		t.Fatal("internal author material published")
	}
}

func TestPlayAcceptedSceneCannotPublishPendingCandidate(t *testing.T) {
	g := &playProbe{}
	a, view := playFixture(t, g)
	p, _ := a.play(view.ID)
	// Simulate the interval between scene acceptance and worker bookkeeping.
	_, err := p.creation.Interact(t.Context(), turn.New(nil, turn.Deps{}), g, turn.CreationOptions{Input: "我问来访者。", Reasoning: model.ReasoningOff})
	if err != nil {
		t.Fatal(err)
	}
	p.latest = &playJob{request: PlayRequest{ExpectedTurn: 0}, view: PlayRun{Status: "running", Candidate: "候选正文", StartedAt: time.Now()}}
	accepted := p.viewLocked()
	if accepted.Turn != 1 || accepted.Run.Status != "completed" || accepted.Run.Candidate != "" || len(accepted.Messages) != 3 {
		t.Fatal("accepted scene also published a pending candidate", accepted.Run)
	}
}

func TestPlayAcceptsOnceWithFrozenOptionsAndPublicView(t *testing.T) {
	g := &playProbe{requests: make(chan model.TextRequest, 8), coreGate: make(chan struct{})}
	a, initial := playFixture(t, g)
	request := PlayRequest{RequestKey: "turn-1", Input: "我低声追问来访者，再请书记员谈谈看法。", Transport: transportStream, AllowPlotAdvance: true}
	view, err := a.SubmitPlay(initial.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	modelRequest := <-g.requests
	if modelRequest.Reasoning != model.ReasoningLow || view.Run.Reasoning != model.ReasoningLow || !modelRequest.JSON || !modelRequest.Streams() || !strings.Contains(modelRequest.Input, `"allow_plot_advance":true`) {
		t.Fatal("options not passed", modelRequest.Reasoning)
	}
	duplicate, err := a.SubmitPlay(initial.ID, request)
	if err != nil || duplicate.Run.ID != view.Run.ID {
		t.Fatal("duplicate", err)
	}
	changed := request
	changed.Reasoning = model.ReasoningOff
	if _, err := a.SubmitPlay(initial.ID, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("reasoning changed accepted request", err)
	}
	changed = request
	changed.AllowPlotAdvance = false
	if _, err := a.SubmitPlay(initial.ID, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed request", err)
	}
	pending := awaitPlay(t, a, initial.ID, func(v PlayView) bool { return v.Run.Candidate != "" })
	if pending.Clock != initial.Clock || len(pending.Messages) != 1 || strings.Contains(pending.Run.Candidate, "positions") {
		t.Fatal("candidate affected accepted state", pending)
	}
	close(g.coreGate)
	accepted := awaitPlay(t, a, initial.ID, func(v PlayView) bool { return v.Run.Status == "completed" })
	if accepted.Turn != 1 || len(accepted.Messages) != 3 || accepted.Messages[1].Content != request.Input || accepted.Clock == initial.Clock || accepted.Run.Candidate != "" || g.calls.Load() != 1 {
		t.Fatal("acceptance", accepted)
	}
	for i, message := range accepted.Messages {
		if message.Seq != int64(i+1) {
			t.Fatal("message order")
		}
	}
	encoded, _ := json.Marshal(accepted)
	if strings.Contains(string(encoded), "author_truth") || strings.Contains(string(encoded), "continuity_notes") || strings.Contains(string(encoded), "personal_knowledge") {
		t.Fatal("private fields published")
	}
	worlds, err := a.ListWorlds(t.Context())
	if err != nil || len(worlds) != 0 {
		t.Fatal("transient play created a save", worlds, err)
	}
	ledger, err := a.ReadUsage(t.Context(), "", 0)
	if err != nil || ledger.Totals.Calls != 1 || ledger.Calls[0].WorldID != initial.ID {
		t.Fatal("usage ledger", ledger, err)
	}
	stale := request
	stale.RequestKey = "stale-turn"
	if _, err := a.SubmitPlay(initial.ID, stale); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("stale turn", err)
	}
}

func TestPlayCancellationAndFailurePreserveAcceptedScene(t *testing.T) {
	g := &playProbe{requests: make(chan model.TextRequest, 8), coreGate: make(chan struct{})}
	a, initial := playFixture(t, g)
	view, err := a.SubmitPlay(initial.ID, PlayRequest{RequestKey: "cancel", Input: "我问来访者。", Transport: transportStream})
	if err != nil {
		t.Fatal(err)
	}
	<-g.requests
	if _, err := a.CancelPlay(initial.ID, view.Run.ID); err != nil {
		t.Fatal(err)
	}
	settled := awaitPlay(t, a, initial.ID, func(v PlayView) bool { return v.Run.Status == "cancelled" })
	if settled.Turn != 0 || len(settled.Messages) != 1 || settled.Clock != initial.Clock || settled.Run.Candidate != "" {
		t.Fatal("cancel changed state", settled)
	}
	close(g.coreGate)
	g.invalid.Store(true)
	if _, err := a.SubmitPlay(initial.ID, PlayRequest{RequestKey: "invalid", Input: "我继续询问。", Transport: transportNonStream}); err != nil {
		t.Fatal(err)
	}
	failed := awaitPlay(t, a, initial.ID, func(v PlayView) bool { return v.Run.Status == "failed" })
	if failed.Turn != 0 || len(failed.Messages) != 1 || failed.Run.Calls != 2 || failed.Run.Repairs != 1 {
		t.Fatal("failure changed state", failed)
	}
}

func TestPlayExplicitThinkingOffAndInvalidMode(t *testing.T) {
	g := &playProbe{requests: make(chan model.TextRequest, 8)}
	a, initial := playFixture(t, g)
	request := PlayRequest{RequestKey: "off", Input: "我问来访者。", Transport: transportNonStream, Reasoning: model.ReasoningOff}
	view, err := a.SubmitPlay(initial.ID, request)
	if err != nil || view.Run.Reasoning != model.ReasoningOff {
		t.Fatal("off mode", err)
	}
	if call := <-g.requests; call.Reasoning != model.ReasoningOff || call.ReasoningReserveTokens != 0 {
		t.Fatal("thinking off not honored")
	}
	awaitPlay(t, a, initial.ID, func(v PlayView) bool { return v.Run.Status == "completed" })
	request.RequestKey, request.ExpectedTurn, request.Reasoning = "invalid", 1, "unsupported"
	if _, err := a.SubmitPlay(initial.ID, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("invalid thinking mode accepted", err)
	}
}

func TestPlayStartsSelectFrozenAuthoredIdentity(t *testing.T) {
	a, initial := playFixture(t, &playProbe{})
	pack, _ := a.Pack("mist-embers")
	for _, option := range pack.Definition.StartingOptions {
		request := PlayCreateRequest{GameID: "mist-embers", Revision: pack.Definition.Revision, RequestKey: "create-" + option.ID, StartingOptionID: option.ID}
		view, err := a.CreatePlay(t.Context(), request)
		if err != nil || view.StartingOptionID != option.ID || view.Player.Name != option.Player.Name || view.World.Location.ID != option.InitialLocation || view.Messages[0].Content != option.Opening {
			t.Fatal("starting identity", option.ID, err)
		}
		duplicate, err := a.CreatePlay(t.Context(), request)
		if err != nil || duplicate.ID != view.ID {
			t.Fatal("creation recovery", err)
		}
		request.StartingOptionID = "local-investigator"
		if option.ID != request.StartingOptionID {
			if _, err := a.CreatePlay(t.Context(), request); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal("identity changed during retry", err)
			}
		}
		if pack.Definition.InitialLocations["player"] != "office" {
			t.Fatal("pack mutated")
		}
	}
	if _, err := a.CreatePlay(t.Context(), PlayCreateRequest{GameID: "mist-embers", Revision: pack.Definition.Revision, RequestKey: "unknown", StartingOptionID: "absent"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("unknown identity", err)
	}
	old, err := a.ReadPlay(initial.ID)
	if err != nil || old.Player != initial.Player || old.Turn != 0 {
		t.Fatal("existing story changed", err)
	}
}

func TestPlaySuggestionsArePlayerScopedAndLoseStalePublication(t *testing.T) {
	g := &playProbe{requests: make(chan model.TextRequest, 8), suggestionGate: make(chan struct{})}
	a, initial := playFixture(t, g)
	if _, err := a.RequestPlaySuggestions(initial.ID, 0, true); err != nil {
		t.Fatal(err)
	}
	request := <-g.requests
	pack, _ := a.Pack("mist-embers")
	if request.Reasoning != model.ReasoningOff || !request.JSON || request.Streams() {
		t.Fatal("suggestion parameters")
	}
	if strings.Contains(request.Input, pack.Definition.Secret) && pack.Definition.Secret != "" {
		t.Fatal("author facts in suggestions")
	}
	for _, character := range pack.Definition.Characters {
		if character.Knowledge != "" && strings.Contains(request.Input, character.Knowledge) {
			t.Fatal("private knowledge in suggestions")
		}
	}
	if _, err := a.RequestPlaySuggestions(initial.ID, 0, true); err != nil {
		t.Fatal(err)
	}
	if g.calls.Load() != 1 {
		t.Fatal("duplicate suggestion")
	}
	if _, err := a.SubmitPlay(initial.ID, PlayRequest{RequestKey: "new-scene", Input: "我继续追问。", Transport: transportNonStream}); err != nil {
		t.Fatal(err)
	}
	awaitPlay(t, a, initial.ID, func(v PlayView) bool { return v.Run.Status == "completed" })
	close(g.suggestionGate)
	if _, err := a.RequestPlaySuggestions(initial.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	ready := awaitPlay(t, a, initial.ID, func(v PlayView) bool { return v.Suggestions.Status == "ready" })
	if ready.Suggestions.Turn != 1 || len(ready.Suggestions.Items) != 3 || len(ready.Messages) != 3 {
		t.Fatal("suggestion basis", ready)
	}
	if _, err := a.RequestPlaySuggestions(initial.ID, 0, true); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("stale suggestions", err)
	}
}

func TestPlayCloseCancelsAndInvalidatesSession(t *testing.T) {
	g := &playProbe{requests: make(chan model.TextRequest, 8), coreGate: make(chan struct{})}
	a, initial := playFixture(t, g)
	if _, err := a.SubmitPlay(initial.ID, PlayRequest{RequestKey: "closing", Input: "我询问。", Transport: transportNonStream}); err != nil {
		t.Fatal(err)
	}
	<-g.requests
	if err := a.ClosePlay(initial.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadPlay(initial.ID); !errors.Is(err, ErrPlayNotFound) {
		t.Fatal("closed session readable", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
}
