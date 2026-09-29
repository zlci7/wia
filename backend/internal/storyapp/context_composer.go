package storyapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

// DeclinedSources are authorized records this request deliberately left out of
// the recent window. They stay retrievable, so they are reported but not treated
// as already supplied or already retrieved.

// oldest first; a section is an indivisible causal group
// Bounded rebuilds this material to fit an input budget. It reports changed=false
// when it is already as small as it can be, so a caller can tell "does not fit"
// from "was reduced".

// WindowShrunk records that the recent-experience window was reduced to fit the
// request budget rather than the request failing outright.

// ContextComposer has no database, model, or mutable cross-request state.

// A material that knows the whole request's input budget can shrink its own recent
// window before the composer has to give up.

// Keep the latest copy of a duplicate, retaining source identity.

// meteredTextFunc performs one model call with usage metering. It is a function rather
// than an interface because context assembly needs exactly one capability, and an
// interface for a single method would describe the application's shape instead of what a
// composed request actually requires.
type meteredTextFunc func(ctx context.Context, generator model.TextGenerator, request model.TextRequest, scope turn.ContextScope, report turn.ContextBuildReport) (model.TextResponse, error)

type contextGenerator struct {
	// metered is how a composed request is sent. It is nil when the generator is not
	// metered, in which case the call goes straight to the wrapped generator.
	//
	// It is the only thing this type ever needed from the application, and holding the
	// whole application for it is what kept the context assemblers from moving anywhere.
	metered       meteredTextFunc
	TextGenerator model.TextGenerator
	composer      turn.ContextComposer
	material      turn.Material
	logger        Logger
	calls         int
}

func (a *App) contextGenerator(generator model.TextGenerator, material turn.Material, snapshot turn.Snapshot, run wiaworld.Run, purpose, recipient string, stage int, template string) model.TextGenerator {
	window := model.WindowLimits{}
	if provider, ok := generator.(model.WindowProvider); ok {
		window = provider.ModelWindow()
	}
	reasoning := 0
	if provider, ok := generator.(model.TextReasoningProvider); ok {
		reasoning = provider.TextReasoningReserve()
	}
	return &contextGenerator{metered: a.meteredText, TextGenerator: generator, material: material, logger: a.logger, composer: turn.ContextComposer{Scope: turn.ContextScope{Owner: a.userID, Game: snapshot.Summary.GameID, World: snapshot.Summary.WorldID, Run: run.RunID, Attempt: run.Attempt, Stage: stage, Epoch: run.BaseContextEpoch, SceneVersion: snapshot.SceneVersion, Purpose: purpose, Recipient: recipient, Template: template, PolicyRevision: material.PolicyRevision}, Window: window, ReasoningReserve: reasoning}}
}

func (g *contextGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	req, report, err := g.composer.Build(g.material, request.System, request.MaxOutputTokens)
	if g.logger != nil {
		s := report.Scope
		g.logger.Printf("story context recall: world_id=%q run_id=%q purpose=%q recipient=%q included=%d excluded=%d", s.World, s.Run, s.Purpose, s.Recipient, report.RecallIncluded, report.RecallExcluded)
		g.logger.Printf("story context output budget: world_id=%q run_id=%q purpose=%q recipient=%q visible_tokens=%d reasoning_requested=%d reasoning_reserved=%d total_output_tokens=%d", s.World, s.Run, s.Purpose, s.Recipient, report.OutputTokens, report.ReasoningRequested, report.ReasoningReserved, report.TotalOutputTokens)
		g.logger.Printf("story context built: owner_id=%q game_id=%q world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d epoch=%d scene_version=%d template=%q policy_revision=%q sections=%q sources=%d excluded=%d duplicates=%d input_tokens=%d output_tokens=%d required_complete=%t window_known=%t success=%t failure=%q excluded_sources=%d selected_source_ids=%q", s.Owner, s.Game, s.World, s.Run, s.Attempt, s.Purpose, s.Recipient, s.Stage, s.Epoch, s.SceneVersion, s.Template, s.PolicyRevision, strings.Join(report.Sections, ","), report.Sources, report.Excluded, report.Duplicates, report.InputTokens, report.OutputTokens, report.RequiredComplete, report.WindowKnown, err == nil, report.Failure, report.ExcludedSources, strings.Join(report.SelectedSources, ","))
	}
	if err != nil {
		return model.TextResponse{}, err
	}
	g.calls++
	scope := g.composer.Scope
	started := time.Now()
	if g.logger != nil {
		g.logger.Printf("story model call started: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d call=%d", scope.World, scope.Run, scope.Attempt, scope.Purpose, scope.Recipient, scope.Stage, g.calls)
	}
	var response model.TextResponse
	var callErr error
	if g.metered != nil {
		response, callErr = g.metered(ctx, g.TextGenerator, req, scope, report)
	} else {
		response, callErr = g.TextGenerator.GenerateText(ctx, req)
	}
	diagnostic := response.Diagnostic
	var failure *model.TextCallError
	if errors.As(callErr, &failure) {
		diagnostic = failure.Diagnostic
	}
	if g.logger != nil {
		g.logger.Printf("story model call finished: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d call=%d elapsed_ms=%d success=%t error_code=%q http_status=%d provider_request_id=%q finish_reason=%q provider_input_tokens=%d provider_output_tokens=%d reasoning_tokens=%d content_chars=%d reasoning_chars=%d", scope.World, scope.Run, scope.Attempt, scope.Purpose, scope.Recipient, scope.Stage, g.calls, time.Since(started).Milliseconds(), callErr == nil, model.TextErrorCode(callErr), diagnostic.HTTPStatus, model.SafeRequestID(diagnostic.RequestID), model.SafeFinishReason(diagnostic.FinishReason), diagnostic.InputTokens, diagnostic.OutputTokens, diagnostic.ReasoningTokens, diagnostic.ContentChars, diagnostic.ReasoningChars)
	}
	return response, callErr
}
