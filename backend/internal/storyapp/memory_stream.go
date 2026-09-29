package storyapp

import (
	"context"
	"database/sql"
	"fmt"
	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/storage"
	"strings"
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
func indexMemorySources(ctx context.Context, store *storage.WorldStore) error {
	tx, err := store.Database().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var missing int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM perceptions p LEFT JOIN events e ON e.event_id=p.source_event_id WHERE e.event_id IS NULL`).Scan(&missing); err != nil {
		return err
	}
	if missing > 0 {
		return ErrContextSourceMissing
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories m LEFT JOIN events e ON e.event_id=m.source_event_id WHERE e.event_id IS NULL`).Scan(&missing); err != nil {
		return err
	}
	if missing > 0 {
		return ErrContextSourceMissing
	}
	rows, err := tx.QueryContext(ctx, `SELECT scope,id,event_id,run_id,actor,kind,content,created_at FROM (
 SELECT p.recipient_id scope,'perception:'||p.seq id,e.event_id,e.run_id,e.actor_id actor,'perception:'||p.source_type kind,p.content,p.created_at,e.seq ordering,0 priority,p.seq tie_seq FROM perceptions p JOIN events e ON e.event_id=p.source_event_id WHERE e.run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed')
 UNION ALL
 SELECT m.recipient_id,'memory:'||m.seq,e.event_id,e.run_id,m.recipient_id,'subjective:'||m.kind,m.content,m.created_at,e.seq,1,m.seq FROM memories m JOIN events e ON e.event_id=m.source_event_id WHERE e.run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed')
 UNION ALL
 SELECT e.actor_id,'speech:'||e.event_id,e.event_id,e.run_id,e.actor_id,'own_speech',e.content,e.created_at,e.seq,2,e.seq FROM events e WHERE e.event_type='npc_dialogue' AND EXISTS(SELECT 1 FROM characters c WHERE c.entity_id=e.actor_id) AND EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed')
 UNION ALL
 SELECT 'player','message:'||m.message_id,'',m.run_id,CASE WHEN m.kind='player' THEN 'player' ELSE 'narrator' END,'message:'||m.kind,m.content,m.created_at,0,3,m.seq FROM messages m WHERE m.message_id='opening' OR m.run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=m.run_id AND r.status='completed')
) ORDER BY ordering,priority,tie_seq,id`)
	if err != nil {
		return err
	}
	var records []memorymodel.MemorySource
	for rows.Next() {
		var r memorymodel.MemorySource
		if err = rows.Scan(&r.Scope, &r.ID, &r.EventID, &r.RunID, &r.Actor, &r.Kind, &r.Content, &r.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range records {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_sources(scope,seq,source_id,event_id,run_id,actor,kind,content,created_at) SELECT ?,COALESCE(MAX(seq),0)+1,?,?,?,?,?,?,? FROM memory_sources WHERE scope=?`, r.Scope, r.ID, r.EventID, r.RunID, r.Actor, r.Kind, r.Content, r.CreatedAt, r.Scope)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func readMemorySources(ctx context.Context, db *sql.DB, scope string, after int64) ([]memorymodel.MemorySource, error) {
	rows, err := db.QueryContext(ctx, `SELECT scope,seq,source_id,event_id,run_id,actor,kind,content,created_at FROM memory_sources WHERE scope=? AND seq>? ORDER BY seq`, scope, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []memorymodel.MemorySource{}
	for rows.Next() {
		var s memorymodel.MemorySource
		if err = rows.Scan(&s.Scope, &s.Seq, &s.ID, &s.EventID, &s.RunID, &s.Actor, &s.Kind, &s.Content, &s.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	corrections, err := readCorrections(ctx, db)
	if err != nil {
		return nil, err
	}
	runs, err := correctionEventRuns(ctx, db, corrections)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = correctedSource(items[i], corrections, runs)
	}
	return items, nil
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

func memoryScopeIDs(snapshot worldSnapshot) []string {
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
