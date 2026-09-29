package storyapp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"gameagent/backend/internal/turn"
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

func dialogueContext(snapshot turn.Snapshot) string {
	var result strings.Builder
	for _, event := range snapshot.Dialogue {
		fmt.Fprintf(&result, "[来源=%s；回合=%s；类型=%s；表达者=%s；对象=%s；范围=%s] %s\n", event.EventID, event.RunID, event.EventType, turn.CharacterDisplayName(snapshot.Characters, event.ActorID), event.TargetID, event.SourceType, event.Content)
	}
	return result.String()
}

const intentVisibilityRule = "交谈对象与可听范围分别判断。指定某人、使用引号或试探语气均不单独构成私聊依据。未表达私密意图且没有已成立私密情境时，visibility 为 public。明确耳语、限定听众或有来源的近期私密交谈情境可支持 private；当前明确表达优先。历史发言用于理解省略和指代，不是本轮再次发生的动作。"
