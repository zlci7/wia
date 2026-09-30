package storyapp

import (
	"context"
	"fmt"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
)

const memorySchema = `
CREATE TABLE IF NOT EXISTS memory_sources (
 scope TEXT NOT NULL, seq INTEGER NOT NULL, source_id TEXT NOT NULL,
 event_id TEXT NOT NULL, run_id TEXT NOT NULL, actor TEXT NOT NULL,
 kind TEXT NOT NULL, content TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(scope,seq), UNIQUE(scope,source_id)
);
CREATE INDEX IF NOT EXISTS idx_memory_source_event ON memory_sources(event_id,scope);
CREATE TABLE IF NOT EXISTS memory_digests (
 scope TEXT NOT NULL, revision INTEGER NOT NULL, epoch INTEGER NOT NULL,
 through_seq INTEGER NOT NULL, source_head INTEGER NOT NULL,
 content TEXT NOT NULL, states TEXT NOT NULL, sources TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(scope,revision)
);`

// This index copies only already-authorized projections. Joining an event grants
// speaker/time metadata, never its body, except the speaker's own public speech.
//
// The precondition checks, the candidate read and the appends share one transaction so
// the rows that were judged eligible are the rows written: a commit in between could
// add a perception pointing at an event that arrived after the check.
func indexMemorySources(ctx context.Context, store *storage.WorldStore) error {
	return store.InTx(ctx, func(tx *storage.WorldTx) error {
		for _, table := range []string{"perceptions", "memories"} {
			missing, err := tx.CountMissingEventSources(ctx, table)
			if err != nil {
				return err
			}
			if missing > 0 {
				return turn.ErrContextSourceMissing
			}
		}
		records, err := tx.MemoryProjectionCandidates(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := appendMemorySource(ctx, tx, storage.MemorySourceWrite{
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

func appendMemorySource(ctx context.Context, tx *storage.WorldTx, record storage.MemorySourceWrite) error {
	result, err := tx.AppendMemorySource(ctx, record)
	if err != nil {
		return err
	}
	if result == storage.MemorySourceContentConflict {
		return fmt.Errorf("%w: memory source %q in scope %q has conflicting content", ErrStorageUnavailable, record.ID, record.Scope)
	}
	return nil
}

func memoryScopeIDs(snapshot turn.Snapshot) []string {
	ids := []string{"player"}
	for _, c := range snapshot.Characters {
		ids = append(ids, c.EntityID)
	}
	return ids
}
