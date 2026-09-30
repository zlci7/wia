package storyapp

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func classifyTurnFailure(err error) (status, reason, message string) {
	if errors.Is(err, turn.ErrContextCapacity) || errors.Is(err, model.ErrTextInputTooLarge) {
		return "failed", "context_capacity_exceeded", "required story context exceeds available model capacity"
	}
	if errors.Is(err, turn.ErrContextSourceMissing) {
		return "failed", "context_source_missing", "story context references are incomplete"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled", "cancelled", "the turn was cancelled"
	}
	if errors.Is(err, ErrVersionConflict) {
		return "failed", "version_conflict", "the world changed before this turn could be saved"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "failed", "generation_timeout", "the model response timed out"
	}
	switch model.TextErrorCode(err) {
	case "network":
		return "failed", "model_connection_failed", "the model connection failed"
	case "provider_http":
		return "failed", "model_service_failed", "the model service rejected the request"
	case "empty_response":
		return "failed", "model_empty_response", "the model returned no usable text"
	case "output_incomplete", "output_limit":
		return "failed", "model_output_incomplete", "the model output was incomplete"
	case "invalid_response":
		return "failed", "model_invalid_response", "the model response was invalid"
	case "timeout":
		return "failed", "generation_timeout", "the model response timed out"
	}
	if errors.Is(err, ErrModelNotConfigured) {
		return "failed", "model_not_configured", "the model connection is unavailable"
	}
	var staged *turn.StageError
	if errors.As(err, &staged) {
		switch staged.Stage {
		case turn.StageLoad, turn.StageCommit:
			return "failed", "storage_unavailable", "the turn could not access its save data"
		case turn.StageIntent:
			return "failed", "intent_generation_failed", "the player intent could not be understood"
		case turn.StageNPC:
			return "failed", "npc_generation_failed", "one or more characters could not respond"
		case turn.StageCoordination:
			return "failed", "coordination_generation_failed", "the scene outcome could not be resolved"
		case turn.StageNarration:
			return "failed", "narration_generation_failed", "the story response could not be written"
		}
	}
	return "failed", "generation_failed", "the turn did not complete"
}

func (a *App) SubmitRun(ctx context.Context, worldID string, request RunRequest) (wiaworld.Run, error) {
	request.Input = wire.Clean(request.Input)
	request.RequestKey = strings.TrimSpace(request.RequestKey)
	request.AddresseeID = wire.Clean(request.AddresseeID)
	if request.RequestKey == "" || request.Input == "" || len([]rune(request.Input)) > 4000 {
		return wiaworld.Run{}, ErrInvalidRequest
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return wiaworld.Run{}, err
	}
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return wiaworld.Run{}, err
	}
	defer store.Close()
	if existing, found, err := store.ReadRunByRequest(ctx, request.RequestKey); err != nil {
		return wiaworld.Run{}, err
	} else if found {
		if existing.RequestHash != a.hashRun(request) {
			return wiaworld.Run{}, ErrIdempotencyConflict
		}
		return existing, nil
	}
	activeID, activeRevision, err := a.activeWorld(ctx)
	if err != nil || activeID != worldID {
		return wiaworld.Run{}, ErrVersionConflict
	}
	if request.ExpectedActiveRevision > 0 && request.ExpectedActiveRevision != activeRevision {
		return wiaworld.Run{}, ErrVersionConflict
	}
	if status != "ready" {
		return wiaworld.Run{}, ErrWorldNotReady
	}
	a.modelMu.RLock()
	generator := a.generator
	a.modelMu.RUnlock()
	if generator == nil {
		return wiaworld.Run{}, ErrModelNotConfigured
	}
	if worldRT.savePending {
		return wiaworld.Run{}, ErrWorldBusy
	}
	if err := memoryReady(ctx, store); err != nil {
		return wiaworld.Run{}, err
	}
	snapshot, err := loadTurnSnapshot(ctx, store, 1)
	if err != nil {
		return wiaworld.Run{}, err
	}
	if snapshot.Summary.StoryEnded {
		return wiaworld.Run{}, ErrStoryEnded
	}
	if (request.requireBaseline || request.ExpectedMessageHead > 0) && request.ExpectedMessageHead != snapshot.Summary.MessageHead {
		return wiaworld.Run{}, ErrVersionConflict
	}
	if (request.requireBaseline || request.ExpectedEventHead > 0) && request.ExpectedEventHead != snapshot.Summary.EventHead {
		return wiaworld.Run{}, ErrVersionConflict
	}
	if (request.requireBaseline || request.ExpectedContextEpoch > 0) && request.ExpectedContextEpoch != snapshot.Summary.ContextEpoch {
		return wiaworld.Run{}, ErrVersionConflict
	}
	if request.requireBaseline && (request.expectedTurnSeq != snapshot.Summary.TurnSeq || request.expectedSceneVersion != snapshot.SceneVersion) {
		return wiaworld.Run{}, ErrVersionConflict
	}
	if request.AddresseeID != "" {
		if _, ok := turn.FindSceneCharacter(snapshot.Characters, request.AddresseeID); !ok {
			return wiaworld.Run{}, ErrInvalidRequest
		}
	}
	if count, err := store.CountActiveRuns(ctx); err != nil {
		return wiaworld.Run{}, err
	} else if count > 0 {
		return wiaworld.Run{}, ErrWorldBusy
	}
	now := time.Now().UTC()
	attempt := request.attempt
	if attempt < 1 {
		attempt = 1
	}
	run, err := a.submitRunTx(ctx, store, snapshot, request, attempt, now)
	if err != nil {
		return wiaworld.Run{}, err
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	worldRT.cancelSuggestions()
	runtime := &runRuntime{Cancel: cancel, Done: make(chan struct{}), WorldID: worldID, RunID: run.RunID, ActiveRevision: activeRevision, Generator: generator}
	a.runsMu.Lock()
	a.runs[run.RunID] = runtime
	a.runsMu.Unlock()
	go a.runWorker(runCtx, runtime, run)
	return run, nil
}

