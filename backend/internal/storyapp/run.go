package storyapp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func validateSceneCharacters(ids []string, characters []wiaworld.Character) error {
	available := make(map[string]bool, len(characters))
	for _, character := range characters {
		available[character.EntityID] = true
	}
	seen := make(map[string]bool, len(ids))
	for index, id := range ids {
		id = wire.Clean(id)
		if id == "" || !available[id] || seen[id] {
			return fmt.Errorf("%w: invalid scene character %q at index %d", turn.ErrGenerationFailed, id, index)
		}
		ids[index] = id
		seen[id] = true
	}
	return nil
}

func classifyTurnFailure(err error) (status, reason, message string) {
	if errors.Is(err, turn.ErrContextCapacity) || errors.Is(err, model.ErrTextInputTooLarge) {
		return "failed", "context_capacity_exceeded", "required story context exceeds available model capacity"
	}
	if errors.Is(err, ErrContextSourceMissing) {
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

type hostResult struct {
	EventOpportunity *eventOpportunity  `json:"event_opportunity,omitempty"`
	InterruptSources []string           `json:"interrupt_source_ids,omitempty"`
	TimeMinutes      int                `json:"time_minutes"`
	Scene            string             `json:"scene"`
	SceneCharacters  []string           `json:"scene_characters"`
	SceneUpdates     []sceneUpdate      `json:"scene_updates"`
	Outcomes         []hostActionResult `json:"outcomes"`
}

type hostActionResult struct {
	ActionID   string   `json:"action_id"`
	Status     string   `json:"status"`
	Content    string   `json:"content"`
	Recipients []string `json:"recipients"`
	// Bystanders names the defined passers-by this outcome actually involved, so
	// their personal experience has a real attribution instead of a guess from prose.
	Bystanders []string `json:"bystanders,omitempty"`
}

const (
	coordinationPromptVersion = "story.coordination.v15"
)

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

// coordinateStage resolves the scene: time, roster, action outcomes and the
// scene views, and returns the events the player can perceive so far.
func (a *App) coordinateStage(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, participants []wiaworld.Character, recipient string, private bool, output *turn.Output) (hostResult, []wiaworld.Event, error) {
	publicReplies := strings.Join(output.PublicReplies, "\n")
	coordinationStarted := time.Now()
	host, coordinationRepairs, err := a.coordinateTurn(ctx, generator, *snapshot, run, intent, output.Decisions, output.Events, publicReplies)
	if err != nil {
		return hostResult{}, nil, turn.AtStage(turn.StageCoordination, err)
	}
	a.logRunStage(snapshot.Summary.WorldID, run, turn.StageCoordination, "coordinate_scene", "scene", 0, coordinationPromptVersion, turn.EventIDs(output.Events), recipient, coordinationRepairs, time.Since(coordinationStarted))
	output.Clock = turn.AdvanceClock(snapshot.Summary.Clock, host.TimeMinutes)
	output.SceneCharacters = append([]string(nil), host.SceneCharacters...)
	if len(host.SceneUpdates) > 0 || !reflect.DeepEqual(output.SceneCharacters, characterIDs(participants)) || turn.SceneFor(*snapshot, "player") != snapshot.Summary.Scene {
		output.SceneVersion = snapshot.SceneVersion + 1
	}
	// Stage 3 outcomes may be witnessed on arrival. Earlier expressions retain
	// their original recipients and are never replayed to the final roster.
	resultParticipants := []wiaworld.Character{}
	for _, character := range snapshot.Characters {
		if character.InScene || wiaworld.ContainsID(host.SceneCharacters, character.EntityID) {
			resultParticipants = append(resultParticipants, character)
		}
	}
	visibleOutcomes, err := appendHostOutcomes(output, run, resultParticipants, snapshot.Definition.BystanderRefs, host.Outcomes)
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("story coordination validation failed: run_id=%q boundary=action_outcomes", run.RunID)
		}
		return hostResult{}, nil, turn.AtStage(turn.StageCoordination, err)
	}
	output.SceneViews, err = applySceneUpdates(*snapshot, run, intent, *output, host)
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("story coordination validation failed: run_id=%q boundary=scene_sources", run.RunID)
		}
		return hostResult{}, nil, turn.AtStage(turn.StageCoordination, err)
	}
	snapshot.SceneViews = output.SceneViews
	output.Scene = turn.SceneFor(*snapshot, "player")
	// The identifier behind the scene text, kept so presence is never decided by
	// comparing prose. A description that names no known place leaves the location
	// unchanged rather than clearing it.
	output.SceneLocation = locationIDFor(snapshot.Definition, output.Scene)
	if output.SceneLocation == "" {
		output.SceneLocation = snapshot.SceneLocation
	}
	snapshot.SceneVersion = output.SceneVersion
	playerNarrativeInput := run.Input
	visibleEvents := turn.VisibleTurnEvents(output.Events, visibleOutcomes, playerNarrativeInput)
	return host, visibleEvents, nil
}

