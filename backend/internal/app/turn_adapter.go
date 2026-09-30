package app

import (
	"time"

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
	return turn.New(&turnHost{app: a}, a.turnDeps())
}

func (h *turnHost) LogStage(worldID string, run wiaworld.Run, stage turn.Stage, purpose, actorID string, stageIndex int, promptVersion string, sourceEventIDs []string, resolvedAddressee string, repairCount int, elapsed time.Duration) {
	h.app.logRunStage(worldID, run, stage, purpose, actorID, stageIndex, promptVersion, sourceEventIDs, resolvedAddressee, repairCount, elapsed)
}
