package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func LoadCharacters(ctx context.Context, db *sql.DB) ([]wiaworld.Character, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.entity_id,c.definition_id,c.name,c.role,c.profile,c.knowledge,c.in_scene,COALESCE(m.value,'') FROM characters c LEFT JOIN meta m ON m.key='initial_concerns:' || c.entity_id ORDER BY c.entity_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []wiaworld.Character
	for rows.Next() {
		var c wiaworld.Character
		var in int
		if err := rows.Scan(&c.EntityID, &c.DefinitionID, &c.Name, &c.Role, &c.Profile, &c.Knowledge, &in, &c.InitialConcerns); err != nil {
			return nil, err
		}
		c.InScene = in != 0
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range result {
		revision, revisionErr := MetaGet(ctx, db, "definition_revision:"+result[i].EntityID)
		if revisionErr != nil && !errors.Is(revisionErr, sql.ErrNoRows) {
			return nil, revisionErr
		}
		result[i].DefinitionRevision = revision
		value, e := MetaGet(ctx, db, "appearance:"+result[i].EntityID)
		if e == nil {
			result[i].Appearance = value
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		// A promoted or authored character can carry its own avatar; the world copy is
		// recorded separately under the world's assets.
		if value, e := MetaGet(ctx, db, "avatar:"+result[i].EntityID); e == nil {
			result[i].Avatar = value
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	return result, nil
}

func LoadMessages(ctx context.Context, db *sql.DB, limit int) ([]wiaworld.Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `SELECT seq,message_id,kind,content,run_id,created_at FROM messages ORDER BY seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []wiaworld.Message
	for rows.Next() {
		var m wiaworld.Message
		var created string
		if err := rows.Scan(&m.Seq, &m.MessageID, &m.Kind, &m.Content, &m.RunID, &created); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, m)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, rows.Err()
}

func LoadEvents(ctx context.Context, db *sql.DB, limit int) ([]wiaworld.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `SELECT seq,event_id,event_type,actor_id,target_id,content,run_id,stage,scene_version,source_type,created_at FROM events ORDER BY seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []wiaworld.Event
	for rows.Next() {
		var e wiaworld.Event
		var created string
		if err := rows.Scan(&e.Seq, &e.EventID, &e.EventType, &e.ActorID, &e.TargetID, &e.Content, &e.RunID, &e.Stage, &e.SceneVersion, &e.SourceType, &created); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, e)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, rows.Err()
}

func LoadPerceptions(ctx context.Context, db *sql.DB, recipient string, limit int) ([]wiaworld.Perception, error) {
	rows, err := db.QueryContext(ctx, `SELECT seq,recipient_id,source_event_id,source_type,content,stage,scene_version,created_at FROM perceptions WHERE recipient_id=? ORDER BY seq DESC LIMIT ?`, recipient, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []wiaworld.Perception
	for rows.Next() {
		var p wiaworld.Perception
		var created string
		if err := rows.Scan(&p.Seq, &p.RecipientID, &p.SourceEventID, &p.SourceType, &p.Content, &p.Stage, &p.SceneVersion, &created); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, p)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, rows.Err()
}

func LoadMemories(ctx context.Context, db *sql.DB, recipient string, limit int) ([]wiaworld.Memory, error) {
	rows, err := db.QueryContext(ctx, `SELECT seq,recipient_id,kind,content,source_event_id,created_at FROM memories WHERE recipient_id=? ORDER BY seq DESC LIMIT ?`, recipient, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []wiaworld.Memory
	for rows.Next() {
		var m wiaworld.Memory
		var created string
		if err := rows.Scan(&m.Seq, &m.RecipientID, &m.Kind, &m.Content, &m.SourceEventID, &created); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, m)
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, rows.Err()
}

// Dialogue is sourced from committed events, never UI messages or failed inputs.
func LoadDialogue(ctx context.Context, db *sql.DB) ([]wiaworld.Event, error) {
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

func ScanRun(row interface{ Scan(...any) error }) (wiaworld.Run, bool, error) {
	var r wiaworld.Run
	var created, updated string
	var foundErr error
	foundErr = row.Scan(&r.RunID, &r.RequestKey, &r.RequestHash, &r.Input, &r.AddresseeID, &r.Attempt, &r.Status, &r.Reason, &r.Error, &r.MessageSeq, &r.InputID, &r.InputSeq, &r.BaseTurnSeq, &r.BaseMessageHead, &r.BaseEventHead, &r.BaseContextEpoch, &r.BaseSceneVersion, &created, &updated)
	if errors.Is(foundErr, sql.ErrNoRows) {
		return wiaworld.Run{}, false, nil
	}
	if foundErr != nil {
		return wiaworld.Run{}, false, foundErr
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return r, true, nil
}

func ReadRun(ctx context.Context, db *sql.DB, runID string) (wiaworld.Run, bool, error) {
	return ScanRun(db.QueryRowContext(ctx, `SELECT run_id,request_key,request_hash,input,addressee_id,attempt,status,reason,error,message_seq,input_id,input_seq,base_turn_seq,base_message_head,base_event_head,base_context_epoch,base_scene_version,created_at,updated_at FROM runs WHERE run_id=?`, runID))
}

func ReadRunByRequest(ctx context.Context, db *sql.DB, key string) (wiaworld.Run, bool, error) {
	return ScanRun(db.QueryRowContext(ctx, `SELECT run_id,request_key,request_hash,input,addressee_id,attempt,status,reason,error,message_seq,input_id,input_seq,base_turn_seq,base_message_head,base_event_head,base_context_epoch,base_scene_version,created_at,updated_at FROM runs WHERE request_key=?`, key))
}

func UpdateRunStatus(ctx context.Context, db *sql.DB, runID, status, reason, errorText string) error {
	_, err := db.ExecContext(ctx, `UPDATE runs SET status=?,reason=?,error=?,updated_at=? WHERE run_id=?`, status, reason, errorText, wire.NowText(), runID)
	return err
}

func RunCancelRequested(ctx context.Context, db *sql.DB, runID string) (bool, error) {
	var value int
	err := db.QueryRowContext(ctx, `SELECT cancel_requested FROM runs WHERE run_id=?`, runID).Scan(&value)
	return value != 0, err
}

func CountActiveRuns(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE status IN ('accepted','running')`).Scan(&count)
	return count, err
}

func MarkRunInterrupted(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `UPDATE runs SET status='interrupted',reason='process_restarted',error='the previous process stopped before completion',updated_at=? WHERE status IN ('accepted','running')`, wire.NowText())
	return err
}