// submitRunTx accepts one run: it confirms the caller's input position, takes the next
// sequence if this is a new input, and records the run.
//
// The whole thing is one transaction because an accepted run and the input sequence it
// consumed must agree; a reader that saw the run without the sequence, or the reverse,
// would have the wrong idea of what the world has already taken in.
func (a *App) submitRunTx(ctx context.Context, store *storage.WorldStore, snapshot turn.Snapshot, request RunRequest, attempt int, now time.Time) (wiaworld.Run, error) {
	var accepted wiaworld.Run
	err := store.InTx(ctx, func(tx *storage.WorldTx) error {
		inputID := wire.Clean(request.inputID)
		inputSeq := request.inputSeq
		if inputSeq > 0 {
			if inputID == "" {
				return ErrVersionConflict
			}
			latest, err := tx.MaxRunInputSequence(ctx)
			if err != nil {
				return err
			}
			if latest != inputSeq {
				return ErrVersionConflict
			}
			completed, err := tx.CountCompletedRunsWithInput(ctx, inputID)
			if err != nil {
				return err
			}
			if completed > 0 {
				return ErrVersionConflict
			}
		} else {
			value, err := tx.GetMeta(ctx, "input_seq")
			if err != nil {
				return err
			}
			inputSeq, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			inputSeq++
			inputID = wire.NewID("input")
			if err := tx.SetMeta(ctx, "input_seq", strconv.FormatInt(inputSeq, 10)); err != nil {
				return err
			}
		}
		run := wiaworld.Run{
			RunID: wire.NewID("run"), RequestKey: request.RequestKey, RequestHash: a.hashRun(request),
			Input: request.Input, AddresseeID: wire.Clean(request.AddresseeID), Attempt: attempt,
			Status: "accepted", CreatedAt: now, UpdatedAt: now,
			InputID: inputID, InputSeq: inputSeq,
			BaseTurnSeq: snapshot.Summary.TurnSeq, BaseMessageHead: snapshot.Summary.MessageHead,
			BaseEventHead: snapshot.Summary.EventHead, BaseContextEpoch: snapshot.Summary.ContextEpoch,
			BaseSceneVersion: snapshot.SceneVersion,
		}
		if err := tx.InsertRun(ctx, storage.RunWrite{
			RunID: run.RunID, RequestKey: run.RequestKey, RequestHash: run.RequestHash, Input: run.Input,
			AddresseeID: run.AddresseeID, Attempt: run.Attempt, Status: run.Status,
			InputID: run.InputID, InputSeq: run.InputSeq,
			BaseTurnSeq: run.BaseTurnSeq, BaseMessageHead: run.BaseMessageHead, BaseEventHead: run.BaseEventHead,
			BaseContextEpoch: run.BaseContextEpoch, BaseSceneVersion: run.BaseSceneVersion,
			CreatedAt: run.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: run.UpdatedAt.Format(time.RFC3339Nano),
		}); err != nil {
			return err
		}
		accepted = run
		return nil
	})
	if err != nil {
		return wiaworld.Run{}, err
	}
	return accepted, nil
}

