package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"gameagent/backend/internal/model"
)

const (
	transportStream    = "stream"
	transportNonStream = "non_stream"
)

type GenerationTransport struct {
	Mode               string `json:"mode"`
	StreamingSupported bool   `json:"streaming_supported"`
}

func (a *App) generationTransport(ctx context.Context, generator model.TextGenerator) (GenerationTransport, error) {
	settings := GenerationTransport{Mode: transportStream}
	capability, ok := generator.(model.TextStreamingProvider)
	settings.StreamingSupported = ok && capability.SupportsTextStreaming()
	err := a.appDB.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key=?`, "generation_transport:"+a.userID).Scan(&settings.Mode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return GenerationTransport{}, err
	}
	if settings.Mode != transportStream && settings.Mode != transportNonStream {
		return GenerationTransport{}, fmt.Errorf("invalid stored generation transport")
	}
	if !settings.StreamingSupported {
		settings.Mode = transportNonStream
	}
	return settings, nil
}

// UpdateGenerationTransport stores a user preference. Each accepted run captures
// it once; model credentials and world data are independent of this setting.
func (a *App) UpdateGenerationTransport(ctx context.Context, mode string) (Status, error) {
	if mode != transportStream && mode != transportNonStream {
		return Status{}, ErrInvalidRequest
	}
	a.modelMu.RLock()
	capability, ok := a.generator.(model.TextStreamingProvider)
	supported := ok && capability.SupportsTextStreaming()
	a.modelMu.RUnlock()
	if mode == transportStream && !supported {
		return Status{}, ErrInvalidRequest
	}
	if _, err := a.appDB.ExecContext(ctx, `INSERT INTO app_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "generation_transport:"+a.userID, mode); err != nil {
		return Status{}, err
	}
	return a.Status(ctx)
}
