package storyapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"gameagent/runtime/internal/model"
)

type npcDecision struct {
	RecallQuery  string `json:"recall_query,omitempty"`
	Speech       string `json:"speech"`
	ActionIntent string `json:"action_intent"`
	Silent       bool   `json:"silent"`
	Memory       string `json:"memory"`
}

type turnIntent struct {
	WaitMinutes int    `json:"wait_minutes,omitempty"`
	IntentType  string `json:"intent_type"`
	AddresseeID string `json:"addressee_id"`
	Visibility  string `json:"visibility"`
}

func (intent *turnIntent) validateGeneratedFields() error {
	intent.IntentType = strings.ToLower(cleanText(intent.IntentType))
	intent.Visibility = strings.ToLower(cleanText(intent.Visibility))
	intent.AddresseeID = cleanText(intent.AddresseeID)
	field, expected := "", ""
	switch {
	case intent.IntentType != "speak" && intent.IntentType != "observe" && intent.IntentType != "act":
		field, expected = "intent_type", "speak|observe|act"
	case intent.Visibility != "public" && intent.Visibility != "private":
		field, expected = "visibility", "public|private"
	case intent.WaitMinutes < 0 || intent.WaitMinutes > 120:
		field, expected = "wait_minutes", "integer:0..120"
	}
	if field != "" {
		return &generationJSONError{Code: "json_field_value", Field: field, Expected: expected, Cause: ErrGenerationFailed}
	}
	return nil
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
}

type narrativeResult struct {
	Narrative string `json:"narrative"`
}

type npcStageInput struct {
	PlayerPerception string
	NewStimulus      string
	SourceEventIDs   []string
}

type narrativeEvent struct {
	OutcomeStatus      string `json:"outcome_status,omitempty"`
	EventID            string `json:"event_id"`
	EventType          string `json:"event_type"`
	ActorID            string `json:"actor_id"`
	ActorName          string `json:"actor_name"`
	ActorRole          string `json:"actor_role,omitempty"`
	NarrativeReference string `json:"narrative_reference"`
	Stage              int    `json:"stage"`
	SpeechScope        string `json:"speech_scope,omitempty"`
	Content            string `json:"content"`
}

type turnStage string

const (
	turnStageLoad         turnStage = "load_world"
	turnStageIntent       turnStage = "intent"
	turnStageNPC          turnStage = "npc"
	turnStageCoordination turnStage = "coordination"
	turnStageNarration    turnStage = "narration"
	turnStageCommit       turnStage = "commit"

	structuredTurnOutputTokens = 4096

	intentPromptVersion       = "story.intent.v7"
	npcPromptVersion          = "story.npc.v13"
	coordinationPromptVersion = "story.coordination.v15"
	narrationPromptVersion    = "story.narration.v11"
)

type turnStageError struct {
	Stage turnStage
	Err   error
}

func (e *turnStageError) Error() string { return string(e.Stage) + ": " + e.Err.Error() }
func (e *turnStageError) Unwrap() error { return e.Err }

func atTurnStage(stage turnStage, err error) error {
	if err == nil {
		return nil
	}
	return &turnStageError{Stage: stage, Err: err}
}

func stageOf(err error) string {
	var staged *turnStageError
	if errors.As(err, &staged) {
		return string(staged.Stage)
	}
	return "unknown"
}