func (a *App) runWorker(ctx context.Context, runtime *runRuntime, run wiaworld.Run) {
	defer close(runtime.Done)
	defer func() {
		a.runsMu.Lock()
		delete(a.runs, run.RunID)
		a.runsMu.Unlock()
	}()

	path, _, err := a.worldRecord(context.Background(), runtime.WorldID)
	if err != nil {
		a.logRunFailure(runtime.WorldID, run, "open_world", "storage_unavailable", err)
		return
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		a.logRunFailure(runtime.WorldID, run, "open_world", "storage_unavailable", err)
		return
	}
	defer store.Close()
	if err := store.UpdateRunStatus(context.Background(), run.RunID, "running", "", ""); err != nil {
		a.logRunFailure(runtime.WorldID, run, "mark_running", "storage_unavailable", err)
		return
	}
	if !a.isActive(context.Background(), runtime.WorldID, runtime.ActiveRevision) {
		_ = store.UpdateRunStatus(context.Background(), run.RunID, "cancelled", "world_switched", "the active world changed")
		return
	}
	output, err := a.turnService().Execute(ctx, store, run, runtime.Generator)
	if err != nil {
		status, reason, message := classifyTurnFailure(err)
		if status == "failed" {
			a.logRunFailure(runtime.WorldID, run, turn.StageOf(err), reason, err)
		}
		if updateErr := store.UpdateRunStatus(context.Background(), run.RunID, status, reason, message); updateErr != nil {
			a.logRunFailure(runtime.WorldID, run, "record_failure", "storage_unavailable", updateErr)
		}
		return
	}
	if !a.isActive(context.Background(), runtime.WorldID, runtime.ActiveRevision) {
		_ = store.UpdateRunStatus(context.Background(), run.RunID, "cancelled", "world_switched", "the active world changed")
		return
	}
	commitStarted := time.Now()
	if _, err := commitTurn(ctx, store, run, output.Narrative, output.Events, output.Perceptions, output.Memories, output.Clock, output.Scene, output.SceneLocation, output.SceneVersion, output.SceneCharacters, output.SceneViews, output.PlotProgress, output.GeneratedEvents); err != nil {
		if a.logger != nil {
			a.logger.Printf("story commit rejected: world_id=%q run_id=%q detail=%q", runtime.WorldID, run.RunID, err.Error())
		}
		err = turn.AtStage(turn.StageCommit, err)
		status, reason, message := classifyTurnFailure(err)
		if status == "failed" {
			a.logRunFailure(runtime.WorldID, run, string(turn.StageCommit), reason, err)
		}
		if updateErr := store.UpdateRunStatus(context.Background(), run.RunID, status, reason, message); updateErr != nil {
			a.logRunFailure(runtime.WorldID, run, "record_failure", "storage_unavailable", updateErr)
		}
		return
	}
	a.logRunStage(runtime.WorldID, run, turn.StageCommit, "atomic_commit", "", 0, "", nil, "", 0, time.Since(commitStarted))
	_ = a.touchWorld(context.Background(), runtime.WorldID)
}

func (a *App) logRunFailure(worldID string, run wiaworld.Run, stage, reason string, err error) {
	if a.logger == nil || err == nil {
		return
	}
	a.logger.Printf("story turn failed: world_id=%q run_id=%q attempt=%d stage=%q reason=%q error_code=%q", worldID, run.RunID, run.Attempt, stage, reason, safeTurnErrorCode(err))
}

func (a *App) logRunStage(worldID string, run wiaworld.Run, stage turn.Stage, purpose, actorID string, stageIndex int, promptVersion string, sourceEventIDs []string, resolvedAddressee string, repairCount int, elapsed time.Duration) {
	if a.logger == nil {
		return
	}
	a.modelMu.RLock()
	modelInfo := a.modelInfo
	a.modelMu.RUnlock()
	a.logger.Printf("story turn stage completed: world_id=%q run_id=%q attempt=%d stage=%q purpose=%q actor_id=%q stage_index=%d explicit_addressee_id=%q resolved_addressee_id=%q source_event_ids=%q prompt_version=%q provider=%q model=%q repair_count=%d elapsed_ms=%d",
		worldID, run.RunID, run.Attempt, stage, purpose, actorID, stageIndex, run.AddresseeID, resolvedAddressee,
		strings.Join(sourceEventIDs, ","), promptVersion, modelInfo.Provider, modelInfo.Model, repairCount, elapsed.Milliseconds())
}

