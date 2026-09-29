package storyapp

import (
	"context"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

// turnHost adapts the application to what a turn asks for.
//
// It exists so the pipeline can live in the turn package without the application
// exporting its internals: every method calls the same private stage function the
// in-package pipeline used, in the same order, so moving the pipeline changes where the
// order is written down and nothing else.
//
// The carried fields hold what one stage decides for the next — the roster and the
// per-character stage inputs — because those are stage handoff rather than results, and
// the turn package deliberately does not thread them through its host signature. A host
// serves exactly one turn.
type turnHost struct {
	app *App

	participants   []wiaworld.Character
	perceptText    map[string]string
	stageOneInputs map[string]turn.StageInput
}

// turnService is the application's turn runner, built per turn so a stale host cannot
// outlive the turn it belongs to.
func (a *App) turnService() *turn.Service {
	return turn.New(&turnHost{app: a})
}

func (h *turnHost) LogStage(worldID string, run wiaworld.Run, stage turn.Stage, purpose, actorID string, stageIndex int, promptVersion string, sourceEventIDs []string, resolvedAddressee string, repairCount int, elapsed time.Duration) {
	h.app.logRunStage(worldID, run, stage, purpose, actorID, stageIndex, promptVersion, sourceEventIDs, resolvedAddressee, repairCount, elapsed)
}

func (h *turnHost) LoadInput(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, generator model.TextGenerator, limit int) (turn.Snapshot, error) {
	return h.app.loadTurn(ctx, store, run, generator, limit)
}

func (h *turnHost) ResolveIntent(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run) (turn.TurnIntent, turn.Output, error) {
	intent, err := h.app.resolveIntentStage(ctx, generator, *snapshot, run)
	if err != nil {
		return turn.TurnIntent{}, turn.Output{}, err
	}
	h.participants = sceneCharacters(snapshot.Characters)
	output, perceptText, stageOneInputs, _ := newTurnOutput(snapshot, intent, run, intent.AddresseeID, intent.Private(), h.participants)
	h.perceptText, h.stageOneInputs = perceptText, stageOneInputs
	return intent, output, nil
}

func (h *turnHost) RunCharacters(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, output *turn.Output) error {
	return h.app.runCharacterStages(ctx, generator, snapshot, run, intent, h.participants, h.perceptText, h.stageOneInputs, output)
}

func (h *turnHost) Coordinate(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, output *turn.Output) error {
	host, visibleEvents, err := h.app.coordinateStage(ctx, generator, snapshot, run, intent, h.participants, intent.AddresseeID, intent.Private(), output)
	if err != nil {
		return err
	}
	output.VisibleEvents = append(output.VisibleEvents, visibleEvents...)
	return h.app.resolveSceneResult(ctx, generator, snapshot, run, intent, host, h.participants, output)
}

func (h *turnHost) Narrate(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, output *turn.Output) error {
	return h.app.narrateStage(ctx, generator, snapshot, run, intent, intent.AddresseeID, intent.Private(), output)
}
