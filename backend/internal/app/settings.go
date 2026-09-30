package app

import (
	"context"
	"strconv"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type UpdateNarrativeSettingsRequest struct {
	Perspective          string                     `json:"perspective"`
	Length               string                     `json:"length"`
	Detail               string                     `json:"detail"`
	PlayerElaboration    string                     `json:"player_elaboration"`
	NPCInitiative        string                     `json:"npc_initiative"`
	CustomInstruction    string                     `json:"custom_instruction"`
	Policies             *wiaworld.BehaviorPolicies `json:"behavior_policies,omitempty"`
	ExpectedContextEpoch int64                      `json:"expected_context_epoch"`
}

func (a *App) UpdateNarrativeSettings(ctx context.Context, worldID string, request UpdateNarrativeSettingsRequest) (wiaworld.NarrativeSettings, wiaworld.WorldSummary, error) {
	if request.Policies != nil && (request.ExpectedContextEpoch <= 0 || request.CustomInstruction != "") {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, ErrInvalidRequest
	}
	settings, err := wiaworld.ValidateNarrativeSettings(wiaworld.NarrativeSettings{
		Perspective: request.Perspective, Length: request.Length, Detail: request.Detail,
		PlayerElaboration: request.PlayerElaboration, NPCInitiative: request.NPCInitiative,
		CustomInstruction: request.CustomInstruction,
	})
	if err != nil {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
	}
	if status != "ready" {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, ErrWorldNotReady
	}
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	if worldRT.savePending {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, ErrWorldBusy
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
	}
	defer store.Close()
	if err := memory.Ready(ctx, store); err != nil {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
	}
	if request.Policies != nil {
		settings.Policies = *request.Policies
	} else {
		current, err := turn.LoadNarrativeSettings(ctx, store)
		if err != nil {
			return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
		}
		settings.Policies = current.Policies
		// Legacy clients submit writing preferences through the existing field.
		if settings.CustomInstruction != "" {
			settings.Policies.Narration = ""
		}
	}
	settings = turn.MigrateWritingPreference(settings)
	settings, err = wiaworld.ValidateNarrativeSettings(settings)
	if err != nil {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
	}
	if count, err := store.CountActiveRuns(ctx); err != nil {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
	} else if count > 0 {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, ErrWorldBusy
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
		if request.ExpectedContextEpoch > 0 && request.ExpectedContextEpoch != currentEpoch {
			return ErrVersionConflict
		}
		values := map[string]string{
			"behavior_policies":            wire.MarshalJSON(settings.Policies),
			"narrative_perspective":        settings.Perspective,
			"narrative_length":             settings.Length,
			"narrative_detail":             settings.Detail,
			"player_elaboration":           settings.PlayerElaboration,
			"npc_initiative":               settings.NPCInitiative,
			"narrative_custom_instruction": settings.CustomInstruction,
			"context_epoch":                strconv.FormatInt(currentEpoch+1, 10),
			"updated_at":                   wire.NowText(),
		}
		for key, value := range values {
			if err := tx.SetMeta(ctx, key, value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
	}
	if err := a.touchWorld(ctx, worldID); err != nil {
		return wiaworld.NarrativeSettings{}, wiaworld.WorldSummary{}, err
	}
	summary, err := a.worldSummary(ctx, worldID)
	return settings, summary, err
}
