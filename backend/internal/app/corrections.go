package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
)

var ErrMemoryRebuilding = memory.ErrRebuilding

func (a *App) Correct(ctx context.Context, worldID string, request memory.CorrectionRequest) (memory.Correction, error) {
	schedule := false
	defer func() {
		if schedule {
			a.startMemoryRebuild(worldID)
		}
	}()
	if wire.Clean(request.RequestKey) == "" || len(request.RequestKey) > 200 || request.ExpectedEpoch < 1 || wire.Clean(request.Replacement) == "" || len([]rune(request.Replacement)) > 8000 {
		return memory.Correction{}, ErrInvalidRequest
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return memory.Correction{}, err
	}
	if status != "ready" {
		return memory.Correction{}, ErrWorldNotReady
	}
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	path, status, err = a.worldRecord(ctx, worldID)
	if err != nil {
		return memory.Correction{}, err
	}
	if status != "ready" {
		return memory.Correction{}, ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return memory.Correction{}, err
	}
	defer store.Close()
	hash := wire.MarshalJSON(request)
	var oldHash string
	err = store.Database().QueryRowContext(ctx, `SELECT request_hash FROM corrections WHERE request_key=?`, request.RequestKey).Scan(&oldHash)
	if err == nil {
		if hash != oldHash {
			return memory.Correction{}, ErrIdempotencyConflict
		}
		list, e := memory.ReadCorrections(ctx, store)
		if e != nil {
			return memory.Correction{}, e
		}
		for _, c := range list {
			if c.Epoch == request.ExpectedEpoch+1 {
				return c, nil
			}
		}
		return memory.Correction{}, ErrStorageUnavailable
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return memory.Correction{}, err
	}
	if worldRT.savePending {
		return memory.Correction{}, ErrWorldBusy
	}
	if count, e := store.CountActiveRuns(ctx); e != nil {
		return memory.Correction{}, e
	} else if count > 0 {
		return memory.Correction{}, ErrWorldBusy
	}
	snapshot, err := turn.LoadSnapshot(ctx, store, 100)
	if err != nil {
		return memory.Correction{}, err
	}
	if snapshot.Summary.ContextEpoch != request.ExpectedEpoch {
		return memory.Correction{}, ErrVersionConflict
	}
	if err = turn.LoadLongMemory(ctx, store, &snapshot); err != nil {
		return memory.Correction{}, err
	}
	original, err := correctionOriginal(ctx, store, snapshot, request)
	if err != nil {
		return memory.Correction{}, err
	}
	c := memory.Correction{Epoch: request.ExpectedEpoch + 1, Kind: request.Kind, Scope: request.Scope, TargetID: request.TargetID, Original: original, Replacement: wire.Clean(request.Replacement), CreatedAt: wire.NowText()}
	c.SceneVersion = snapshot.SceneVersion
	if err = memory.ExpandCorrection(ctx, store, &c); err != nil {
		return c, err
	}
	if c.Kind == "event" && snapshot.Definition.Capabilities["spatial"] == 1 {
		sources, sourceErr := store.LoadEntityLocationSources(ctx)
		if sourceErr != nil {
			return c, sourceErr
		}
		for _, sourceEventID := range sources {
			if c.Affects(sourceEventID) {
				return c, fmt.Errorf("%w: correction would invalidate a current position; position rebuilding is not available", ErrInvalidRequest)
			}
		}
	}
	if c.Kind == "event" && (snapshot.Definition.Capabilities["state"] == 1 || snapshot.Definition.Capabilities["relations"] == 1 || snapshot.Definition.Capabilities["items"] == 1 || len(snapshot.Definition.ActionRules) > 0) {
		sources, sourceErr := store.LoadStructuredFactSources(ctx)
		if sourceErr != nil {
			return c, sourceErr
		}
		for _, sourceEventID := range sources {
			if sourceEventID != "opening" && c.Affects(sourceEventID) {
				return c, fmt.Errorf("%w: correction would invalidate a current structured fact; rebuilding is not available", ErrInvalidRequest)
			}
		}
	}
	var eventRun string
	if c.Kind == "event" {
		if err = store.Database().QueryRowContext(ctx, `SELECT run_id FROM events WHERE event_id=?`, c.TargetID).Scan(&eventRun); err != nil {
			return c, err
		}
	}
	// This is a business transaction, not a persistence primitive: a correction, the
	// epoch it advances, the rebuilds it invalidates, the notices it writes into each
	// scope's stream, and the new rebuild job it queues all have to land together or
	// the world would believe something was corrected without agreeing on which.
	if err = store.InTx(ctx, func(tx *storage.WorldTx) error {
		if err := tx.InsertCorrection(ctx, storage.CorrectionWrite{
			Epoch: c.Epoch, RequestKey: request.RequestKey, RequestHash: hash, Kind: c.Kind,
			Scope: c.Scope, TargetID: c.TargetID, Original: c.Original, Replacement: c.Replacement,
			CreatedAt: c.CreatedAt, SceneVersion: c.SceneVersion,
		}); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "context_epoch", fmt.Sprint(c.Epoch)); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "updated_at", c.CreatedAt); err != nil {
			return err
		}
		if err := tx.SupersedeOpenMemoryJobs(ctx); err != nil {
			return err
		}
		for scope, content := range correctionNotices(snapshot, c, eventRun) {
			if err := memory.AppendSource(ctx, tx, storage.MemorySourceWrite{
				Scope:     scope,
				ID:        fmt.Sprintf("correction:%d", c.Epoch),
				Actor:     "author",
				Kind:      "correction:" + c.Kind,
				Content:   content,
				CreatedAt: c.CreatedAt,
			}); err != nil {
				return err
			}
		}
		return tx.InsertMemoryJob(ctx, storage.MemoryJobWrite{
			Epoch: c.Epoch, Scopes: wire.MarshalJSON(memory.ScopeIDs(snapshot.Characters)), CreatedAt: c.CreatedAt,
		})
	}); err != nil {
		return c, err
	}
	schedule = true
	return c, nil
}

