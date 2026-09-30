package app

import (
	"time"

	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

// turnHost connects turn stage observations to application diagnostics.
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
