package storyapp

import (
	"context"
	"gameagent/backend/internal/wire"
	"strconv"
)

// The lead character's public name and profile belong to one world. Editing them
// carries the world's context epoch and advances it, so derived optional material
// such as action suggestions is rebuilt while committed prose and events stay.
type UpdatePlayerProfileRequest struct {
	PlayerName           string `json:"player_name"`
	PlayerProfile        string `json:"player_profile"`
	ExpectedContextEpoch int64  `json:"expected_context_epoch"`
}

func (a *App) UpdatePlayerProfile(ctx context.Context, worldID string, request UpdatePlayerProfileRequest) (WorldSummary, error) {
	playerName, playerProfile := wire.Clean(request.PlayerName), wire.Clean(request.PlayerProfile)
	if playerName == "" || len([]rune(playerName)) > 80 || len([]rune(playerProfile)) > 2000 || request.ExpectedContextEpoch < 1 {
		return WorldSummary{}, ErrInvalidRequest
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return WorldSummary{}, err
	}
	if status != "ready" {
		return WorldSummary{}, ErrWorldNotReady
	}
	world := a.worldRuntimeFor(worldID)
	world.mu.Lock()
	defer world.mu.Unlock()
	if world.savePending {
		return WorldSummary{}, ErrWorldBusy
	}
	store, err := openWorldDB(path)
	if err != nil {
		return WorldSummary{}, err
	}
	defer store.db.Close()
	if err := memoryReady(ctx, store.db); err != nil {
		return WorldSummary{}, err
	}
	if count, err := countActiveRuns(ctx, store.db); err != nil {
		return WorldSummary{}, err
	} else if count > 0 {
		return WorldSummary{}, ErrWorldBusy
	}
	snapshot, err := loadWorldSnapshot(ctx, store, 1)
	if err != nil {
		return WorldSummary{}, err
	}
	if !snapshot.Definition.Summary.Player.Editable {
		return WorldSummary{}, ErrInvalidRequest
	}
	if playerName == snapshot.PlayerName && playerProfile == snapshot.PlayerProfile {
		// Nothing to change: confirm the epoch and report without advancing it.
		if request.ExpectedContextEpoch != snapshot.Summary.ContextEpoch {
			return WorldSummary{}, ErrVersionConflict
		}
		return a.worldSummary(ctx, worldID)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return WorldSummary{}, err
	}
	defer tx.Rollback()
	currentEpochText, err := metaGetTx(ctx, tx, "context_epoch")
	if err != nil {
		return WorldSummary{}, err
	}
	currentEpoch, err := strconv.ParseInt(currentEpochText, 10, 64)
	if err != nil {
		return WorldSummary{}, err
	}
	if request.ExpectedContextEpoch != currentEpoch {
		return WorldSummary{}, ErrVersionConflict
	}
	for key, value := range map[string]string{
		"player_name":    playerName,
		"player_profile": playerProfile,
		"context_epoch":  strconv.FormatInt(currentEpoch+1, 10),
		"updated_at":     wire.NowText(),
	} {
		if err := metaSetTx(ctx, tx, key, value); err != nil {
			return WorldSummary{}, err
		}
	}
	// The old material is no longer a valid basis for derived optional content.
	if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key='suggestion_set'`); err != nil {
		return WorldSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorldSummary{}, err
	}
	return a.worldSummary(ctx, worldID)
}
