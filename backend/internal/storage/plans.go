package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	wiaworld "gameagent/backend/internal/world"
)

func (s *WorldStore) LoadOpenProgress(ctx context.Context) (*wiaworld.OpenProgress, error) {
	text, err := s.MetaGet(ctx, "open_progress")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var progress wiaworld.OpenProgress
	if err := json.Unmarshal([]byte(text), &progress); err != nil {
		return nil, err
	}
	return &progress, nil
}

func (t *WorldTx) SetOpenProgress(ctx context.Context, progress *wiaworld.OpenProgress) error {
	text, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	return t.SetMeta(ctx, "open_progress", string(text))
}