func classifyTurnFailure(err error) (status, reason, message string) {
	if errors.Is(err, ErrContextCapacity) || errors.Is(err, model.ErrTextInputTooLarge) {
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
	var staged *turnStageError
	if errors.As(err, &staged) {
		switch staged.Stage {
		case turnStageLoad, turnStageCommit:
			return "failed", "storage_unavailable", "the turn could not access its save data"
		case turnStageIntent:
			return "failed", "intent_generation_failed", "the player intent could not be understood"
		case turnStageNPC:
			return "failed", "npc_generation_failed", "one or more characters could not respond"
		case turnStageCoordination:
			return "failed", "coordination_generation_failed", "the scene outcome could not be resolved"
		case turnStageNarration:
			return "failed", "narration_generation_failed", "the story response could not be written"
		}
	}
	return "failed", "generation_failed", "the turn did not complete"
}

type turnOutput struct {
	GeneratedEvents *generatedEventState
	Narrative       string
	Clock           string
	Scene           string
	SceneVersion    int64
	SceneCharacters []string
	SceneViews      []SceneView
	PlotProgress    *PlotProgress
	Events          []Event
	Perceptions     []Perception
	Memories        []Memory
}

func (a *App) SubmitRun(ctx context.Context, worldID string, request RunRequest) (Run, error) {
	request.Input = cleanText(request.Input)
	request.RequestKey = strings.TrimSpace(request.RequestKey)
	request.AddresseeID = cleanText(request.AddresseeID)
	if request.RequestKey == "" || request.Input == "" || len([]rune(request.Input)) > 4000 {
		return Run{}, ErrInvalidRequest
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return Run{}, err
	}
	world := a.worldRuntimeFor(worldID)
	world.mu.Lock()
	defer world.mu.Unlock()
	store, err := openWorldDB(path)
	if err != nil {
		return Run{}, err
	}
	defer store.db.Close()
	if existing, found, err := readRunByRequest(ctx, store.db, request.RequestKey); err != nil {
		return Run{}, err
	} else if found {
		if existing.RequestHash != a.hashRun(request) {
			return Run{}, ErrIdempotencyConflict
		}
		return existing, nil
	}
	activeID, activeRevision, err := a.activeWorld(ctx)
	if err != nil || activeID != worldID {
		return Run{}, ErrVersionConflict
	}
	if request.ExpectedActiveRevision > 0 && request.ExpectedActiveRevision != activeRevision {
		return Run{}, ErrVersionConflict
	}
	if status != "ready" {
		return Run{}, ErrWorldNotReady
	}
	a.modelMu.RLock()
	generator := a.generator
	a.modelMu.RUnlock()
	if generator == nil {
		return Run{}, ErrModelNotConfigured
	}
	if world.savePending {
		return Run{}, ErrWorldBusy
	}
	if err := memoryReady(ctx, store.db); err != nil {
		return Run{}, err
	}
	snapshot, err := loadWorldSnapshot(ctx, store, 1)
	if err != nil {
		return Run{}, err
	}
	if snapshot.Summary.StoryEnded {
		return Run{}, ErrStoryEnded
	}
	if (request.requireBaseline || request.ExpectedMessageHead > 0) && request.ExpectedMessageHead != snapshot.Summary.MessageHead {
		return Run{}, ErrVersionConflict
	}
	if (request.requireBaseline || request.ExpectedEventHead > 0) && request.ExpectedEventHead != snapshot.Summary.EventHead {
		return Run{}, ErrVersionConflict
	}
	if (request.requireBaseline || request.ExpectedContextEpoch > 0) && request.ExpectedContextEpoch != snapshot.Summary.ContextEpoch {
		return Run{}, ErrVersionConflict
	}
	if request.requireBaseline && (request.expectedTurnSeq != snapshot.Summary.TurnSeq || request.expectedSceneVersion != snapshot.SceneVersion) {
		return Run{}, ErrVersionConflict
	}
	if request.AddresseeID != "" {
		if _, ok := findSceneCharacter(snapshot.Characters, request.AddresseeID); !ok {
			return Run{}, ErrInvalidRequest
		}
	}
	if count, err := countActiveRuns(ctx, store.db); err != nil {
		return Run{}, err
	} else if count > 0 {
		return Run{}, ErrWorldBusy
	}
	now := time.Now().UTC()
	attempt := request.attempt
	if attempt < 1 {
		attempt = 1
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	inputID := cleanText(request.inputID)
	inputSeq := request.inputSeq
	if inputSeq > 0 {
		if inputID == "" {
			return Run{}, ErrVersionConflict
		}
		var latestInputSeq int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(input_seq),0) FROM runs`).Scan(&latestInputSeq); err != nil {
			return Run{}, err
		}
		if latestInputSeq != inputSeq {
			return Run{}, ErrVersionConflict
		}
		var completed int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE input_id=? AND status='completed'`, inputID).Scan(&completed); err != nil {
			return Run{}, err
		}
		if completed > 0 {
			return Run{}, ErrVersionConflict
		}
	} else {
		value, err := metaGetTx(ctx, tx, "input_seq")
		if err != nil {
			return Run{}, err
		}
		inputSeq, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Run{}, err
		}
		inputSeq++
		inputID = newID("input")
		if err := metaSetTx(ctx, tx, "input_seq", strconv.FormatInt(inputSeq, 10)); err != nil {
			return Run{}, err
		}
	}
	run := Run{
		RunID: newID("run"), RequestKey: request.RequestKey, RequestHash: a.hashRun(request),
		Input: request.Input, AddresseeID: cleanText(request.AddresseeID), Attempt: attempt,
		Status: "accepted", CreatedAt: now, UpdatedAt: now,
		InputID: inputID, InputSeq: inputSeq,
		BaseTurnSeq: snapshot.Summary.TurnSeq, BaseMessageHead: snapshot.Summary.MessageHead,
		BaseEventHead: snapshot.Summary.EventHead, BaseContextEpoch: snapshot.Summary.ContextEpoch,
		BaseSceneVersion: snapshot.SceneVersion,
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runs(
		run_id,request_key,request_hash,input,addressee_id,attempt,status,input_id,input_seq,base_turn_seq,base_message_head,base_event_head,base_context_epoch,base_scene_version,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.RunID, run.RequestKey, run.RequestHash, run.Input,
		run.AddresseeID, run.Attempt, run.Status, run.InputID, run.InputSeq, run.BaseTurnSeq, run.BaseMessageHead, run.BaseEventHead,
		run.BaseContextEpoch, run.BaseSceneVersion, run.CreatedAt.Format(time.RFC3339Nano), run.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, err
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	world.cancelSuggestions()
	runtime := &runRuntime{Cancel: cancel, Done: make(chan struct{}), WorldID: worldID, RunID: run.RunID, ActiveRevision: activeRevision, Generator: generator}
	a.runsMu.Lock()
	a.runs[run.RunID] = runtime
	a.runsMu.Unlock()
	go a.runWorker(runCtx, runtime, run)
	return run, nil
}

func (a *App) runWorker(ctx context.Context, runtime *runRuntime, run Run) {
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
	store, err := openWorldDB(path)
	if err != nil {
		a.logRunFailure(runtime.WorldID, run, "open_world", "storage_unavailable", err)
		return
	}
	defer store.db.Close()
	if err := updateRunStatus(context.Background(), store.db, run.RunID, "running", "", ""); err != nil {
		a.logRunFailure(runtime.WorldID, run, "mark_running", "storage_unavailable", err)
		return
	}
	if !a.isActive(context.Background(), runtime.WorldID, runtime.ActiveRevision) {
		_ = updateRunStatus(context.Background(), store.db, run.RunID, "cancelled", "world_switched", "the active world changed")
		return
	}
	output, err := a.executeTurn(ctx, store, run, runtime.Generator)
	if err != nil {
		status, reason, message := classifyTurnFailure(err)
		if status == "failed" {
			a.logRunFailure(runtime.WorldID, run, stageOf(err), reason, err)
		}
		if updateErr := updateRunStatus(context.Background(), store.db, run.RunID, status, reason, message); updateErr != nil {
			a.logRunFailure(runtime.WorldID, run, "record_failure", "storage_unavailable", updateErr)
		}
		return
	}
	if !a.isActive(context.Background(), runtime.WorldID, runtime.ActiveRevision) {
		_ = updateRunStatus(context.Background(), store.db, run.RunID, "cancelled", "world_switched", "the active world changed")
		return
	}
	commitStarted := time.Now()
	if _, err := commitTurn(ctx, store, run, output.Narrative, output.Events, output.Perceptions, output.Memories, output.Clock, output.Scene, output.SceneVersion, output.SceneCharacters, output.SceneViews, output.PlotProgress, output.GeneratedEvents); err != nil {
		err = atTurnStage(turnStageCommit, err)
		status, reason, message := classifyTurnFailure(err)
		if status == "failed" {
			a.logRunFailure(runtime.WorldID, run, string(turnStageCommit), reason, err)
		}
		if updateErr := updateRunStatus(context.Background(), store.db, run.RunID, status, reason, message); updateErr != nil {
			a.logRunFailure(runtime.WorldID, run, "record_failure", "storage_unavailable", updateErr)
		}
		return
	}
	a.logRunStage(runtime.WorldID, run, turnStageCommit, "atomic_commit", "", 0, "", nil, "", 0, time.Since(commitStarted))
	_ = a.touchWorld(context.Background(), runtime.WorldID)
}

func (a *App) logRunFailure(worldID string, run Run, stage, reason string, err error) {
	if a.logger == nil || err == nil {
		return
	}
	a.logger.Printf("story turn failed: world_id=%q run_id=%q attempt=%d stage=%q reason=%q error_code=%q", worldID, run.RunID, run.Attempt, stage, reason, safeTurnErrorCode(err))
}

func (a *App) logRunStage(worldID string, run Run, stage turnStage, purpose, actorID string, stageIndex int, promptVersion string, sourceEventIDs []string, resolvedAddressee string, repairCount int, elapsed time.Duration) {
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

func (a *App) Run(ctx context.Context, worldID, runID string) (Run, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return Run{}, err
	}
	if status != "ready" {
		return Run{}, ErrWorldNotReady
	}
	store, err := openWorldDB(path)
	if err != nil {
		return Run{}, err
	}
	defer store.db.Close()
	run, found, err := readRun(ctx, store.db, runID)
	if err != nil {
		return Run{}, err
	}
	if !found {
		return Run{}, ErrRunNotFound
	}
	return run, nil
}

func (a *App) ListRuns(ctx context.Context, worldID string, requestKeys ...string) ([]Run, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return nil, err
	}
	if status != "ready" {
		return nil, ErrWorldNotReady
	}
	store, err := openWorldDB(path)
	if err != nil {
		return nil, err
	}
	defer store.db.Close()
	if len(requestKeys) > 0 && requestKeys[0] != "" {
		run, found, err := readRunByRequest(ctx, store.db, requestKeys[0])
		if err != nil {
			return nil, err
		}
		if !found {
			return []Run{}, nil
		}
		return []Run{run}, nil
	}
	rows, err := store.db.QueryContext(ctx, `SELECT run_id,request_key,request_hash,input,addressee_id,attempt,status,reason,error,message_seq,input_id,input_seq,base_turn_seq,base_message_head,base_event_head,base_context_epoch,base_scene_version,created_at,updated_at FROM runs ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]Run, 0)
	for rows.Next() {
		run, found, err := scanRun(rows)
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
	store, err := openWorldDB(path)
	if err != nil {
		return err
	}
	defer store.db.Close()
	if _, err := store.db.ExecContext(ctx, `UPDATE runs SET cancel_requested=1,updated_at=? WHERE run_id=? AND status IN ('accepted','running')`, nowText(), runID); err != nil {
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

func (a *App) RetryRun(ctx context.Context, worldID, runID, requestKey string) (Run, error) {
	path, _, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return Run{}, err
	}
	store, err := openWorldDB(path)
	if err != nil {
		return Run{}, err
	}
	run, found, err := readRun(ctx, store.db, runID)
	_ = store.db.Close()
	if err != nil {
		return Run{}, err
	}
	if !found {
		return Run{}, ErrRunNotFound
	}
	if run.Status != "failed" && run.Status != "cancelled" && run.Status != "interrupted" {
		return Run{}, ErrInvalidRequest
	}
	if run.InputID == "" || run.InputSeq <= 0 {
		return Run{}, ErrVersionConflict
	}
	if strings.TrimSpace(requestKey) == "" {
		requestKey = newID("retry")
	}
	return a.SubmitRun(ctx, worldID, RunRequest{
		RequestKey: requestKey, Input: run.Input, AddresseeID: run.AddresseeID, attempt: run.Attempt + 1,
		ExpectedMessageHead: run.BaseMessageHead, ExpectedEventHead: run.BaseEventHead,
		ExpectedContextEpoch: run.BaseContextEpoch, requireBaseline: true,
		expectedTurnSeq: run.BaseTurnSeq, expectedSceneVersion: run.BaseSceneVersion,
		inputID: run.InputID, inputSeq: run.InputSeq,
	})
}

func (a *App) resolveTurnIntent(ctx context.Context, generator model.TextGenerator, snapshot worldSnapshot, run Run) (turnIntent, int, error) {
	participants := sceneCharacters(snapshot.Characters)
	explicitRecipient := cleanText(run.AddresseeID)
	if explicitRecipient != "" {
		if _, ok := findSceneCharacter(participants, explicitRecipient); !ok {
			return turnIntent{}, 0, ErrInvalidRequest
		}
	}
	material := composeIntent(snapshot, run)
	generator = a.contextGenerator(generator, material, snapshot, run, "intent", "player", 0, intentPromptVersion)
	input := material.Required
	var intent turnIntent
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	checkRecipient := func() error {
		if explicitRecipient != "" {
			intent.AddresseeID = explicitRecipient
		}
		if intent.AddresseeID != "" {
			if _, ok := findSceneCharacter(participants, intent.AddresseeID); !ok {
				return &generationJSONError{Code: "json_field_value", Field: "addressee_id", Expected: "listed-in-scene-important-character-or-empty", Cause: ErrGenerationFailed}
			}
		}
		if intent.Visibility == "private" && intent.AddresseeID == "" {
			return &generationJSONError{Code: "json_field_value", Field: "visibility", Expected: "public-when-addressee-is-empty", Cause: ErrGenerationFailed}
		}
		return nil
	}
	repairCount, err := generateJSONCheckedMetrics(callCtx, generator, material.System, input, &intent, structuredTurnOutputTokens, []string{"addressee_id"}, []string{"intent_type", "addressee_id", "visibility"}, checkRecipient)
	if err != nil {
		return turnIntent{}, repairCount, err
	}
	return intent, repairCount, nil
}

func (a *App) executeTurn(ctx context.Context, store *worldStore, run Run, generator model.TextGenerator) (turnOutput, error) {
	loadStarted := time.Now()
	snapshot, err := loadTurnSnapshot(ctx, store, 40)
	if err != nil {
		return turnOutput{}, atTurnStage(turnStageLoad, err)
	}
	if err = a.prepareLongMemory(ctx, store, &snapshot, run, generator); err != nil {
		return turnOutput{}, atTurnStage(turnStageLoad, err)
	}
	if err = loadCoordinationEvidence(ctx, store, &snapshot, run.Input); err != nil {
		return turnOutput{}, atTurnStage(turnStageLoad, err)
	}
	a.logRunStage(snapshot.Summary.WorldID, run, turnStageLoad, "load_snapshot", "", 0, "", nil, "", 0, time.Since(loadStarted))
	def := snapshot.Definition
	def.Characters = snapshot.Characters
	intentStarted := time.Now()
	intent, intentRepairs, err := a.resolveTurnIntent(ctx, generator, snapshot, run)
	if err != nil {
		return turnOutput{}, atTurnStage(turnStageIntent, err)
	}
	a.logRunStage(snapshot.Summary.WorldID, run, turnStageIntent, "resolve_intent", "", 0, intentPromptVersion, nil, intent.AddresseeID, intentRepairs, time.Since(intentStarted))
	recipient := intent.AddresseeID
	private := intent.Visibility == "private"
	participants := sceneCharacters(snapshot.Characters)
	now := time.Now().UTC()
	playerEventID := run.RunID + ":input"
	output := turnOutput{
		Clock: snapshot.Summary.Clock, Scene: snapshot.Summary.Scene, SceneVersion: snapshot.SceneVersion,
		SceneCharacters: characterIDs(participants),
		Events:          []Event{{EventID: playerEventID, EventType: "player_attempt", ActorID: "player", TargetID: recipient, Content: run.Input, RunID: run.RunID, Stage: 1, SceneVersion: snapshot.SceneVersion, SourceType: "player_" + intent.Visibility, CreatedAt: now}},
		Memories:        []Memory{}, Perceptions: []Perception{},
	}
	decisions := make(map[string]npcDecision)
	perceptText := make(map[string]string)
	stageOneInputs := make(map[string]npcStageInput)
	for _, character := range participants {
		if private && character.EntityID != recipient {
			perceptText[character.EntityID] = fmt.Sprintf("你看见玩家与%s低声交谈，但听不清内容。不要猜测耳语原文。", describeRecipient(def, recipient))
		} else {
			perceptText[character.EntityID] = run.Input
		}
		output.Perceptions = append(output.Perceptions, Perception{RecipientID: character.EntityID, SourceEventID: playerEventID, SourceType: sourceTypeFor(private, character.EntityID, recipient, intent.IntentType), Content: perceptText[character.EntityID], Stage: 1, SceneVersion: snapshot.SceneVersion, CreatedAt: now})
		stageOneInputs[character.EntityID] = npcStageInput{PlayerPerception: perceptText[character.EntityID], SourceEventIDs: []string{playerEventID}}
	}
	if err := a.decideNPCs(ctx, generator, snapshot, def, run, recipient, intent.IntentType, stageOneInputs, nil, decisions, 1); err != nil {
		return turnOutput{}, atTurnStage(turnStageNPC, err)
	}
	var publicReplyLog []string
	for _, character := range participants {
		decision := decisions[character.EntityID]
		if reply := appendNPCDecisionOutput(&output, run, character, decision, participants, playerEventID, snapshot.SceneVersion, 1); reply != "" {
			publicReplyLog = append(publicReplyLog, reply)
		}
	}

	// Only public replies from other characters are new stage-two stimuli.
	stageTwoInputs := publicReplyStageInputs(output.Perceptions, perceptText, 1)
	if len(stageTwoInputs) > 0 {
		priorTurn := make(map[string]string, len(stageTwoInputs))
		for characterID := range stageTwoInputs {
			priorTurn[characterID] = fmt.Sprintf("第一阶段自己的决定：%s", formatSelfDecision(decisions[characterID]))
		}
		followDecisions := make(map[string]npcDecision)
		if err := a.decideNPCs(ctx, generator, snapshot, def, run, recipient, intent.IntentType, stageTwoInputs, priorTurn, followDecisions, 2); err != nil {
			return turnOutput{}, atTurnStage(turnStageNPC, err)
		}
		for _, character := range participants {
			decision, ok := followDecisions[character.EntityID]
			if !ok {
				continue
			}
			input := stageTwoInputs[character.EntityID]
			sourceEventID := playerEventID
			if len(input.SourceEventIDs) > 0 {
				sourceEventID = input.SourceEventIDs[0]
			}
			if reply := appendNPCDecisionOutput(&output, run, character, decision, participants, sourceEventID, snapshot.SceneVersion, 2); reply != "" {
				publicReplyLog = append(publicReplyLog, reply)
			}
			decisions[character.EntityID] = mergeNPCDecision(decisions[character.EntityID], decision)
		}
	}

	publicReplies := strings.Join(publicReplyLog, "\n")
	if intent.IntentType != "speak" || recipient == "" {
		output.Events = append(output.Events, Event{EventID: run.RunID + ":player-action", EventType: "player_action_intent", ActorID: "player", Content: run.Input, RunID: run.RunID, Stage: 2, SceneVersion: snapshot.SceneVersion, SourceType: "player_attempt", CreatedAt: time.Now().UTC()})
	}
	coordinationStarted := time.Now()
	host, coordinationRepairs, err := a.coordinateTurn(ctx, generator, snapshot, run, intent, decisions, output.Events, publicReplies)
	if err != nil {
		return turnOutput{}, atTurnStage(turnStageCoordination, err)
	}
	a.logRunStage(snapshot.Summary.WorldID, run, turnStageCoordination, "coordinate_scene", "scene", 0, coordinationPromptVersion, eventIDs(output.Events), recipient, coordinationRepairs, time.Since(coordinationStarted))
	output.Clock = advanceClock(snapshot.Summary.Clock, host.TimeMinutes)
	output.SceneCharacters = append([]string(nil), host.SceneCharacters...)
	if len(host.SceneUpdates) > 0 || !reflect.DeepEqual(output.SceneCharacters, characterIDs(participants)) || sceneFor(snapshot, "player") != snapshot.Summary.Scene {
		output.SceneVersion = snapshot.SceneVersion + 1
	}
	// Stage 3 outcomes may be witnessed on arrival. Earlier expressions retain
	// their original recipients and are never replayed to the final roster.
	resultParticipants := []Character{}
	for _, character := range snapshot.Characters {
		if character.InScene || containsID(host.SceneCharacters, character.EntityID) {
			resultParticipants = append(resultParticipants, character)
		}
	}
	visibleOutcomes, err := appendHostOutcomes(&output, run, resultParticipants, host.Outcomes)
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("story coordination validation failed: run_id=%q boundary=action_outcomes", run.RunID)
		}
		return turnOutput{}, atTurnStage(turnStageCoordination, err)
	}
	output.SceneViews, err = applySceneUpdates(snapshot, run, intent, output, host)
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("story coordination validation failed: run_id=%q boundary=scene_sources", run.RunID)
		}
		return turnOutput{}, atTurnStage(turnStageCoordination, err)
	}
	snapshot.SceneViews = output.SceneViews
	output.Scene = sceneFor(snapshot, "player")
	snapshot.SceneVersion = output.SceneVersion
	playerNarrativeInput := run.Input
	visibleEvents := visibleTurnEvents(output.Events, visibleOutcomes, playerNarrativeInput)
	if snapshot.Plot != nil || snapshot.Definition.EventGeneration != nil {
		elapsed := Event{EventID: run.RunID + ":clock", EventType: "time_advanced", ActorID: "world", Content: fmt.Sprintf("本轮实际经过 %d 分钟，从%s到%s。更长的等待请求仅执行到这个时点，剩余时段尚未发生。", host.TimeMinutes, snapshot.Summary.Clock, output.Clock), RunID: run.RunID, Stage: 3, SceneVersion: output.SceneVersion, SourceType: "world_clock", CreatedAt: time.Now().UTC()}
		output.Events = append(output.Events, elapsed)
		visibleEvents = append(visibleEvents, elapsed)
	}
	plotEvents, err := a.advancePlot(ctx, generator, snapshot, run, &output)
	if err != nil {
		return turnOutput{}, atTurnStage(turnStageCoordination, err)
	}
	visibleEvents = append(visibleEvents, plotEvents...)
	generatedVisible, err := a.advanceGeneratedEvents(ctx, generator, snapshot, run, host.EventOpportunity, &output)
	if err != nil {
		return turnOutput{}, atTurnStage(turnStageCoordination, err)
	}
	visibleEvents = append(visibleEvents, generatedVisible...)
	snapshot.SceneViews, snapshot.SceneVersion = output.SceneViews, output.SceneVersion
	playerProjection := renderVisibleProjection(visibleEvents, snapshot.Characters)
	narrationStarted := time.Now()
	result, narrationRepairs, err := a.narrateVisible(ctx, generator, snapshot, run, def, recipient, intent.IntentType, visibleEvents, private, output.Clock, output.Scene, output.SceneCharacters)
	if err != nil {
		return turnOutput{}, atTurnStage(turnStageNarration, err)
	}
	a.logRunStage(snapshot.Summary.WorldID, run, turnStageNarration, "render_player_text", "scene", 0, narrationPromptVersion, eventIDs(visibleEvents), recipient, narrationRepairs, time.Since(narrationStarted))
	output.Narrative = result.Narrative
	settledStage := 3
	for _, event := range output.Events {
		if event.Stage >= 4 {
			settledStage = 7
		}
	}
	output.Events = append(output.Events, Event{EventID: run.RunID + ":outcome", EventType: "turn_settled", ActorID: "scene", Content: playerProjection, RunID: run.RunID, Stage: settledStage, SceneVersion: output.SceneVersion, SourceType: "scene", CreatedAt: time.Now().UTC()})
	for _, character := range participants {
		kind, memory := playerExperienceMemory(intent.IntentType, private, character.EntityID, recipient, run.Input, def)
		output.Memories = append(output.Memories, Memory{RecipientID: character.EntityID, Kind: kind, Content: memory, SourceEventID: playerEventID, CreatedAt: time.Now().UTC()})
	}
	return output, nil
}

func appendNPCDecisionOutput(output *turnOutput, run Run, character Character, decision npcDecision, participants []Character, defaultSourceEventID string, sceneVersion int64, stage int) string {
	sourceEventID := defaultSourceEventID
	if decision.ActionIntent != "" {
		actionEventID := fmt.Sprintf("%s:%s:action:%d", run.RunID, character.EntityID, stage)
		output.Events = append(output.Events, Event{EventID: actionEventID, EventType: "npc_action_intent", ActorID: character.EntityID, TargetID: "player", Content: decision.ActionIntent, RunID: run.RunID, Stage: stage, SceneVersion: sceneVersion, SourceType: "npc_intent", CreatedAt: time.Now().UTC()})
		sourceEventID = actionEventID
	}
	var reply string
	if decision.Speech != "" {
		eventID := fmt.Sprintf("%s:%s:speech:%d", run.RunID, character.EntityID, stage)
		output.Events = append(output.Events, Event{EventID: eventID, EventType: "npc_dialogue", ActorID: character.EntityID, TargetID: "player", Content: decision.Speech, RunID: run.RunID, Stage: stage, SceneVersion: sceneVersion, SourceType: "visible_dialogue", CreatedAt: time.Now().UTC()})
		for _, other := range participants {
			if other.EntityID != character.EntityID {
				output.Perceptions = append(output.Perceptions, Perception{RecipientID: other.EntityID, SourceEventID: eventID, SourceType: "heard_public_reply", Content: fmt.Sprintf("%s（%s）公开说：%s", character.Name, character.Role, decision.Speech), Stage: stage, SceneVersion: sceneVersion, CreatedAt: time.Now().UTC()})
			}
		}
		if decision.ActionIntent == "" {
			sourceEventID = eventID
		}
		reply = fmt.Sprintf("%s（%s）说：%s", character.Name, character.Role, decision.Speech)
	}
	if decision.Memory != "" {
		output.Memories = append(output.Memories, Memory{RecipientID: character.EntityID, Kind: "character_judgment", Content: cleanText(decision.Memory), SourceEventID: sourceEventID, CreatedAt: time.Now().UTC()})
	}
	return reply
}

func appendHostOutcomes(output *turnOutput, run Run, participants []Character, outcomes []hostActionResult) ([]Event, error) {
	actions := make(map[string]Event)
	for _, event := range output.Events {
		if event.EventType == "npc_action_intent" || event.EventType == "player_action_intent" {
			actions[event.EventID] = event
		}
	}
	if len(outcomes) != len(actions) {
		return nil, fmt.Errorf("%w: outcome count %d does not match action count %d", ErrGenerationFailed, len(outcomes), len(actions))
	}
	participantIDs := make(map[string]bool, len(participants))
	for _, character := range participants {
		participantIDs[character.EntityID] = true
	}
	seen := make(map[string]bool, len(outcomes))
	var visible []Event
	for index, outcome := range outcomes {
		outcome.ActionID = cleanText(outcome.ActionID)
		outcome.Status = strings.ToLower(cleanText(outcome.Status))
		outcome.Content = cleanText(outcome.Content)
		action, ok := actions[outcome.ActionID]
		if !ok || seen[outcome.ActionID] || outcome.Content == "" || (outcome.Status != "succeeded" && outcome.Status != "failed" && outcome.Status != "partial" && outcome.Status != "not_executed") || outcome.Recipients == nil {
			return nil, fmt.Errorf("%w: invalid outcome at index %d", ErrGenerationFailed, index)
		}
		seen[outcome.ActionID] = true
		recipients := make(map[string]bool, len(outcome.Recipients)+1)
		for _, id := range outcome.Recipients {
			id = cleanText(id)
			if id != "player" && !participantIDs[id] {
				return nil, fmt.Errorf("%w: outcome %q has unknown recipient %q", ErrGenerationFailed, outcome.ActionID, id)
			}
			recipients[id] = true
		}
		// An actor always observes the resolved result of its own attempt.
		recipients[action.ActorID] = true
		resultID := fmt.Sprintf("%s:result:%d", outcome.ActionID, index+1)
		resultEvent := Event{EventID: resultID, EventType: "npc_action_result", ActorID: action.ActorID, TargetID: action.TargetID, Content: outcome.Content, RunID: run.RunID, Stage: 3, SceneVersion: output.SceneVersion, SourceType: "action_" + outcome.Status, CreatedAt: time.Now().UTC()}
		if action.ActorID == "player" {
			resultEvent.EventType = "player_action_result"
		}
		output.Events = append(output.Events, resultEvent)
		for _, character := range participants {
			if recipients[character.EntityID] {
				output.Perceptions = append(output.Perceptions, Perception{RecipientID: character.EntityID, SourceEventID: resultID, SourceType: "action_" + outcome.Status, Content: outcome.Content, Stage: 3, SceneVersion: output.SceneVersion, CreatedAt: time.Now().UTC()})
			}
		}
		if recipients["player"] {
			visible = append(visible, resultEvent)
		}
	}
	return visible, nil
}

func renderVisibleProjection(events []Event, characters []Character) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		if event.EventType == "player_attempt" {
			continue
		}
		content := cleanText(event.Content)
		if content == "" {
			continue
		}
		if event.EventType == "npc_dialogue" {
			parts = append(parts, fmt.Sprintf("%s说：%s", characterDisplayName(characters, event.ActorID), content))
		} else {
			parts = append(parts, content)
		}
	}
	if len(parts) == 0 {
		return "没有人立刻回答，也没有发生玩家可见的新结果。"
	}
	return strings.Join(parts, "\n")
}

func visibleTurnEvents(events, visibleOutcomes []Event, playerNarrativeInput string) []Event {
	result := make([]Event, 0, len(events)+len(visibleOutcomes))
	for _, event := range events {
		if event.EventType == "player_attempt" || event.EventType == "npc_dialogue" {
			if event.EventType == "player_attempt" {
				event.Content = playerNarrativeInput
			}
			result = append(result, event)
		}
	}
	return append(result, visibleOutcomes...)
}

func eventIDs(events []Event) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		if event.EventID != "" {
			result = append(result, event.EventID)
		}
	}
	return result
}

func publicReplyStageInputs(perceptions []Perception, playerPerceptions map[string]string, sourceStage int) map[string]npcStageInput {
	texts := make(map[string][]string)
	sources := make(map[string][]string)
	for _, perception := range perceptions {
		if perception.Stage != sourceStage || perception.SourceType != "heard_public_reply" {
			continue
		}
		texts[perception.RecipientID] = append(texts[perception.RecipientID], perception.Content)
		sources[perception.RecipientID] = append(sources[perception.RecipientID], perception.SourceEventID)
	}
	result := make(map[string]npcStageInput, len(texts))
	for recipientID, items := range texts {
		result[recipientID] = npcStageInput{
			PlayerPerception: playerPerceptions[recipientID],
			NewStimulus:      strings.Join(items, "\n"),
			SourceEventIDs:   append([]string(nil), sources[recipientID]...),
		}
	}
	return result
}

func mergeNPCDecision(previous, current npcDecision) npcDecision {
	if current.Speech == "" {
		current.Speech = previous.Speech
	}
	if current.ActionIntent == "" {
		current.ActionIntent = previous.ActionIntent
	}
	if current.Memory == "" {
		current.Memory = previous.Memory
	}
	current.Silent = current.Speech == ""
	return current
}

func advanceClock(clock string, minutes int) string {
	if minutes == 0 {
		return clock
	}
	var day, hour, minute int
	if _, err := fmt.Sscanf(clock, "第 %d 日 %d:%d", &day, &hour, &minute); err != nil || day < 1 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return clock
	}
	total := (day-1)*24*60 + hour*60 + minute + minutes
	if total < 0 {
		return clock
	}
	return fmt.Sprintf("第 %d 日 %02d:%02d", total/(24*60)+1, (total/60)%24, total%60)
}

func sourceTypeFor(private bool, id, recipient, intentType string) string {
	if private && id != recipient {
		return "observed_private_conversation"
	}
	if intentType == "speak" {
		if private {
			return "direct_private_message"
		}
		return "direct_hearing"
	}
	return "observed_player_action"
}

func playerExperienceMemory(intentType string, private bool, characterID, recipient, input string, def gameDefinition) (string, string) {
	if private && characterID != recipient {
		return "observed", "我看见玩家和" + describeRecipient(def, recipient) + "低声交谈，但没有听清内容。"
	}
	switch intentType {
	case "observe":
		return "observed_player_action", "我看见玩家尝试观察：" + input
	case "act":
		return "observed_player_action", "我看见玩家尝试行动：" + input
	default:
		if private {
			return "heard_player", "玩家私下表达：" + input
		}
		return "heard_player", "玩家公开表达：" + input
	}
}

func describeRecipient(def gameDefinition, recipient string) string {
	if recipient == "" {
		return "未明确指定具体人物"
	}
	if character, ok := characterByID(def, recipient); ok {
		return fmt.Sprintf("%s（%s）", character.Name, character.Role)
	}
	return "未明确指定具体人物"
}

func (a *App) decideNPCs(ctx context.Context, generator model.TextGenerator, snapshot worldSnapshot, def gameDefinition, run Run, recipient, intentType string, inputs map[string]npcStageInput, priorTurn map[string]string, decisions map[string]npcDecision, stage int) error {
	if generator == nil {
		return ErrModelNotConfigured
	}
	npcCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	var decisionMu sync.Mutex
	for _, character := range sceneCharacters(snapshot.Characters) {
		character := character
		stageInput, present := inputs[character.EntityID]
		if !present {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			material := composeNPC(snapshot, def, character, recipient, intentType, stageInput, priorTurn[character.EntityID], stage)
			callGenerator := a.contextGenerator(generator, material, snapshot, run, "npc", character.EntityID, stage, npcPromptVersion)
			input := material.Required
			var decision npcDecision
			started := time.Now()
			callCtx, callCancel := context.WithTimeout(npcCtx, 60*time.Second)
			defer callCancel()
			repairCount, err := generateJSONWithNullableFieldsMetrics(callCtx, callGenerator, material.System, input, &decision, structuredTurnOutputTokens, []string{"speech", "action_intent", "memory"}, "speech", "action_intent", "silent", "memory")
			for recall := 0; err == nil && cleanText(decision.RecallQuery) != ""; recall++ {
				if recall >= 2 || len([]rune(decision.RecallQuery)) > 256 {
					err = ErrGenerationFailed
					break
				}
				material = withRecall(material, snapshot.LongMemory[character.EntityID], decision.RecallQuery)
				material.Required += fmt.Sprintf("\n已完成第%d次只读检索；命中材料按因果组纳入预算，已在近期经历或此前检索中的内容不重复添加。参考检索预算说明，缺少材料不等于事情未发生。最多两次，随后根据已获准资料完成决定。", recall+1)
				callGenerator = a.contextGenerator(generator, material, snapshot, run, "npc", character.EntityID, stage, npcPromptVersion)
				decision = npcDecision{}
				var repairs int
				repairs, err = generateJSONWithNullableFieldsMetrics(callCtx, callGenerator, material.System, material.Required, &decision, structuredTurnOutputTokens, []string{"speech", "action_intent", "memory"}, "speech", "action_intent", "silent", "memory")
				repairCount += repairs
			}
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				errMu.Unlock()
				return
			}
			decision.Speech = cleanText(decision.Speech)
			decision.ActionIntent = normalizeNPCActionIntent(decision.ActionIntent)
			decision.Memory = cleanText(decision.Memory)
			if decision.Speech == "" {
				decision.Silent = true
			}
			if decision.Silent {
				decision.Speech = ""
			}
			a.logRunStage(snapshot.Summary.WorldID, run, turnStageNPC, "npc_decision", character.EntityID, stage, npcPromptVersion, stageInput.SourceEventIDs, recipient, repairCount, time.Since(started))
			decisionMu.Lock()
			decisions[character.EntityID] = decision
			decisionMu.Unlock()
		}()
	}
	wg.Wait()
	return firstErr
}

func normalizeNPCActionIntent(value string) string {
	action := cleanText(value)
	if action == "" {
		return ""
	}
	normalized := strings.Trim(action, "。；;，, ")
	for _, passive := range []string{
		"观察", "继续观察", "保持观察", "留意", "继续留意", "注视", "继续注视",
		"观察异常", "保持警惕", "提高警惕", "等待", "继续等待", "维持原位", "留在原位",
		"保持沉默", "继续沉默", "不动", "没有行动", "无行动",
	} {
		if normalized == passive {
			return ""
		}
	}
	return action
}

func formatSelfDecision(decision npcDecision) string {
	return fmt.Sprintf("已进入本轮交谈的本人公开对白：%q\n本人尚未执行、待场景协调的行动提案：%q\n是否保持沉默：%t\n本轮暂存主观判断（不是执行结果）：%q", decision.Speech, decision.ActionIntent, decision.Silent, decision.Memory)
}

func eventByID(events []Event, id string) (Event, bool) {
	for _, event := range events {
		if event.EventID == id {
			return event, true
		}
	}
	return Event{}, false
}

func characterDisplayName(characters []Character, id string) string {
	for _, character := range characters {
		if character.EntityID == id {
			return fmt.Sprintf("%s（%s）", character.Name, character.Role)
		}
	}
	if id == "player" {
		return "玩家"
	}
	return id
}

func (a *App) coordinateTurn(ctx context.Context, generator model.TextGenerator, snapshot worldSnapshot, run Run, intent turnIntent, decisions map[string]npcDecision, events []Event, publicReplies string) (hostResult, int, error) {
	if generator == nil {
		return hostResult{}, 0, ErrModelNotConfigured
	}
	material := composeCoordination(snapshot, run, intent, decisions, events, publicReplies)
	generator = a.contextGenerator(generator, material, snapshot, run, "coordination", "coordinator", 3, coordinationPromptVersion)
	input := material.Required
	var result hostResult
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()
	repairCount, err := generateJSONMetrics(callCtx, generator, material.System, input, &result, structuredTurnOutputTokens, "time_minutes", "scene", "scene_characters", "outcomes", "scene_updates")
	if err != nil {
		return hostResult{}, repairCount, err
	}
	result.Scene = cleanText(result.Scene)
	if intent.WaitMinutes > 0 {
		if result.TimeMinutes > min(intent.WaitMinutes, plotTimeLimit(snapshot)) {
			return hostResult{}, repairCount, fmt.Errorf("%w: waiting exceeds requested duration", ErrGenerationFailed)
		}
		for _, id := range result.InterruptSources {
			e, ok := eventByID(events, id)
			current := ok && e.RunID == run.RunID && e.Stage >= 1 && e.Stage <= 2
			_, committed := eventByID(snapshot.Events, id)
			if !current && !committed {
				if a.logger != nil {
					a.logger.Printf("story coordination validation failed: run_id=%q boundary=wait_interruption_source", run.RunID)
				}
				return hostResult{}, repairCount, fmt.Errorf("%w: invalid wait interruption source", ErrGenerationFailed)
			}
		}
		if result.TimeMinutes < min(intent.WaitMinutes, plotTimeLimit(snapshot)) && len(result.InterruptSources) == 0 {
			if a.logger != nil {
				a.logger.Printf("story coordination validation failed: run_id=%q boundary=wait_shortened", run.RunID)
			}
			return hostResult{}, repairCount, fmt.Errorf("%w: waiting shortened without interruption evidence", ErrGenerationFailed)
		}
	}
	if result.Scene == "" || result.TimeMinutes < 0 || result.TimeMinutes > plotTimeLimit(snapshot) || result.SceneCharacters == nil || result.Outcomes == nil || result.SceneUpdates == nil {
		if a.logger != nil {
			a.logger.Printf("story coordination validation failed: run_id=%q boundary=required_fields time_minutes=%d limit=%d", run.RunID, result.TimeMinutes, plotTimeLimit(snapshot))
		}
		return hostResult{}, repairCount, fmt.Errorf("%w: invalid scene coordination fields", ErrGenerationFailed)
	}
	result.SceneCharacters = normalizeSceneCharacters(result.SceneCharacters)
	if err := validateSceneCharacters(result.SceneCharacters, snapshot.Characters); err != nil {
		if a.logger != nil {
			a.logger.Printf("story coordination validation failed: run_id=%q boundary=scene_character_id", run.RunID)
		}
		return hostResult{}, repairCount, err
	}
	return result, repairCount, nil
}

func (a *App) narrateVisible(ctx context.Context, generator model.TextGenerator, snapshot worldSnapshot, run Run, def gameDefinition, recipient, intentType string, visibleEvents []Event, private bool, clock, scene string, sceneCharacters []string) (narrativeResult, int, error) {
	if generator == nil {
		return narrativeResult{}, 0, ErrModelNotConfigured
	}
	material, maxOutputTokens, err := composeNarration(snapshot, run, def, recipient, intentType, visibleEvents, clock, sceneCharacters)
	if err != nil {
		return narrativeResult{}, 0, err
	}
	stage := 3
	if snapshot.Plot != nil || snapshot.Definition.EventGeneration != nil {
		stage = 7
	}
	generator = a.contextGenerator(generator, material, snapshot, run, "narration", "player", stage, narrationPromptVersion)
	input := material.Required
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()
	narrative, repairCount, err := generateNarrativeText(callCtx, generator, material.System, input, maxOutputTokens)
	if err != nil {
		return narrativeResult{}, repairCount, err
	}
	return narrativeResult{Narrative: narrative}, repairCount, nil
}

func narrativeEvents(events []Event, characters []Character, playerName string, settings NarrativeSettings) []narrativeEvent {
	result := make([]narrativeEvent, 0, len(events))
	for _, event := range events {
		name, role, reference := event.ActorID, "", event.ActorID
		if event.ActorID == "player" {
			name, role, reference = playerName, "player_character", narrativeReference(settings, playerName)
		}
		for _, character := range characters {
			if character.EntityID == event.ActorID {
				name, role, reference = character.Name, character.Role, character.Name
				break
			}
		}
		speechScope := ""
		if event.EventType == "npc_dialogue" || event.SourceType == "player_public" {
			speechScope = "public_current_scene"
		} else if event.SourceType == "player_private" {
			speechScope = "private_recipient"
		}
		outcomeStatus := ""
		if event.EventType == "npc_action_result" || event.EventType == "player_action_result" {
			outcomeStatus = strings.TrimPrefix(event.SourceType, "action_")
		}
		result = append(result, narrativeEvent{OutcomeStatus: outcomeStatus, SpeechScope: speechScope, EventID: event.EventID, EventType: event.EventType, ActorID: event.ActorID, ActorName: name, ActorRole: role, NarrativeReference: reference, Stage: event.Stage, Content: event.Content})
	}
	return result
}

func publicCharacterContext(characters []Character, sceneCharacterIDs []string) string {
	inScene := make(map[string]bool, len(sceneCharacterIDs))
	for _, id := range sceneCharacterIDs {
		inScene[id] = true
	}
	type publicFact struct {
		EntityID string `json:"entity_id"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		InScene  bool   `json:"in_scene"`
	}
	facts := make([]publicFact, 0, len(characters))
	for _, character := range characters {
		facts = append(facts, publicFact{EntityID: character.EntityID, Name: character.Name, Role: character.Role, InScene: inScene[character.EntityID]})
	}
	data, _ := json.Marshal(facts)
	return string(data)
}