// resolveSceneResult advances the plot and the generated events and folds their
// player-visible results into the events the narration stage renders.
//
// The visible events are written into the output rather than returned, because narration
// reads them and a return value would have to be carried across a stage boundary by
// whoever calls what is in between.
func (a *App) resolveSceneResult(ctx context.Context, generator model.TextGenerator, snapshot *turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, host hostResult, participants []wiaworld.Character, output *turn.Output) error {
	if snapshot.Plot != nil || snapshot.Definition.EventGeneration != nil {
		elapsed := wiaworld.Event{EventID: run.RunID + ":clock", EventType: "time_advanced", ActorID: "world", Content: fmt.Sprintf("本轮实际经过 %d 分钟，从%s到%s。更长的等待请求仅执行到这个时点，剩余时段尚未发生。", host.TimeMinutes, snapshot.Summary.Clock, output.Clock), RunID: run.RunID, Stage: 3, SceneVersion: output.SceneVersion, SourceType: "world_clock", CreatedAt: time.Now().UTC()}
		output.Events = append(output.Events, elapsed)
		output.VisibleEvents = append(output.VisibleEvents, elapsed)
	}
	plotEvents, err := a.advancePlot(ctx, generator, *snapshot, run, output)
	if err != nil {
		return turn.AtStage(turn.StageCoordination, err)
	}
	output.VisibleEvents = append(output.VisibleEvents, plotEvents...)
	generatedVisible, err := a.advanceGeneratedEvents(ctx, generator, *snapshot, run, host.EventOpportunity, output)
	if err != nil {
		return turn.AtStage(turn.StageCoordination, err)
	}
	output.VisibleEvents = append(output.VisibleEvents, generatedVisible...)
	snapshot.SceneViews, snapshot.SceneVersion = output.SceneViews, output.SceneVersion
	return nil
}

// includeNewCharactersInSceneViews gives every character present in the world a view
// of the closing result when the scene projection predates them, so a roster change
// does not invalidate the turn.
func (a *App) includeNewCharactersInSceneViews(ctx context.Context, store *storage.WorldStore, snapshot *turn.Snapshot, output *turn.Output) error {
	current, err := store.LoadCharacters(ctx)
	if err != nil {
		return err
	}
	settled := ""
	for index := len(output.Events) - 1; index >= 0; index-- {
		if output.Events[index].EventType == "turn_settled" {
			settled = output.Events[index].Content
			break
		}
	}
	if settled == "" {
		return nil
	}
	existing := map[string]bool{}
	for _, view := range snapshot.SceneViews {
		existing[view.Recipient] = true
	}
	changed := false
	for _, character := range current {
		if existing[character.EntityID] {
			continue
		}
		// Only a character that genuinely appeared between the snapshot and the commit
		// needs a view, and it must be that character's own starting state rather than
		// the player's whole projection.
		output.SceneViews = append(output.SceneViews, turn.SceneView{
			Recipient: character.EntityID,
			Content:   fmt.Sprintf("%s 此刻在原地，尚未有属于本人的已提交结果。", character.Name),
			Version:   output.SceneVersion + 1,
		})
		existing[character.EntityID] = true
		changed = true
	}
	if changed {
		output.SceneVersion++
		snapshot.SceneVersion = output.SceneVersion
	}
	return nil
}