func correctionNotices(snapshot turn.Snapshot, c memory.Correction, eventRun string) map[string]string {
	if c.Kind == "perception" || c.Kind == "subjective" {
		return map[string]string{c.Scope: c.Replacement}
	}
	notices := map[string]string{}
	if c.Kind != "event" {
		return notices
	}
	for scope, m := range snapshot.LongMemory {
		for _, s := range m.Archive {
			if !c.Affects(s.EventID) && !(scope == "player" && s.RunID != "" && s.RunID == eventRun) {
				continue
			}
			if s.Content == c.Original {
				notices[scope] = c.Replacement
				break
			}
			notices[scope] = memory.Notice(c, s.ID)
		}
	}
	return notices
}

func correctionOriginal(ctx context.Context, store *storage.WorldStore, s turn.Snapshot, r memory.CorrectionRequest) (string, error) {
	switch r.Kind {
	case "event":
		if r.Scope != "author" {
			return "", ErrInvalidRequest
		}
		var content string
		err := store.Database().QueryRowContext(ctx, `SELECT content FROM events e WHERE event_id=? AND (event_id='opening' OR run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed'))`, r.TargetID).Scan(&content)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidRequest
		}
		if err != nil {
			return "", err
		}
		list, err := memory.ReadCorrections(ctx, store)
		if err != nil {
			return "", err
		}
		for _, c := range list {
			if c.Kind == "event" && c.TargetID == r.TargetID {
				content = c.Replacement
			}
		}
		return content, nil
	case "character":
		for _, c := range s.Characters {
			if c.EntityID != r.Scope {
				continue
			}
			switch r.TargetID {
			case "profile":
				return c.Profile, nil
			case "knowledge":
				return c.Knowledge, nil
			case "initial_concerns":
				return c.InitialConcerns, nil
			}
		}
	case "perception", "subjective":
		m, ok := s.LongMemory[r.Scope]
		if !ok || r.Scope == "player" {
			return "", ErrInvalidRequest
		}
		for _, item := range m.Archive {
			if item.ID == r.TargetID && ((r.Kind == "perception" && strings.HasPrefix(item.Kind, "perception:")) || (r.Kind == "subjective" && strings.HasPrefix(item.Kind, "subjective:"))) {
				return item.Content, nil
			}
		}
		if r.Kind == "subjective" {
			for i, state := range m.Digest.States {
				if r.TargetID == fmt.Sprintf("state:%d:%d", m.Digest.Revision, i) {
					return state.Content, nil
				}
			}
		}
	case "digest":
		m, ok := s.LongMemory[r.Scope]
		if ok && m.Digest.Revision > 0 && r.TargetID == fmt.Sprint(m.Digest.Revision) {
			return m.Digest.Content, nil
		}
	}
	return "", ErrInvalidRequest
}

// correctionNotices reports the stream entries a correction writes for each scope.