func (a *App) Run(ctx context.Context, worldID, runID string) (wiaworld.Run, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return wiaworld.Run{}, err
	}
	if status != "ready" {
		return wiaworld.Run{}, ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return wiaworld.Run{}, err
	}
	defer store.Close()
	run, found, err := store.ReadRun(ctx, runID)
	if err != nil {
		return wiaworld.Run{}, err
	}
	if !found {
		return wiaworld.Run{}, ErrRunNotFound
	}
	return run, nil
}

func (a *App) ListRuns(ctx context.Context, worldID string, requestKeys ...string) ([]wiaworld.Run, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return nil, err
	}
	if status != "ready" {
		return nil, ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if len(requestKeys) > 0 && requestKeys[0] != "" {
		run, found, err := store.ReadRunByRequest(ctx, requestKeys[0])
		if err != nil {
			return nil, err
		}
		if !found {
			return []wiaworld.Run{}, nil
		}
		return []wiaworld.Run{run}, nil
	}
	rows, err := store.Database().QueryContext(ctx, `SELECT run_id,request_key,request_hash,input,addressee_id,attempt,status,reason,error,message_seq,input_id,input_seq,base_turn_seq,base_message_head,base_event_head,base_context_epoch,base_scene_version,created_at,updated_at FROM runs ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]wiaworld.Run, 0)
	for rows.Next() {
		run, found, err := storage.ScanRun(rows)
		if err != nil {
			return nil, err
		}
		if found {
			runs = append(runs, run)
		}
	}
	return runs, rows.Err()
}

func (a *App) CancelRun(ctx context.Context, worldID, runID string) error {
	path, _, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return err
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return err
	}
	defer store.Close()
	if _, err := store.Database().ExecContext(ctx, `UPDATE runs SET cancel_requested=1,updated_at=? WHERE run_id=? AND status IN ('accepted','running')`, wire.NowText(), runID); err != nil {
		return err
	}
	a.runsMu.Lock()
	runtime := a.runs[runID]
	a.runsMu.Unlock()
	if runtime != nil {
		runtime.Cancel()
	}
	return nil
}

func (a *App) RetryRun(ctx context.Context, worldID, runID, requestKey string) (wiaworld.Run, error) {
	path, _, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return wiaworld.Run{}, err
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return wiaworld.Run{}, err
	}
	run, found, err := store.ReadRun(ctx, runID)
	_ = store.Close()
	if err != nil {
		return wiaworld.Run{}, err
	}
	if !found {
		return wiaworld.Run{}, ErrRunNotFound
	}
	if run.Status != "failed" && run.Status != "cancelled" && run.Status != "interrupted" {
		return wiaworld.Run{}, ErrInvalidRequest
	}
	if run.InputID == "" || run.InputSeq <= 0 {
		return wiaworld.Run{}, ErrVersionConflict
	}
	if strings.TrimSpace(requestKey) == "" {
		requestKey = wire.NewID("retry")
	}
	return a.SubmitRun(ctx, worldID, RunRequest{
		RequestKey: requestKey, Input: run.Input, AddresseeID: run.AddresseeID, attempt: run.Attempt + 1,
		ExpectedMessageHead: run.BaseMessageHead, ExpectedEventHead: run.BaseEventHead,
		ExpectedContextEpoch: run.BaseContextEpoch, requireBaseline: true,
		expectedTurnSeq: run.BaseTurnSeq, expectedSceneVersion: run.BaseSceneVersion,
		inputID: run.InputID, inputSeq: run.InputSeq,
	})
}

// loadTurn reads the frozen turn input: the world snapshot, the long-memory
// material and the coordination evidence.
func (a *App) loadTurn(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, generator model.TextGenerator, limit int) (turn.Snapshot, error) {
	snapshot, err := loadTurnInput(ctx, store, limit)
	if err != nil {
		return turn.Snapshot{}, turn.AtStage(turn.StageLoad, err)
	}
	if err = a.prepareLongMemory(ctx, store, &snapshot, run, generator); err != nil {
		return turn.Snapshot{}, turn.AtStage(turn.StageLoad, err)
	}
	if err = loadCoordinationEvidence(ctx, store, &snapshot, run.Input); err != nil {
		return turn.Snapshot{}, turn.AtStage(turn.StageLoad, err)
	}
	return snapshot, nil
}