func appendHostOutcomes(output *turn.Output, run wiaworld.Run, participants []wiaworld.Character, bystanders []story.Bystander, outcomes []hostActionResult) ([]wiaworld.Event, error) {
	actions := make(map[string]wiaworld.Event)
	for _, event := range output.Events {
		if event.EventType == "npc_action_intent" || event.EventType == "player_action_intent" {
			actions[event.EventID] = event
		}
	}
	if len(outcomes) != len(actions) {
		return nil, fmt.Errorf("%w: outcome count %d does not match action count %d", turn.ErrGenerationFailed, len(outcomes), len(actions))
	}
	participantIDs := make(map[string]bool, len(participants))
	for _, character := range participants {
		participantIDs[character.EntityID] = true
	}
	definedBystanders := make(map[string]bool, len(bystanders))
	for _, bystander := range bystanders {
		definedBystanders[bystander.BystanderID] = true
	}
	seen := make(map[string]bool, len(outcomes))
	var visible []wiaworld.Event
	for index, outcome := range outcomes {
		outcome.ActionID = wire.Clean(outcome.ActionID)
		outcome.Status = strings.ToLower(wire.Clean(outcome.Status))
		outcome.Content = wire.Clean(outcome.Content)
		action, ok := actions[outcome.ActionID]
		if !ok || seen[outcome.ActionID] || outcome.Content == "" || (outcome.Status != "succeeded" && outcome.Status != "failed" && outcome.Status != "partial" && outcome.Status != "not_executed") || outcome.Recipients == nil {
			return nil, fmt.Errorf("%w: invalid outcome at index %d", turn.ErrGenerationFailed, index)
		}
		seen[outcome.ActionID] = true
		recipients := make(map[string]bool, len(outcome.Recipients)+1)
		for _, id := range outcome.Recipients {
			id = wire.Clean(id)
			if id != "player" && !participantIDs[id] {
				return nil, fmt.Errorf("%w: outcome %q has unknown recipient %q", turn.ErrGenerationFailed, outcome.ActionID, id)
			}
			recipients[id] = true
		}
		// An actor always observes the resolved result of its own attempt.
		recipients[action.ActorID] = true
		resultID := fmt.Sprintf("%s:result:%d", outcome.ActionID, index+1)
		resultEvent := wiaworld.Event{EventID: resultID, EventType: "npc_action_result", ActorID: action.ActorID, TargetID: action.TargetID, Content: outcome.Content, RunID: run.RunID, Stage: 3, SceneVersion: output.SceneVersion, SourceType: "action_" + outcome.Status, CreatedAt: time.Now().UTC()}
		if action.ActorID == "player" {
			resultEvent.EventType = "player_action_result"
		}
		output.Events = append(output.Events, resultEvent)
		for _, character := range participants {
			if recipients[character.EntityID] {
				output.Perceptions = append(output.Perceptions, wiaworld.Perception{RecipientID: character.EntityID, SourceEventID: resultID, SourceType: "action_" + outcome.Status, Content: outcome.Content, Stage: 3, SceneVersion: output.SceneVersion, CreatedAt: time.Now().UTC()})
			}
		}
		if recipients["player"] {
			visible = append(visible, resultEvent)
		}
		// A passers-by who actually took part in the outcome keeps that as their own
		// experience; being in the room still grants nothing.
		involved := make(map[string]bool, len(outcome.Bystanders))
		for _, id := range outcome.Bystanders {
			id = wire.Clean(id)
			if id == "" || involved[id] {
				continue
			}
			if !definedBystanders[id] {
				return nil, fmt.Errorf("%w: outcome %q attributes an undefined bystander %q", turn.ErrGenerationFailed, outcome.ActionID, id)
			}
			involved[id] = true
			output.Perceptions = append(output.Perceptions, wiaworld.Perception{RecipientID: id, SourceEventID: resultID, SourceType: "action_" + outcome.Status, Content: outcome.Content, Stage: 3, SceneVersion: output.SceneVersion, CreatedAt: time.Now().UTC()})
		}
	}
	return visible, nil
}

