package app

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
)

func (a *App) meteredText(ctx context.Context, generator model.TextGenerator, request model.TextRequest, scope turn.ContextScope, report turn.ContextBuildReport) (model.TextResponse, error) {
	id, err := a.beginUsage(ctx, scope, report)
	if err != nil {
		return model.TextResponse{}, err
	}
	started := time.Now()
	progress := a.progressForRun(scope.World, scope.Run)
	if progress != nil {
		if progress.transport != "" {
			streaming := progress.transport == transportStream
			request.Streaming = &streaming
		}
		progress.begin(id, scope)
		observer := request.OnDelta
		request.OnDelta = func(delta model.TextDelta) {
			progress.delta(id, delta)
			if observer != nil {
				observer(delta)
			}
		}
	}
	response, callErr := generator.GenerateText(ctx, request)
	if progress != nil {
		progress.finish(id, callErr)
	}
	diagnostic := response.Diagnostic
	var failure *model.TextCallError
	if errors.As(callErr, &failure) {
		diagnostic = failure.Diagnostic
	}
	if err := a.finishUsage(id, diagnostic, time.Since(started), callErr); err != nil && a.logger != nil {
		a.logger.Printf("story usage persistence failed: usage_id=%d", id)
	}
	if a.logger != nil {
		a.logger.Printf("story model cache: world_id=%q run_id=%q usage_id=%d input_known=%t output_known=%t reasoning_known=%t cache_known=%t cache_hit_tokens=%d cache_miss_tokens=%d", scope.World, scope.Run, id, diagnostic.InputKnown, diagnostic.OutputKnown, diagnostic.ReasoningKnown, diagnostic.CacheKnown, diagnostic.CacheHitTokens, diagnostic.CacheMissTokens)
	}
	return response, callErr
}

const usageSchema = `
CREATE TABLE IF NOT EXISTS model_usage (
 id INTEGER PRIMARY KEY AUTOINCREMENT, user_id TEXT NOT NULL,
 world_id TEXT NOT NULL, run_id TEXT NOT NULL, attempt INTEGER NOT NULL,
 purpose TEXT NOT NULL, stage INTEGER NOT NULL, started_at TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'unconfirmed', elapsed_ms INTEGER NOT NULL DEFAULT 0,
 provider TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
 input_tokens INTEGER, output_tokens INTEGER, reasoning_tokens INTEGER,
 cache_hit_tokens INTEGER, cache_miss_tokens INTEGER,
 estimated_input_tokens INTEGER NOT NULL, output_limit INTEGER NOT NULL,
 error_code TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS model_usage_owner ON model_usage(user_id, id);
CREATE INDEX IF NOT EXISTS model_usage_world ON model_usage(user_id, world_id, id);
`

type UsageCall struct {
	ID                   int64  `json:"id"`
	WorldID              string `json:"world_id"`
	RunID                string `json:"run_id"`
	Attempt              int    `json:"attempt"`
	Purpose              string `json:"purpose"`
	Stage                int    `json:"stage"`
	StartedAt            string `json:"started_at"`
	Status               string `json:"status"`
	ElapsedMS            int64  `json:"elapsed_ms"`
	Provider             string `json:"provider"`
	Model                string `json:"model"`
	InputTokens          *int64 `json:"input_tokens"`
	OutputTokens         *int64 `json:"output_tokens"`
	ReasoningTokens      *int64 `json:"reasoning_tokens"`
	CacheHitTokens       *int64 `json:"cache_hit_tokens"`
	CacheMissTokens      *int64 `json:"cache_miss_tokens"`
	EstimatedInputTokens int    `json:"estimated_input_tokens"`
	OutputLimit          int    `json:"output_limit"`
	ErrorCode            string `json:"error_code"`
}

type UsageTotals struct {
	Calls           int64    `json:"calls"`
	Unconfirmed     int64    `json:"unconfirmed"`
	Failed          int64    `json:"failed"`
	InputKnown      int64    `json:"input_known"`
	OutputKnown     int64    `json:"output_known"`
	ReasoningKnown  int64    `json:"reasoning_known"`
	CacheKnown      int64    `json:"cache_known"`
	InputTokens     int64    `json:"input_tokens"`
	OutputTokens    int64    `json:"output_tokens"`
	ReasoningTokens int64    `json:"reasoning_tokens"`
	CacheHitTokens  int64    `json:"cache_hit_tokens"`
	CacheMissTokens int64    `json:"cache_miss_tokens"`
	CacheHitRate    *float64 `json:"cache_hit_rate"`
}

type UsagePage struct {
	Totals       UsageTotals `json:"totals"`
	Calls        []UsageCall `json:"calls"`
	NextBeforeID int64       `json:"next_before_id,omitempty"`
}

