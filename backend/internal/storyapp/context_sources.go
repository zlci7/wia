package storyapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
)

var ErrContextSourceMissing = errors.New("context source is missing")

// No event body crosses this metadata lookup. Perception content remains authoritative.

func loadSourceMetadata(ctx context.Context, db *sql.DB, snapshot turn.Snapshot) (map[string]turn.SourceMetadata, error) {
	ids := []string{}
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, items := range snapshot.Perceptions {
		for _, p := range items {
			add(p.SourceEventID)
		}
	}
	for _, items := range snapshot.Memories {
		for _, m := range items {
			add(m.SourceEventID)
		}
	}
	for _, view := range snapshot.SceneViews {
		for _, id := range view.SourceIDs {
			add(id)
		}
	}
	for _, node := range snapshot.PlotProgress.Nodes {
		add(node.EventID)
		for _, id := range node.Evidence {
			if !strings.HasPrefix(id, "definition:") {
				add(id)
			}
		}
	}
	result := map[string]turn.SourceMetadata{}
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		args := make([]any, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, id)
		}
		rows, err := db.QueryContext(ctx, `SELECT event_id,actor_id,event_type,seq,run_id,stage,scene_version FROM events WHERE event_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m turn.SourceMetadata
			if err := rows.Scan(&m.ID, &m.Actor, &m.Kind, &m.Seq, &m.RunID, &m.Stage, &m.SceneVersion); err != nil {
				rows.Close()
				return nil, err
			}
			result[m.ID] = m
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(result) != len(ids) {
		return nil, fmt.Errorf("%w: %d references unresolved", ErrContextSourceMissing, len(ids)-len(result))
	}
	return result, nil
}

// loadTurnInput builds the turn's frozen input: the world snapshot plus the provenance
// of every projection in it, so a stage can tell where a fact came from without
// re-reading the event it was derived from.
func loadTurnInput(ctx context.Context, store *storage.WorldStore, limit int) (turn.Snapshot, error) {
	snapshot, err := loadTurnSnapshot(ctx, store, limit)
	if err != nil {
		return snapshot, err
	}
	snapshot.Sources, err = loadSourceMetadata(ctx, store.Database(), snapshot)
	return snapshot, err
}