func (a *App) coordinateTurn(ctx context.Context, generator model.TextGenerator, snapshot turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, decisions map[string]turn.NPCDecision, events []wiaworld.Event, publicReplies string) (hostResult, int, error) {
	if generator == nil {
		return hostResult{}, 0, ErrModelNotConfigured
	}
	material := turn.ComposeCoordination(snapshot, run, intent, decisions, events, publicReplies)
	generator = a.contextGenerator(generator, material, snapshot, run, "coordination", "coordinator", 3, coordinationPromptVersion)
	input := material.Required
	var result hostResult
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()
	repairCount, err := turn.GenerateJSONMetrics(callCtx, generator, material.System, input, &result, turn.StructuredTurnOutputTokens, "time_minutes", "scene", "scene_characters", "outcomes", "scene_updates")
	if err != nil {
		return hostResult{}, repairCount, err
	}
	result.Scene = wire.Clean(result.Scene)
	if intent.WaitMinutes > 0 {
		if result.TimeMinutes > min(intent.WaitMinutes, turn.PlotTimeLimit(snapshot)) {
			return hostResult{}, repairCount, fmt.Errorf("%w: waiting exceeds requested duration", turn.ErrGenerationFailed)
		}
		for _, id := range result.InterruptSources {
			e, ok := turn.EventByID(events, id)
			current := ok && e.RunID == run.RunID && e.Stage >= 1 && e.Stage <= 2
			_, committed := turn.EventByID(snapshot.Events, id)
			if !current && !committed {
				if a.logger != nil {
					a.logger.Printf("story coordination validation failed: run_id=%q boundary=wait_interruption_source", run.RunID)
				}
				return hostResult{}, repairCount, fmt.Errorf("%w: invalid wait interruption source", turn.ErrGenerationFailed)
			}
		}
		if result.TimeMinutes < min(intent.WaitMinutes, turn.PlotTimeLimit(snapshot)) && len(result.InterruptSources) == 0 {
			if a.logger != nil {
				a.logger.Printf("story coordination validation failed: run_id=%q boundary=wait_shortened", run.RunID)
			}
			return hostResult{}, repairCount, fmt.Errorf("%w: waiting shortened without interruption evidence", turn.ErrGenerationFailed)
		}
	}
	if result.Scene == "" || result.TimeMinutes < 0 || result.TimeMinutes > turn.PlotTimeLimit(snapshot) || result.SceneCharacters == nil || result.Outcomes == nil || result.SceneUpdates == nil {
		if a.logger != nil {
			a.logger.Printf("story coordination validation failed: run_id=%q boundary=required_fields time_minutes=%d limit=%d", run.RunID, result.TimeMinutes, turn.PlotTimeLimit(snapshot))
		}
		return hostResult{}, repairCount, fmt.Errorf("%w: invalid scene coordination fields", turn.ErrGenerationFailed)
	}
	result.SceneCharacters = turn.NormalizeSceneCharacters(result.SceneCharacters)
	if err := validateSceneCharacters(result.SceneCharacters, snapshot.Characters); err != nil {
		if a.logger != nil {
			a.logger.Printf("story coordination validation failed: run_id=%q boundary=scene_character_id", run.RunID)
		}
		return hostResult{}, repairCount, err
	}
	return result, repairCount, nil
}

func characterIDs(items []wiaworld.Character) []string {
	result := make([]string, 0, len(items))
	for _, character := range items {
		result = append(result, character.EntityID)
	}
	return result
}