func formatBystanders(bystanders []string) string {
	if len(bystanders) == 0 {
		return "（无已记录背景人物）"
	}
	return strings.Join(bystanders, "、")
}

func generateNarrativeText(ctx context.Context, generator model.TextGenerator, system, input string, maxOutputTokens int) (string, int, error) {
	for attempt := 0; attempt < 2; attempt++ {
		requestSystem := system
		if attempt > 0 {
			requestSystem += "\n上一次响应不可用。请重新生成，只输出一段完整的故事正文。"
		}
		response, err := generator.GenerateText(ctx, model.TextRequest{System: requestSystem, Input: input, MaxInputTokens: 12000, MaxOutputTokens: maxOutputTokens, MaxResponseBytes: 1 << 20})
		if err != nil {
			if attempt == 0 && errors.Is(err, model.ErrInvalidTextResponse) {
				continue
			}
			return "", attempt, err
		}
		narrative, err := parseNarrativeText(response.Text)
		if err != nil {
			if attempt == 0 {
				continue
			}
			return "", attempt, err
		}
		return narrative, attempt, nil
	}
	return "", 1, ErrGenerationFailed
}

func parseNarrativeText(text string) (string, error) {
	text = cleanText(text)
	if text == "" {
		return "", fmt.Errorf("%w: narrative is empty", ErrGenerationFailed)
	}
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) < 3 || !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			return "", fmt.Errorf("%w: narrative has an incomplete code fence", ErrGenerationFailed)
		}
		text = cleanText(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	if strings.HasPrefix(text, "{") {
		if err := validateStrictJSON([]byte(text)); err != nil {
			return "", fmt.Errorf("%w: narrative JSON is invalid: %v", ErrGenerationFailed, err)
		}
		var legacy narrativeResult
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&legacy); err != nil {
			return "", fmt.Errorf("%w: narrative JSON does not match the legacy wrapper", ErrGenerationFailed)
		}
		text = cleanText(legacy.Narrative)
	}
	if text == "" {
		return "", fmt.Errorf("%w: narrative is empty", ErrGenerationFailed)
	}
	return text, nil
}