// Usage lives in the application ledger: copying a story does not duplicate cost.
// Reservations survive process interruption and stay unconfirmed if usage is lost.
func (a *App) beginUsage(ctx context.Context, scope turn.ContextScope, report turn.ContextBuildReport) (int64, error) {
	result, err := a.appDB.ExecContext(ctx, `INSERT INTO model_usage(user_id,world_id,run_id,attempt,purpose,stage,started_at,estimated_input_tokens,output_limit) VALUES(?,?,?,?,?,?,?,?,?)`, a.userID, scope.World, scope.Run, scope.Attempt, scope.Purpose, scope.Stage, time.Now().UTC().Format(time.RFC3339Nano), report.InputTokens, report.TotalOutputTokens)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func usageCounter(value int, known bool) any {
	if !known {
		return nil
	}
	return value
}

func (a *App) finishUsage(id int64, diagnostic model.TextDiagnostic, elapsed time.Duration, callErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status := "returned"
	if callErr != nil {
		status = "failed"
	}
	_, err := a.appDB.ExecContext(ctx, `UPDATE model_usage SET status=?,elapsed_ms=?,provider=?,model=?,input_tokens=?,output_tokens=?,reasoning_tokens=?,cache_hit_tokens=?,cache_miss_tokens=?,error_code=? WHERE id=? AND user_id=?`, status, elapsed.Milliseconds(), diagnostic.Provider, diagnostic.Model,
		usageCounter(diagnostic.InputTokens, diagnostic.InputKnown), usageCounter(diagnostic.OutputTokens, diagnostic.OutputKnown), usageCounter(diagnostic.ReasoningTokens, diagnostic.ReasoningKnown), usageCounter(diagnostic.CacheHitTokens, diagnostic.CacheKnown), usageCounter(diagnostic.CacheMissTokens, diagnostic.CacheKnown), model.TextErrorCode(callErr), id, a.userID)
	return err
}

func (a *App) ReadUsage(ctx context.Context, worldID string, beforeID int64) (UsagePage, error) {
	page := UsagePage{Calls: []UsageCall{}}
	if beforeID < 0 {
		return page, ErrInvalidRequest
	}
	if worldID != "" {
		_, status, err := a.worldRecord(ctx, worldID)
		if err != nil {
			return page, err
		}
		if status != "ready" {
			return page, ErrWorldNotReady
		}
	}
	tx, err := a.appDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	where := `user_id=?`
	args := []any{a.userID}
	if worldID != "" {
		where += ` AND world_id=?`
		args = append(args, worldID)
	}
	t := &page.Totals
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(status='unconfirmed'),0),COALESCE(SUM(status='failed'),0),COUNT(input_tokens),COUNT(output_tokens),COUNT(reasoning_tokens),COUNT(cache_hit_tokens),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(reasoning_tokens),0),COALESCE(SUM(cache_hit_tokens),0),COALESCE(SUM(cache_miss_tokens),0) FROM model_usage WHERE `+where, args...).Scan(&t.Calls, &t.Unconfirmed, &t.Failed, &t.InputKnown, &t.OutputKnown, &t.ReasoningKnown, &t.CacheKnown, &t.InputTokens, &t.OutputTokens, &t.ReasoningTokens, &t.CacheHitTokens, &t.CacheMissTokens)
	if err != nil {
		return page, err
	}
	if total := t.CacheHitTokens + t.CacheMissTokens; total > 0 {
		rate := float64(t.CacheHitTokens) / float64(total)
		t.CacheHitRate = &rate
	}
	if beforeID > 0 {
		where += ` AND id<?`
		args = append(args, beforeID)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,world_id,run_id,attempt,purpose,stage,started_at,status,elapsed_ms,provider,model,input_tokens,output_tokens,reasoning_tokens,cache_hit_tokens,cache_miss_tokens,estimated_input_tokens,output_limit,error_code FROM model_usage WHERE `+where+` ORDER BY id DESC LIMIT 101`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var c UsageCall
		if err := rows.Scan(&c.ID, &c.WorldID, &c.RunID, &c.Attempt, &c.Purpose, &c.Stage, &c.StartedAt, &c.Status, &c.ElapsedMS, &c.Provider, &c.Model, &c.InputTokens, &c.OutputTokens, &c.ReasoningTokens, &c.CacheHitTokens, &c.CacheMissTokens, &c.EstimatedInputTokens, &c.OutputLimit, &c.ErrorCode); err != nil {
			return page, err
		}
		page.Calls = append(page.Calls, c)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Calls) > 100 {
		page.Calls = page.Calls[:100]
		page.NextBeforeID = page.Calls[99].ID
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	return page, tx.Commit()
}
