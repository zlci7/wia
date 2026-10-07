package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

var ErrPlayNotFound = errors.New("play session not found")

type PlayCreateRequest struct {
	GameID           string `json:"game_id"`
	Revision         string `json:"expected_revision"`
	RequestKey       string `json:"request_key"`
	StartingOptionID string `json:"starting_option_id,omitempty"`
}

type PlayRequest struct {
	RequestKey       string              `json:"request_key"`
	ExpectedTurn     int64               `json:"expected_turn"`
	Input            string              `json:"input"`
	AllowPlotAdvance bool                `json:"allow_plot_advance"`
	Transport        string              `json:"transport"`
	Reasoning        model.ReasoningMode `json:"reasoning"`
}

type PlayRun struct {
	RequestKey string              `json:"request_key"`
	ID         string              `json:"id"`
	Input      string              `json:"input"`
	Status     string              `json:"status"`
	Transport  string              `json:"transport"`
	Reasoning  model.ReasoningMode `json:"reasoning"`
	Advance    bool                `json:"allow_plot_advance"`
	StartedAt  time.Time           `json:"started_at"`
	ElapsedMS  int64               `json:"elapsed_ms"`
	Candidate  string              `json:"candidate,omitempty"`
	Calls      int                 `json:"calls"`
	Repairs    int                 `json:"repairs"`
	Error      string              `json:"error,omitempty"`
	ErrorCode  string              `json:"error_code,omitempty"`
}

type PlaySuggestions struct {
	Turn    int64    `json:"turn"`
	Enabled bool     `json:"enabled"`
	Status  string   `json:"status"`
	Items   []string `json:"items"`
}

type PlayView struct {
	Version          int64                      `json:"version"`
	ID               string                     `json:"id"`
	GameID           string                     `json:"game_id"`
	Title            string                     `json:"title"`
	Revision         string                     `json:"revision"`
	StartingOptionID string                     `json:"starting_option_id,omitempty"`
	Turn             int64                      `json:"turn"`
	Clock            string                     `json:"clock"`
	Location         string                     `json:"location"`
	Messages         []wiaworld.Message         `json:"messages"`
	Characters       []wiaworld.PublicCharacter `json:"characters"`
	World            wiaworld.WorldSummary      `json:"world"`
	Player           PlayPlayer                 `json:"player"`
	States           []wiaworld.PublicState     `json:"states"`
	Items            []wiaworld.PublicItem      `json:"items"`
	Locations        []wiaworld.KnownLocation   `json:"known_locations"`
	Bystanders       []story.Bystander          `json:"bystanders"`
	Run              *PlayRun                   `json:"run,omitempty"`
	Suggestions      PlaySuggestions            `json:"suggestions"`
}

type PlayPlayer struct {
	Name    string `json:"name"`
	Profile string `json:"profile"`
}

type playJob struct {
	request PlayRequest
	view    PlayRun
	cancel  context.CancelFunc
	done    chan struct{}
}

// playSession manages browser requests around the existing creation session.
// Its accepted world and resources live only for the lifetime of the application.
type playSession struct {
	mu               sync.Mutex
	id               string
	create           PlayCreateRequest
	creation         *turn.CreationSession
	jobs             map[string]*playJob
	latest           *playJob
	closed           bool
	viewVersion      int64
	suggestions      PlaySuggestions
	suggestionCancel context.CancelFunc
	suggestionID     string
}

