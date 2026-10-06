package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type SuggestionBasis struct {
	WorldID     string `json:"world_id"`
	MessageHead int64  `json:"message_head"`
	EventHead   int64  `json:"event_head"`
	Epoch       int64  `json:"context_epoch"`
	Revision    string `json:"revision"`
}

type SuggestionSet struct {
	ID      string          `json:"id"`
	Basis   SuggestionBasis `json:"basis"`
	Enabled bool            `json:"enabled"`
	Status  string          `json:"status"`
	Items   []string        `json:"items"`
}

type SuggestionRequest struct {
	Basis                  SuggestionBasis `json:"basis"`
	ExpectedActiveRevision int64           `json:"expected_active_revision"`
	Enabled                *bool           `json:"enabled,omitempty"`
}

func suggestionBasis(s wiaworld.WorldSummary) SuggestionBasis {
	return SuggestionBasis{WorldID: s.WorldID, MessageHead: s.MessageHead, EventHead: s.EventHead, Epoch: s.ContextEpoch, Revision: s.Revision}
}

func readSuggestionSet(ctx context.Context, store *storage.WorldStore, snapshot turn.Snapshot, runtime *worldRuntime) (SuggestionSet, error) {
	out := SuggestionSet{Enabled: true, Status: "empty", Basis: suggestionBasis(snapshot.Summary), Items: []string{}}
	if value, err := store.MetaGet(ctx, "suggestions_enabled"); err == nil {
		if value != "true" && value != "false" {
			return out, ErrStorageUnavailable
		}
		out.Enabled = value == "true"
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if !out.Enabled {
		out.Status = "disabled"
		return out, nil
	}
	if snapshot.Summary.StoryEnded {
		out.Status = "ended"
		return out, nil
	}
	if raw, err := store.MetaGet(ctx, "suggestion_set"); err == nil {
		var saved SuggestionSet
		if err := json.Unmarshal([]byte(raw), &saved); err != nil {
			return out, err
		}
		if saved.Basis == out.Basis {
			out = saved
			out.Enabled = true
			if out.Status == "generating" && runtime.suggestionID != out.ID {
				out.Status = "interrupted"
				out.Items = []string{}
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	return out, nil
}

func (a *App) ReadSuggestions(ctx context.Context, worldID string) (SuggestionSet, error) {
	w := a.worldRuntimeFor(worldID)
	w.mu.Lock()
	defer w.mu.Unlock()
	store, snapshot, err := a.suggestionWorld(ctx, worldID)
	if err != nil {
		return SuggestionSet{}, err
	}
	defer store.Close()
	return readSuggestionSet(ctx, store, snapshot, w)
}

func (a *App) suggestionWorld(ctx context.Context, worldID string) (*storage.WorldStore, turn.Snapshot, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return nil, turn.Snapshot{}, err
	}
	if status != "ready" {
		return nil, turn.Snapshot{}, ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return nil, turn.Snapshot{}, err
	}
	snapshot, err := turn.LoadSnapshot(ctx, store, 20)
	if err != nil {
		store.Close()
		return nil, turn.Snapshot{}, err
	}
	return store, snapshot, nil
}

// One automatic request per committed basis; repeated POSTs reuse its result.
func (a *App) RequestSuggestions(ctx context.Context, worldID string, req SuggestionRequest) (SuggestionSet, error) {
	a.activationMu.Lock()
	defer a.activationMu.Unlock()
	w := a.worldRuntimeFor(worldID)
	w.mu.Lock()
	defer w.mu.Unlock()
	store, snapshot, err := a.suggestionWorld(ctx, worldID)
	if err != nil {
		return SuggestionSet{}, err
	}
	defer store.Close()
	if req.Basis != suggestionBasis(snapshot.Summary) || !a.isActive(ctx, worldID, req.ExpectedActiveRevision) || req.ExpectedActiveRevision <= 0 {
		return SuggestionSet{}, ErrVersionConflict
	}
	if w.savePending {
		return SuggestionSet{}, ErrWorldBusy
	}
	if count, err := store.CountActiveRuns(ctx); err != nil {
		return SuggestionSet{}, err
	} else if count > 0 {
		return SuggestionSet{}, ErrWorldBusy
	}
	set, err := readSuggestionSet(ctx, store, snapshot, w)
	if err != nil {
		return set, err
	}
	if req.Enabled != nil {
		if set.Enabled == *req.Enabled {
			return set, nil
		}
		w.cancelSuggestions()
		if err := store.InTx(ctx, func(tx *storage.WorldTx) error {
			if err := tx.SetMeta(ctx, "suggestions_enabled", fmt.Sprint(*req.Enabled)); err != nil {
				return err
			}
			return tx.DeleteMeta(ctx, "suggestion_set")
		}); err != nil {
			return set, err
		}
		set = SuggestionSet{Enabled: *req.Enabled, Status: "empty", Basis: req.Basis, Items: []string{}}
		if !set.Enabled {
			set.Status = "disabled"
		}
		return set, nil
	}
	if set.Status != "empty" {
		return set, nil
	}
	if err := memory.Ready(ctx, store); err != nil {
		return set, err
	}
	// An uncommitted input never becomes the basis for auxiliary generation.
	if len(snapshot.Messages) == 0 || snapshot.Messages[len(snapshot.Messages)-1].Kind != "narrative" {
		set.Status = "waiting"
		return set, nil
	}
	a.modelMu.RLock()
	generator := a.generator
	a.modelMu.RUnlock()
	if generator == nil {
		return set, ErrModelNotConfigured
	}
	if err := turn.LoadLongMemory(ctx, store, &snapshot); err != nil {
		return set, err
	}
	material := turn.ComposeSuggestions(snapshot)
	a.copyMu.Lock()
	defer a.copyMu.Unlock()
	if a.closing {
		return set, ErrWorldBusy
	}
	w.cancelSuggestions()
	set.ID = wire.NewID("suggestions")
	set.Status = "generating"
	if _, err := store.Database().ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('suggestion_set',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, wire.MarshalJSON(set)); err != nil {
		return set, err
	}
	jobCtx, cancel := context.WithTimeout(a.copyCtx, 20*time.Second)
	w.suggestionID, w.suggestionCancel = set.ID, cancel
	a.copyWG.Add(1)
	go a.generateSuggestions(jobCtx, cancel, generator, snapshot, material, set, req.ExpectedActiveRevision)
	return set, nil
}

func (a *App) generateSuggestions(ctx context.Context, cancel context.CancelFunc, generator model.TextGenerator, snapshot turn.Snapshot, material turn.Material, set SuggestionSet, activeRevision int64) {
	defer a.copyWG.Done()
	defer cancel()
	run := wiaworld.Run{RunID: set.ID, Attempt: 1, BaseContextEpoch: set.Basis.Epoch}
	g := a.contextGenerator(generator, material, snapshot, run, "suggestions", "player", 0, "story.suggestions.v2")
	var result struct {
		Items []string `json:"items"`
	}
	check := func() error {
		if len(result.Items) != 3 {
			return ErrInvalidRequest
		}
		seen := map[string]bool{}
		for i, item := range result.Items {
			item = wire.Clean(item)
			if item == "" || len([]rune(item)) > 120 || seen[item] {
				return ErrInvalidRequest
			}
			seen[item] = true
			result.Items[i] = item
		}
		return nil
	}
	_, err := turn.GenerateJSONCheckedMetrics(ctx, g, material.System, material.Required, &result, 1024, nil, []string{"items"}, check)
	w := a.worldRuntimeFor(set.Basis.WorldID)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.suggestionID != set.ID {
		return
	}
	defer func() { w.suggestionID = ""; w.suggestionCancel = nil }()
	if ctx.Err() != nil || w.savePending {
		return
	}
	if !a.isActive(ctx, set.Basis.WorldID, activeRevision) {
		return
	}
	store, current, readErr := a.suggestionWorld(ctx, set.Basis.WorldID)
	if readErr != nil {
		return
	}
	defer store.Close()
	if suggestionBasis(current.Summary) != set.Basis {
		return
	}
	if count, countErr := store.CountActiveRuns(ctx); countErr != nil || count > 0 {
		return
	}
	if memory.Ready(ctx, store) != nil {
		return
	}
	set.Status = "ready"
	set.Items = result.Items
	if err != nil {
		set.Status = "failed"
		set.Items = []string{}
	}
	_, _ = store.Database().ExecContext(ctx, `UPDATE meta SET value=? WHERE key='suggestion_set'`, wire.MarshalJSON(set))
}

// Called while the world's mutation gate is held; late responses lose publishing rights.
func (w *worldRuntime) cancelSuggestions() {
	if w.suggestionCancel != nil {
		w.suggestionCancel()
	}
	w.suggestionCancel = nil
	w.suggestionID = ""
}