func coordinationContinuity(events []Event) string {
	completed := make([]Event, 0)
	for _, event := range events {
		if event.EventType == "npc_action_result" || event.EventType == "player_action_result" {
			completed = append(completed, event)
		}
	}
	if len(completed) > 12 {
		completed = completed[len(completed)-12:]
	}
	data, _ := json.Marshal(completed)
	return "连续状态规则：scene 是本轮结束后的简明状态快照，写人物位置、物件状态及仍成立的环境，不是动作回放或对白记录。此前已提交行动结果是连续状态的依据；按时间顺序接续，较新的明确结果覆盖同一事项的旧状态。上一场景中取碗、斟茶、递物等进行式描述，若对应结果已经完成，应写为完成后的状态，不能再次执行。输入与已完成动作重叠时，结合当前状态承接新意图。本轮未提出或未确认的新行动不能借 scene 补出；没有动作变化时沿用完成状态，不把人物移回原位置。\n此前已提交行动结果（仅作状态依据，不属于本轮待裁定行动）：" + string(data) + "\n"
}

func coordinationDecisionContext(decisions map[string]npcDecision, characters []Character) string {
	var parts []string
	for _, character := range sceneCharacters(characters) {
		decision := decisions[character.EntityID]
		parts = append(parts, fmt.Sprintf("%s（%s，%s）：speech=%q；action_intent=%q；silent=%t", character.Name, character.Role, character.EntityID, decision.Speech, decision.ActionIntent, decision.Silent))
	}
	if len(parts) == 0 {
		return "（暂无）"
	}
	return strings.Join(parts, "\n")
}

