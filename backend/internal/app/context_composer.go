package app

import (
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

// contextGenerator wraps a model generator for one composed call. The wrapper itself
// lives in turn now; this method only supplies what the application can still answer for
// it — the account the world belongs to, usage metering and the logger.
func (a *App) contextGenerator(generator model.TextGenerator, material turn.Material, snapshot turn.Snapshot, run wiaworld.Run, purpose, recipient string, stage int, template string) model.TextGenerator {
	return turn.NewContextGenerator(a.turnDeps(), a.userID, generator, material, snapshot, run, purpose, recipient, stage, template)
}

// turnDeps is what a turn asks the application for beyond its host operations.
func (a *App) turnDeps() turn.Deps {
	return turn.Deps{Logger: a.logger, Meter: a.meteredText, Owner: a.userID}
}
