package storyapp

import (
	"context"
	"fmt"
	"strings"

	"gameagent/backend/internal/memorymodel"
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
				return ErrContextSourceMissing
			}
		}
		records, err := tx.MemoryProjectionCandidates(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := tx.AppendMemorySourceIfAbsent(ctx, storage.MemorySourceWrite{
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

func memoryGroups(items []memorymodel.MemorySource) [][]memorymodel.MemorySource {
	var groups [][]memorymodel.MemorySource
	for _, item := range items {
		if len(groups) == 0 || groups[len(groups)-1][0].RunID != item.RunID {
			groups = append(groups, []memorymodel.MemorySource{})
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], item)
	}
	return groups
}

func memoryScopeIDs(snapshot turn.Snapshot) []string {
	ids := []string{"player"}
	for _, c := range snapshot.Characters {
		ids = append(ids, c.EntityID)
	}
	return ids
}

func memoryRecordsText(items []memorymodel.MemorySource) string {
	var b strings.Builder
	for _, s := range items {
		fmt.Fprintf(&b, "[%s；个人序号=%d；说话者=%s；类型=%s；来源=%s；记录于=%s] %s\n", s.ID, s.Seq, s.Actor, s.Kind, s.EventID, s.CreatedAt, s.Content)
	}
	return b.String()
}