func characterIDs(items []Character) []string {
	result := make([]string, 0, len(items))
	for _, character := range items {
		result = append(result, character.EntityID)
	}
	return result
}

func availableCharacterIDs(items []Character) string {
	var ids []string
	for _, character := range items {
		ids = append(ids, fmt.Sprintf("%s=%s（%s）", character.EntityID, character.Name, character.Role))
	}
	return strings.Join(ids, "、")
}

func validateSceneCharacters(ids []string, characters []Character) error {
	available := make(map[string]bool, len(characters))
	for _, character := range characters {
		available[character.EntityID] = true
	}
	seen := make(map[string]bool, len(ids))
	for index, id := range ids {
		id = cleanText(id)
		if id == "" || !available[id] || seen[id] {
			return fmt.Errorf("%w: invalid scene character %q at index %d", ErrGenerationFailed, id, index)
		}
		ids[index] = id
		seen[id] = true
	}
	return nil
}

func normalizeSceneCharacters(ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = cleanText(id)
		if id != "player" {
			result = append(result, id)
		}
	}
	return result
}

func generateJSON(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, requiredFields ...string) error {
	_, err := generateJSONMetrics(ctx, generator, system, input, target, maxOutput, requiredFields...)
	return err
}

func generateJSONWithNullableFields(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, nullableFields []string, requiredFields ...string) error {
	_, err := generateJSONWithNullableFieldsMetrics(ctx, generator, system, input, target, maxOutput, nullableFields, requiredFields...)
	return err
}

