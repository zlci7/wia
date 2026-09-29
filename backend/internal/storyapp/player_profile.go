package storyapp

import (
	"context"
	"strconv"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// The lead character's public name and profile belong to one world. Editing them
// carries the world's context epoch and advances it, so derived optional material
// such as action suggestions is rebuilt while committed prose and events stay.
type UpdatePlayerProfileRequest struct {
	PlayerName           string `json:"player_name"`
	PlayerProfile        string `json:"player_profile"`
	ExpectedContextEpoch int64  `json:"expected_context_epoch"`
}

func (a *App) UpdatePlayerProfile(ctx context.Context, worldID string, request UpdatePlayerProfileRequest) (wiaworld.WorldSummary, error) {
	playerName, playerProfile := wire.Clean(request.PlayerName), wire.Clean(request.PlayerProfile)
	if playerName == "" || len([]rune(playerName)) > 80 || len([]rune(playerProfile)) > 2000 || request.ExpectedContextEpoch < 1 {
		return wiaworld.WorldSummary{}, ErrInvalidRequest
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return wiaworld.WorldSummary{}, err
	}
	if status != "ready" {
		return wiaworld.WorldSummary{}, ErrWorldNotReady
	}
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	if worldRT.savePending {
		return wiaworld.WorldSummary{}, ErrWorldBusy
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return wiaworld.WorldSummary{}, err
	}
	defer store.Close()
	if err := memoryReady(ctx, store); err != nil {
		return wiaworld.WorldSummary{}, err
	}
	if count, err := store.CountActiveRuns(ctx); err != nil {
		return wiaworld.WorldSummary{}, err
	} else if count > 0 {
		return wiaworld.WorldSummary{}, ErrWorldBusy
	}
	snapshot, err := loadTurnSnapshot(ctx, store, 1)
	if err != nil {
		return wiaworld.WorldSummary{}, err
	}
	if !snapshot.Definition.Summary.Player.Editable {
		return wiaworld.WorldSummary{}, ErrInvalidRequest
	}
	if playerName == snapshot.PlayerName && playerProfile == snapshot.PlayerProfile {
		// Nothing to change: confirm the epoch and report without advancing it.
		if request.ExpectedContextEpoch != snapshot.Summary.ContextEpoch {
			return wiaworld.WorldSummary{}, ErrVersionConflict
		}
		return a.worldSummary(ctx, worldID)
	}
	if err := store.InTx(ctx, func(tx *storage.WorldTx) error {
		currentEpochText, err := tx.GetMeta(ctx, "context_epoch")
		if err != nil {
			return err
		}
		currentEpoch, err := strconv.ParseInt(currentEpochText, 10, 64)
		if err != nil {
			return err
		}
		if request.ExpectedContextEpoch != currentEpoch {
			return ErrVersionConflict
		}
		for key, value := range map[string]string{
			"player_name":    playerName,
			"player_profile": playerProfile,
			"context_epoch":  strconv.FormatInt(currentEpoch+1, 10),
			"updated_at":     wire.NowText(),
		} {
			if err := tx.SetMeta(ctx, key, value); err != nil {
				return err
			}
		}
		// The old material is no longer a valid basis for derived optional content.
		return tx.DeleteMeta(ctx, "suggestion_set")
	}); err != nil {
		return wiaworld.WorldSummary{}, err
	}
	return a.worldSummary(ctx, worldID)
}
