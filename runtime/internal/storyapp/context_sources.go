package storyapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrContextSourceMissing = errors.New("context source is missing")

// No event body crosses this metadata lookup. Perception content remains authoritative.
type sourceMetadata struct {
	ID           string
	Actor        string
	Kind         string
	Seq          int64
	RunID        string
	Stage        int
	SceneVersion int64
}

func loadSourceMetadata(ctx context.Context, db *sql.DB, snapshot worldSnapshot) (map[string]sourceMetadata, error) {
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
	result := map[string]sourceMetadata{}
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
			var m sourceMetadata
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
