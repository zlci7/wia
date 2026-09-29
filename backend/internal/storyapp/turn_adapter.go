package storyapp

import (
	"context"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

// turnHost adapts the application to what a turn still asks it for.
//
// It exists so the pipeline can live in the turn package without the application
// exporting its internals: every method calls the same private stage function the
// in-package pipeline used, in the same order, so moving the pipeline changed where the
// order is written down and nothing else.
//
// It holds no state. Everything one stage decides for the next travels in the turn's own
// output, which is why a host can serve a turn without remembering anything about it.
type turnHost struct {
	app *App
}

// turnService is the application's turn runner.
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
	return intent, turn.OpenOutput(snapshot, intent, run), nil
}

func (h *turnHost) RunCharacters(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, output *turn.Output) error {
	return h.app.runCharacterStages(ctx, generator, snapshot, run, intent, turn.InScene(snapshot.Characters), output.PerceptText, output.StageOneInputs, output)
}

func (h *turnHost) Coordinate(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, output *turn.Output) error {
	participants := turn.InScene(snapshot.Characters)
	host, visibleEvents, err := h.app.coordinateStage(ctx, generator, snapshot, run, intent, participants, intent.AddresseeID, intent.Private(), output)
	if err != nil {
		return err
	}
	output.VisibleEvents = append(output.VisibleEvents, visibleEvents...)
	return h.app.resolveSceneResult(ctx, generator, snapshot, run, intent, host, participants, output)
}

func (h *turnHost) Narrate(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, output *turn.Output) error {
	return h.app.narrateStage(ctx, generator, snapshot, run, intent, intent.AddresseeID, intent.Private(), output)
}