func generateJSONMetrics(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, requiredFields ...string) (int, error) {
	return generateJSONWithNullableFieldsMetrics(ctx, generator, system, input, target, maxOutput, nil, requiredFields...)
}

func generateJSONWithNullableFieldsMetrics(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, nullableFields []string, requiredFields ...string) (int, error) {
	return generateJSONCheckedMetrics(ctx, generator, system, input, target, maxOutput, nullableFields, requiredFields, nil)
}

func generateJSONCheckedMetrics(ctx context.Context, generator model.TextGenerator, system, input string, target any, maxOutput int, nullableFields, requiredFields []string, check func() error) (int, error) {
	if t := reflect.TypeOf(target); t != nil && t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
		system += "\n机器可读字段合同（对象只使用以下字段；string表示字符串，[]表示数组，boolean表示布尔值，integer表示整数）：" + generatedFieldContract(t)
	}
	var lastValidation error
	for attempt := 0; attempt < 2; attempt++ {
		requestSystem := system
		if attempt > 0 {
			requestSystem += "\n上一次响应不是可接受的完整 JSON。请重新生成，只输出满足字段要求的单个 JSON 对象。"
			var detail *generationJSONError
			if errors.As(lastValidation, &detail) {
				requestSystem += "\n本地字段校验：" + detail.Error() + "。按本地字段类型生成；只使用输出合同列出的字段。"
			}
		}
		response, err := generator.GenerateText(ctx, model.TextRequest{System: requestSystem, Input: input, MaxInputTokens: 12000, MaxOutputTokens: maxOutput, MaxResponseBytes: 1 << 20})
		if err != nil {
			if attempt == 0 && errors.Is(err, model.ErrInvalidTextResponse) {
				continue
			}
			return attempt, err
		}
		lastValidation = decodeGeneratedJSON(response.Text, target, nullableFields, requiredFields)
		if lastValidation == nil && check != nil {
			lastValidation = check()
		}
		if recorder, ok := generator.(interface{ recordJSONValidation(error) }); ok {
			recorder.recordJSONValidation(lastValidation)
		}
		if err := lastValidation; err != nil {
			if attempt == 0 {
				continue
			}
			return attempt, err
		}
		return attempt, nil
	}
	return 1, ErrGenerationFailed
}

