package memory

import (
	"context"
	"strings"
	"sync"

	"gameagent/backend/internal/session"
)

const DefaultMaxRecordsPerSession = 20

type InMemoryStore struct {
	mu                   sync.RWMutex
	maxRecordsPerSession int
	records              map[session.AgentSessionKey][]Record
}

// NewInMemoryStore 创建默认的进程内 MemoryStore。
// Phase4 先使用 InMemory backend，验证 AgentSession 级短期记忆链路。
func NewInMemoryStore() *InMemoryStore {
	return NewInMemoryStoreWithMaxRecords(DefaultMaxRecordsPerSession)
}

// NewInMemoryStoreWithMaxRecords 创建带保留上限的 InMemoryStore。
// 上限用于防止默认开启 Memory 后，长时间游戏让进程内历史无限增长。
func NewInMemoryStoreWithMaxRecords(maxRecordsPerSession int) *InMemoryStore {
	if maxRecordsPerSession <= 0 {
		maxRecordsPerSession = DefaultMaxRecordsPerSession
	}
	return &InMemoryStore{
		maxRecordsPerSession: maxRecordsPerSession,
		records:              make(map[session.AgentSessionKey][]Record),
	}
}

// Append 写入一条 AgentSession-scoped MemoryRecord。
// 超过保留上限时淘汰最旧记录，只保留最近的短期 Memory。
func (s *InMemoryStore) Append(ctx context.Context, record Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(record.MemoryID) == "" {
		return ErrInvalidRecord
	}
	if record.SessionKey.GameID == "" || record.SessionKey.WorldID == "" || record.SessionKey.EntityID == "" {
		return ErrInvalidRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records := append(s.records[record.SessionKey], record)
	if len(records) > s.maxRecordsPerSession {
		records = append([]Record(nil), records[len(records)-s.maxRecordsPerSession:]...)
	}
	s.records[record.SessionKey] = records
	return nil
}

// Recent 读取当前 AgentSession 最近的 MemoryRecord。
// 返回顺序保持从旧到新，便于 Renderer 按时间顺序注入 prompt。
func (s *InMemoryStore) Recent(ctx context.Context, key session.AgentSessionKey, limit int) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	records := s.records[key]
	if len(records) == 0 {
		return nil, nil
	}

	start := len(records) - limit
	if start < 0 {
		start = 0
	}

	out := make([]Record, len(records[start:]))
	copy(out, records[start:])
	return out, nil
}
