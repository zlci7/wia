package memory

import (
	"context"
	"errors"

	"gameagent/backend/internal/session"
)

var ErrInvalidRecord = errors.New("invalid memory record")

type Store interface {
	// Append 在 record.SessionKey 对应的 AgentSession 下写入一条 MemoryRecord。
	Append(ctx context.Context, record Record) error

	// Recent 返回当前 AgentSession 最近的 MemoryRecord，顺序从旧到新。
	Recent(ctx context.Context, key session.AgentSessionKey, limit int) ([]Record, error)
}