func (a *App) CreatePlay(ctx context.Context, request PlayCreateRequest) (PlayView, error) {
	if !runtimeID.MatchString(request.RequestKey) || request.GameID == "" || request.Revision == "" {
		return PlayView{}, ErrInvalidRequest
	}
	a.copyMu.Lock()
	defer a.copyMu.Unlock()
	if a.closing {
		return PlayView{}, ErrWorldBusy
	}
	a.playMu.Lock()
	defer a.playMu.Unlock()
	for _, p := range a.playSessions {
		if p.create.RequestKey == request.RequestKey {
			if p.create != request {
				return PlayView{}, ErrIdempotencyConflict
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.viewLocked(), nil
		}
	}
	if len(a.playSessions) >= 8 {
		return PlayView{}, ErrWorldBusy
	}
	pack, ok := a.Pack(request.GameID)
	if !ok {
		return PlayView{}, ErrWorldNotFound
	}
	if pack.Definition.Revision != request.Revision {
		return PlayView{}, ErrVersionConflict
	}
	definition, err := story.WithStartingOption(pack.Definition, request.StartingOptionID)
	if err != nil {
		return PlayView{}, ErrInvalidRequest
	}
	creation, err := turn.NewCreationSession(definition)
	if err != nil {
		return PlayView{}, err
	}
	if err := ctx.Err(); err != nil {
		return PlayView{}, err
	}
	p := &playSession{id: creation.PlayerSnapshot().Summary.WorldID, create: request, creation: creation, jobs: map[string]*playJob{}, suggestions: PlaySuggestions{Enabled: true, Status: "empty", Items: []string{}}}
	if a.playSessions == nil {
		a.playSessions = map[string]*playSession{}
	}
	a.playSessions[p.id] = p
	return p.viewLocked(), nil
}

func (a *App) play(id string) (*playSession, error) {
	a.playMu.Lock()
	defer a.playMu.Unlock()
	p := a.playSessions[id]
	if p == nil {
		return nil, ErrPlayNotFound
	}
	return p, nil
}

func (p *playSession) viewLocked() PlayView {
	p.viewVersion++
	snapshot := p.creation.PlayerSnapshot()
	v := PlayView{Version: p.viewVersion, ID: p.id, GameID: p.create.GameID, Title: snapshot.Definition.Summary.Title, Revision: p.create.Revision,
		StartingOptionID: p.create.StartingOptionID,
		Turn:             snapshot.Summary.TurnSeq, Clock: snapshot.Summary.Clock, Location: snapshot.SceneLocation, Messages: snapshot.Messages,
		Characters: PublicCharacterViews(snapshot.Characters), Suggestions: p.suggestions,
		World: snapshot.Summary, Player: PlayPlayer{snapshot.PlayerName, snapshot.PlayerProfile},
		States: turn.PlayerStateProjection(snapshot), Items: turn.PlayerItemProjection(snapshot),
		Locations: turn.PlayerLocationProjection(snapshot), Bystanders: slices.Clone(snapshot.BystanderRefs)}
	for _, location := range snapshot.Definition.Locations {
		if location.ID == snapshot.SceneLocation {
			v.Location = location.Name
			break
		}
	}
	v.Suggestions.Items = slices.Clone(p.suggestions.Items)
	if v.StartingOptionID == "" && len(snapshot.Definition.StartingOptions) > 0 {
		v.StartingOptionID = snapshot.Definition.StartingOptions[0].ID
	}
	if p.latest != nil {
		run := p.latest.view
		// Scene acceptance can precede the worker's final bookkeeping. Publish
		// the accepted scene and its completed status as one consistent view.
		if run.Status == "running" && snapshot.Summary.TurnSeq > p.latest.request.ExpectedTurn {
			run.Status, run.Candidate = "completed", ""
			run.ElapsedMS = time.Since(run.StartedAt).Milliseconds()
		}
		v.Run = &run
	}
	return v
}

func (a *App) ReadPlay(id string) (PlayView, error) {
	p, err := a.play(id)
	if err != nil {
		return PlayView{}, err
	}
	p.mu.Lock()
	if job := p.latest; job != nil && job.done != nil && job.view.Status == "running" && p.creation.Status().Turn > job.request.ExpectedTurn {
		// Acceptance is complete; let the worker publish its terminal metadata
		// before exposing a view that permits the next action.
		p.mu.Unlock()
		<-job.done
		p.mu.Lock()
	}
	defer p.mu.Unlock()
	if p.closed {
		return PlayView{}, ErrPlayNotFound
	}
	return p.viewLocked(), nil
}

func playBusy(p *playSession) bool { return p.latest != nil && p.latest.view.Status == "running" }

func (a *App) SubmitPlay(id string, request PlayRequest) (PlayView, error) {
	if request.Reasoning == model.ReasoningDefault {
		request.Reasoning = model.ReasoningLow
	}
	if request.Reasoning != model.ReasoningOff && request.Reasoning != model.ReasoningLow {
		return PlayView{}, ErrInvalidRequest
	}
	if !runtimeID.MatchString(request.RequestKey) || !utf8.ValidString(request.Input) || strings.TrimSpace(request.Input) == "" || utf8.RuneCountInString(request.Input) > 4000 || request.ExpectedTurn < 0 || (request.Transport != transportStream && request.Transport != transportNonStream) {
		return PlayView{}, ErrInvalidRequest
	}
	a.copyMu.Lock()
	defer a.copyMu.Unlock()
	if a.closing {
		return PlayView{}, ErrWorldBusy
	}
	p, err := a.play(id)
	if err != nil {
		return PlayView{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return PlayView{}, ErrPlayNotFound
	}
	if job := p.jobs[request.RequestKey]; job != nil {
		if job.request != request {
			return PlayView{}, ErrIdempotencyConflict
		}
		return p.viewLocked(), nil
	}
	if playBusy(p) {
		return PlayView{}, ErrWorldBusy
	}
	if p.creation.Status().Turn != request.ExpectedTurn {
		return PlayView{}, ErrVersionConflict
	}
	a.modelMu.RLock()
	generator := a.generator
	a.modelMu.RUnlock()
	if generator == nil {
		return PlayView{}, ErrModelNotConfigured
	}
	if request.Transport == transportStream {
		capability, ok := generator.(model.TextStreamingProvider)
		if !ok || !capability.SupportsTextStreaming() {
			return PlayView{}, ErrInvalidRequest
		}
	}
	if len(p.jobs) >= 256 {
		return PlayView{}, ErrWorldBusy
	}
	p.cancelSuggestionsLocked()
	p.suggestions.Status, p.suggestions.Items = "empty", []string{}
	ctx, cancel := context.WithTimeout(a.copyCtx, turn.GenerationTimeBudget)
	job := &playJob{request: request, cancel: cancel, done: make(chan struct{}), view: PlayRun{ID: wire.NewID("playrun"), RequestKey: request.RequestKey, Input: request.Input, Status: "running", StartedAt: time.Now().UTC(), Transport: request.Transport, Advance: request.AllowPlotAdvance, Reasoning: request.Reasoning}}
	p.jobs[request.RequestKey], p.latest = job, job
	a.copyWG.Add(1)
	go a.runPlay(ctx, p, job, generator)
	return p.viewLocked(), nil
}

func (a *App) runPlay(ctx context.Context, p *playSession, job *playJob, generator model.TextGenerator) {
	defer a.copyWG.Done()
	defer close(job.done)
	defer job.cancel()
	deps := a.turnDeps()
	deps.Meter = func(callCtx context.Context, g model.TextGenerator, request model.TextRequest, scope turn.ContextScope, report turn.ContextBuildReport) (model.TextResponse, error) {
		p.mu.Lock()
		job.view.Candidate = ""
		job.view.Calls++
		p.mu.Unlock()
		var raw strings.Builder
		observer := request.OnDelta
		request.OnDelta = func(delta model.TextDelta) {
			if request.Streams() && delta.Text != "" && raw.Len()+len(delta.Text) <= 1<<20 {
				raw.WriteString(delta.Text)
				candidate := turn.NarrativePrefix(raw.String())
				p.mu.Lock()
				if job.view.Status == "running" && !p.closed {
					job.view.Candidate = candidate
				}
				p.mu.Unlock()
			}
			if observer != nil {
				observer(delta)
			}
		}
		scope.World, scope.Run = p.id, job.view.ID
		return a.meteredText(callCtx, g, request, scope, report)
	}
	stream := job.request.Transport == transportStream
	result, err := p.creation.Interact(ctx, turn.New(nil, deps), generator, turn.CreationOptions{Input: job.request.Input, AllowPlotAdvance: job.request.AllowPlotAdvance, Reasoning: job.request.Reasoning, Streaming: &stream})
	p.mu.Lock()
	defer p.mu.Unlock()
	job.view.ElapsedMS, job.view.Repairs = time.Since(job.view.StartedAt).Milliseconds(), result.Report.Repairs
	job.view.Candidate = ""
	if err != nil {
		job.view.Status, job.view.ErrorCode = "failed", turn.ErrorCode(err)
		job.view.Error = "本轮未完成，输入和上一轮情境已保留。"
		if errors.Is(err, turn.ErrContextCapacity) {
			job.view.Error = "本轮必需内容超出模型容量，输入和上一轮情境已保留。"
		}
		if errors.Is(err, model.ErrTextReasoningUnsupported) || errors.Is(err, model.ErrTextFormatUnsupported) {
			job.view.Error = "当前模型连接尚不支持共创所需的请求选项，请选择已支持的 DeepSeek 连接。输入已保留。"
		}
		if errors.Is(err, context.Canceled) {
			job.view.Status, job.view.ErrorCode, job.view.Error = "cancelled", "cancelled", "本轮已取消，输入和上一轮情境已保留。"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			job.view.ErrorCode, job.view.Error = "generation_timeout", "模型响应超时，输入和上一轮情境已保留。"
		}
		return
	}
	job.view.Status = "completed"
	p.suggestions.Turn = p.creation.Status().Turn
	if !p.closed && p.suggestions.Enabled {
		p.suggestions.Status = "empty"
	}
}

func (a *App) CancelPlay(id, runID string) (PlayView, error) {
	p, err := a.play(id)
	if err != nil {
		return PlayView{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return PlayView{}, ErrPlayNotFound
	}
	if p.latest == nil || p.latest.view.ID != runID {
		return PlayView{}, ErrRunNotFound
	}
	if playBusy(p) {
		p.creation.Cancel()
		p.latest.cancel()
	}
	return p.viewLocked(), nil
}

func (a *App) ClosePlay(id string) error {
	p, err := a.play(id)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.closed = true
	p.cancelSuggestionsLocked()
	if playBusy(p) {
		p.creation.Cancel()
		p.latest.cancel()
	}
	p.mu.Unlock()
	a.playMu.Lock()
	delete(a.playSessions, id)
	a.playMu.Unlock()
	return nil
}

func (p *playSession) cancelSuggestionsLocked() {
	if p.suggestionCancel != nil {
		p.suggestionCancel()
	}
	p.suggestionCancel, p.suggestionID = nil, ""
}

func (a *App) RequestPlaySuggestions(id string, expectedTurn int64, enabled bool) (PlayView, error) {
	a.copyMu.Lock()
	defer a.copyMu.Unlock()
	if a.closing {
		return PlayView{}, ErrWorldBusy
	}
	p, err := a.play(id)
	if err != nil {
		return PlayView{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return PlayView{}, ErrPlayNotFound
	}
	if p.creation.Status().Turn != expectedTurn {
		return PlayView{}, ErrVersionConflict
	}
	if playBusy(p) {
		return PlayView{}, ErrWorldBusy
	}
	p.suggestions.Enabled = enabled
	if !enabled {
		p.cancelSuggestionsLocked()
		p.suggestions.Status = "empty"
		p.suggestions.Items = []string{}
		return p.viewLocked(), nil
	}
	if p.suggestions.Turn == expectedTurn && (p.suggestions.Status == "ready" || p.suggestions.Status == "generating") {
		return p.viewLocked(), nil
	}
	a.modelMu.RLock()
	generator := a.generator
	a.modelMu.RUnlock()
	if generator == nil {
		return PlayView{}, ErrModelNotConfigured
	}
	snapshot, material := p.creation.SuggestionContext()
	snapshot.Summary.WorldID = p.id
	ctx, cancel := context.WithTimeout(a.copyCtx, 20*time.Second)
	idCall := wire.NewID("suggestions")
	p.suggestionID, p.suggestionCancel = idCall, cancel
	p.suggestions.Turn, p.suggestions.Status, p.suggestions.Items = expectedTurn, "generating", []string{}
	a.copyWG.Add(1)
	go func() {
		defer a.copyWG.Done()
		defer cancel()
		run := wiaworld.Run{RunID: idCall, Attempt: 1}
		g := a.contextGenerator(generator, material, snapshot, run, "suggestions", "player", 0, "story.suggestions.v2")
		var result struct {
			Items []string `json:"items"`
		}
		stream := false
		_, err := turn.GenerateJSONRequestCheckedMetrics(ctx, g, model.TextRequest{System: material.System, Input: material.Required, MaxInputTokens: 12000, MaxOutputTokens: 1024, MaxResponseBytes: 1 << 20, Reasoning: model.ReasoningOff, JSON: true, Streaming: &stream}, &result, nil, []string{"items"}, func() error { return turn.ValidateSuggestions(result.Items) })
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.closed || p.suggestionID != idCall || p.creation.Status().Turn != expectedTurn || playBusy(p) {
			return
		}
		p.suggestionID, p.suggestionCancel = "", nil
		p.suggestions.Status = "ready"
		if err != nil || ctx.Err() != nil {
			p.suggestions.Status = "failed"
			return
		}
		for i, item := range result.Items {
			result.Items[i] = wire.Clean(item)
		}
		p.suggestions.Items = result.Items
	}()
	return p.viewLocked(), nil
}
