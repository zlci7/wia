package storyapp

import (
	"context"
	"database/sql"

	wiaworld "gameagent/backend/internal/world"
)

// Dialogue is sourced from committed events, never UI messages or failed inputs.
func loadDialogue(ctx context.Context, db *sql.DB) ([]wiaworld.Event, error) {
	rows, err := db.QueryContext(ctx, `SELECT e.event_id,e.event_type,e.actor_id,e.target_id,e.content,e.run_id,
	CASE WHEN EXISTS(SELECT 1 FROM perceptions p WHERE p.source_event_id=e.event_id AND p.source_type IN ('direct_private_message','observed_private_conversation')) THEN 'private' ELSE 'public' END
	FROM events e WHERE e.run_id IN (SELECT run_id FROM runs WHERE status='completed' ORDER BY input_seq DESC,created_at DESC LIMIT 4)
	AND e.event_type IN ('player_attempt','npc_dialogue') AND e.source_type!='offscene_dialogue' ORDER BY e.seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []wiaworld.Event
	for rows.Next() {
		var event wiaworld.Event
		if err := rows.Scan(&event.EventID, &event.EventType, &event.ActorID, &event.TargetID, &event.Content, &event.RunID, &event.SourceType); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
