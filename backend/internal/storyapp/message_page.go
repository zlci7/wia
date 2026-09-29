package storyapp

import (
	"context"
	"time"
)

type MessagePageRequest struct {
	Limit     int
	BeforeSeq *int64
	AfterSeq  *int64
}

type MessagePage struct {
	Messages      []Message `json:"messages"`
	HasMore       bool      `json:"has_more"`
	NextBeforeSeq *int64    `json:"next_before_seq,omitempty"`
	NextAfterSeq  *int64    `json:"next_after_seq,omitempty"`
}

func (a *App) ReadMessagePage(ctx context.Context, worldID string, request MessagePageRequest) (MessagePage, error) {
	if request.Limit == 0 {
		request.Limit = 100
	}
	if request.Limit < 1 || request.Limit > 200 || (request.BeforeSeq != nil && request.AfterSeq != nil) || (request.BeforeSeq != nil && *request.BeforeSeq <= 0) || (request.AfterSeq != nil && *request.AfterSeq < 0) {
		return MessagePage{}, ErrInvalidRequest
	}
	world := a.worldRuntimeFor(worldID)
	world.mu.Lock()
	defer world.mu.Unlock()
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return MessagePage{}, err
	}
	if status != "ready" {
		return MessagePage{}, ErrWorldNotReady
	}
	store, err := openWorldDB(path)
	if err != nil {
		return MessagePage{}, err
	}
	defer store.db.Close()
	query := `SELECT seq,message_id,kind,content,run_id,created_at FROM messages`
	args := []any{}
	if request.BeforeSeq != nil {
		query += ` WHERE seq < ?`
		args = append(args, *request.BeforeSeq)
	}
	if request.AfterSeq != nil {
		query += ` WHERE seq > ?`
		args = append(args, *request.AfterSeq)
	}
	if request.AfterSeq != nil {
		query += ` ORDER BY seq ASC LIMIT ?`
	} else {
		query += ` ORDER BY seq DESC LIMIT ?`
	}
	args = append(args, request.Limit+1)
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return MessagePage{}, err
	}
	defer rows.Close()
	page := MessagePage{Messages: []Message{}}
	for rows.Next() {
		var m Message
		var created string
		if err := rows.Scan(&m.Seq, &m.MessageID, &m.Kind, &m.Content, &m.RunID, &created); err != nil {
			return MessagePage{}, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		page.Messages = append(page.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return MessagePage{}, err
	}
	page.HasMore = len(page.Messages) > request.Limit
	if page.HasMore {
		page.Messages = page.Messages[:request.Limit]
	}
	if request.AfterSeq == nil {
		for i, j := 0, len(page.Messages)-1; i < j; i, j = i+1, j-1 {
			page.Messages[i], page.Messages[j] = page.Messages[j], page.Messages[i]
		}
	}
	if page.HasMore {
		if request.AfterSeq != nil {
			seq := page.Messages[len(page.Messages)-1].Seq
			page.NextAfterSeq = &seq
		} else {
			seq := page.Messages[0].Seq
			page.NextBeforeSeq = &seq
		}
	}
	return page, nil
}
