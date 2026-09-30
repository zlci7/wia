package memory

import (
	"context"
	"fmt"

	"gameagent/backend/internal/storage"
	wiaworld "gameagent/backend/internal/world"
)

// This index copies only already-authorized projections. Joining an event grants
// speaker/time metadata, never its body, except the speaker's own public speech.
//
// The precondition checks, the candidate read and the appends share one transaction so
// the rows that were judged eligible are the rows written: a commit in between could
// add a perception pointing at an event that arrived after the check.
func IndexSources(ctx context.Context, store *storage.WorldStore) error {
	return store.InTx(ctx, func(tx *storage.WorldTx) error {
		for _, table := range []string{"perceptions", "memories"} {
			missing, err := tx.CountMissingEventSources(ctx, table)
			if err != nil {
				return err
			}
			if missing > 0 {
				return ErrContextSourceMissing
			}
		}
		records, err := tx.MemoryProjectionCandidates(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := AppendSource(ctx, tx, storage.MemorySourceWrite{
				Scope:     record.Scope,
				ID:        record.ID,
				EventID:   record.EventID,
				RunID:     record.RunID,
				Actor:     record.Actor,
				Kind:      record.Kind,
				Content:   record.Content,
				CreatedAt: record.CreatedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func AppendSource(ctx context.Context, tx *storage.WorldTx, record storage.MemorySourceWrite) error {
	result, err := tx.AppendMemorySource(ctx, record)
	if err != nil {
		return err
	}
	if result == storage.MemorySourceContentConflict {
		return fmt.Errorf("%w: memory source %q in scope %q has conflicting content", ErrStorageUnavailable, record.ID, record.Scope)
	}
	return nil
}

func ScopeIDs(characters []wiaworld.Character) []string {
	ids := []string{"player"}
	for _, c := range characters {
		ids = append(ids, c.EntityID)
	}
	return ids
}
