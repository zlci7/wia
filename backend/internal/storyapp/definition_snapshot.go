package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
)

func snapshotDefinition(ctx context.Context, store *storage.WorldStore, snapshot turn.Snapshot) (story.Definition, error) {
	raw, err := store.MetaGet(ctx, "definition_snapshot")
	if err == nil {
		var definition story.Definition
		if json.Unmarshal([]byte(raw), &definition) != nil || definition.Summary.ID != snapshot.Summary.GameID || definition.Revision == "" {
			return definition, ErrStorageUnavailable
		}
		// Promotion removes a passer-by from the world, so the stored list is the
		// authority once it exists. Legacy worlds keep display names only until the
		// next world starts from the same content.
		if stored, readErr := store.MetaGet(ctx, "bystander_refs"); readErr == nil {
			var refs []story.Bystander
			if json.Unmarshal([]byte(stored), &refs) == nil {
				definition.BystanderRefs = refs
			}
		} else if !errors.Is(readErr, sql.ErrNoRows) {
			return definition, readErr
		}
		return definition, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return story.Definition{}, err
	}
	// Legacy worlds use only their persisted facts, never a newer installed pack.
	definition := story.Definition{Summary: story.Summary{ID: snapshot.Summary.GameID, Mode: snapshot.Summary.Mode}, Characters: snapshot.Characters, Bystanders: snapshot.Bystanders, Clock: snapshot.Summary.Clock, Plot: snapshot.Plot, Settings: snapshot.Narrative}
	definition.Revision, _ = store.MetaGet(ctx, "game_revision")
	definition.Summary.Revision = definition.Revision
	if definition.Summary.ID == GameID {
		definition.Summary.Title = "暮灯镇的失踪信使"
	} else {
		definition.Summary.Title = definition.Summary.ID
	}
	return definition, nil
}