func generatedFieldContract(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		return generatedFieldContract(t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		fields := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			fields = append(fields, fmt.Sprintf("%q:%s", name, generatedFieldContract(f.Type)))
		}
		return "{" + strings.Join(fields, ",") + "}"
	case reflect.Slice:
		return "[" + generatedFieldContract(t.Elem()) + "]"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	default:
		return "integer"
	}
}

func decodeGeneratedJSON(text string, target any, nullableFields, requiredFields []string) error {
	if err := validateStrictJSON([]byte(text)); err != nil {
		return &generationJSONError{Code: "json_syntax_invalid", Cause: err}
	}
	if len(requiredFields) > 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &object); err != nil || object == nil {
			return &generationJSONError{Code: "json_object_required", Cause: ErrGenerationFailed}
		}
		nullable := make(map[string]bool, len(nullableFields))
		for _, field := range nullableFields {
			nullable[field] = true
		}
		for _, field := range requiredFields {
			raw, ok := object[field]
			if !ok {
				return &generationJSONError{Code: "json_required_field_missing", Field: field, Cause: ErrGenerationFailed}
			}
			if strings.EqualFold(strings.TrimSpace(string(raw)), "null") && !nullable[field] {
				return &generationJSONError{Code: "json_required_field_null", Field: field, Cause: ErrGenerationFailed}
			}
		}
	}
	value := reflect.ValueOf(target)
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		value.Elem().Set(reflect.Zero(value.Elem().Type()))
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return generatedDecodeError(err, target)
	}
	if validator, ok := target.(interface{ validateGeneratedFields() error }); ok {
		return validator.validateGeneratedFields()
	}
	return nil
}
