package turn

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/story"
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
			return coordinationInvalid("scene_character_invalid", fmt.Sprintf("scene_characters[%d]", index), "unique-listed-important-character-id")
		}
		ids[index] = id
		seen[id] = true
	}
	return nil
}

type hostResult struct {
	EventOpportunity    *eventOpportunity    `json:"event_opportunity,omitempty"`
	InterruptSources    []string             `json:"interrupt_source_ids,omitempty"`
	TimeMinutes         int                  `json:"time_minutes"`
	Scene               string               `json:"scene"`
	SceneCharacters     []string             `json:"scene_characters"`
	SceneUpdates        []sceneUpdate        `json:"scene_updates"`
	Outcomes            []hostActionResult   `json:"outcomes"`
	Movements           []movementResult     `json:"movements,omitempty"`
	StateEffects        []stateEffect        `json:"state_effects,omitempty"`
	RelationshipEffects []relationshipEffect `json:"relationship_effects,omitempty"`
	ItemTransfers       []itemTransferEffect `json:"item_transfers,omitempty"`
}

type movementResult struct {
	EntityID string   `json:"entity_id"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Route    []string `json:"route"`
	ActionID string   `json:"action_id"`
}

type hostActionResult struct {
	ActionID   string   `json:"action_id"`
	Status     string   `json:"status"`
	Content    string   `json:"content"`
	Recipients []string `json:"recipients"`
	// Bystanders names the defined passers-by this outcome actually involved, so
	// their personal experience has a real attribution instead of a guess from prose.
	Bystanders  []string           `json:"bystanders,omitempty"`
	Projections []actionProjection `json:"projections,omitempty"`
}

type actionProjection struct {
	Recipient string `json:"recipient"`
	Content   string `json:"content"`
}

const (
	coordinationPromptVersion = "story.coordination.v20"
)

func (s *Service) coordinateStage(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, intent TurnIntent, recipient string, output *Output) (hostResult, []wiaworld.Event, error) {
	coordinationStarted := time.Now()
	resolved, coordinationRepairs, err := s.coordinateTurn(ctx, generator, *snapshot, run, intent, *output)
	if err != nil {
		return hostResult{}, nil, AtStage(StageCoordination, err)
	}
	s.host.LogStage(snapshot.Summary.WorldID, run, StageCoordination, "coordinate_scene", "scene", 0, coordinationPromptVersion, EventIDs(output.Events), recipient, coordinationRepairs, time.Since(coordinationStarted))
	*output = resolved.output
	snapshot.SceneViews = output.SceneViews
	snapshot.SceneVersion = output.SceneVersion
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		if err := applySpatialOutput(snapshot, *output); err != nil {
			return hostResult{}, nil, err
		}
	}
	snapshot.States = cloneStates(output.States)
	snapshot.Relationships = slices.Clone(output.Relationships)
	snapshot.Items = cloneItems(output.Items)
	if snapshot.Perceptions == nil {
		snapshot.Perceptions = map[string][]wiaworld.Perception{}
	}
	for _, perception := range output.Perceptions {
		if perception.RecipientID == "player" {
			snapshot.Perceptions["player"] = append(snapshot.Perceptions["player"], perception)
		}
	}
	return resolved.host, resolved.visible, nil
}

func applySpatialOutput(snapshot *Snapshot, output Output) error {
	snapshot.Positions = clonePositions(output.Positions)
	if snapshot.PositionSources == nil {
		snapshot.PositionSources = map[string]string{}
	}
	for _, change := range output.PositionChanges {
		snapshot.PositionSources[change.EntityID] = change.SourceEventID
	}
	return refreshSpatialProjection(snapshot)
}

type coordinatedTurn struct {
	host    hostResult
	output  Output
	visible []wiaworld.Event
}

// Each response is resolved against the same input. Rejected candidates never
// share writable event or perception storage with the accepted turn.
func prepareCoordination(snapshot Snapshot, run wiaworld.Run, intent TurnIntent, original Output, host hostResult) (coordinatedTurn, error) {
	if snapshot.Definition.Progression != nil {
		for _, outcome := range host.Outcomes {
			if outcome.Projections == nil {
				return coordinatedTurn{}, coordinationInvalid("action_projections_required", "outcomes.projections", "explicit-personal-projections")
			}
		}
	}
	if intent.Private() {
		for index, outcome := range host.Outcomes {
			if outcome.ActionID != inputPrefix(run)+":player-action" {
				continue
			}
			if len(outcome.Bystanders) > 0 {
				return coordinatedTurn{}, coordinationInvalid("private_player_outcome_bystander", fmt.Sprintf("outcomes[%d].bystanders", index), "empty-for-private-player-action")
			}
			for _, recipient := range outcome.Recipients {
				if recipient != "player" && recipient != intent.AddresseeID {
					return coordinatedTurn{}, coordinationInvalid("private_player_outcome_recipient", fmt.Sprintf("outcomes[%d].recipients", index), "player-or-private-addressee")
				}
			}
		}
	}
	output := original
	output.Events = slices.Clone(original.Events)
	output.Perceptions = slices.Clone(original.Perceptions)
	output.Clock = AdvanceClock(snapshot.Summary.Clock, host.TimeMinutes)
	output.elapsedMinutes += host.TimeMinutes
	output.SceneCharacters = append([]string(nil), host.SceneCharacters...)
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		var err error
		output.Positions, output.PositionChanges, output.SceneCharacters, err = applyMovements(snapshot, output.Events, host)
		if err != nil {
			return coordinatedTurn{}, err
		}
		if err = validateMovementSceneUpdates(host); err != nil {
			return coordinatedTurn{}, err
		}
		if err = validateSpatialOutcomeAudiences(snapshot, output.Positions, output.Events, host); err != nil {
			return coordinatedTurn{}, err
		}
		host.SceneCharacters = append([]string(nil), output.SceneCharacters...)
	}
	if len(host.SceneUpdates) > 0 || !reflect.DeepEqual(output.SceneCharacters, CharacterIDs(InScene(snapshot.Characters))) || SceneFor(snapshot, "player") != snapshot.Summary.Scene {
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
	resultBystanders := snapshot.BystanderRefs
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		// Per-outcome spatial validation above is the authorization boundary. The
		// writer needs the complete catalog to persist an authorized arrival witness.
		resultParticipants = snapshot.Characters
		resultBystanders = snapshot.Definition.BystanderRefs
	}
	visibleOutcomes, err := appendHostOutcomes(&output, run, resultParticipants, resultBystanders, host.Outcomes)
	if err != nil {
		return coordinatedTurn{}, err
	}
	ruleEvent, err := applyActionResolution(snapshot, &output, host)
	if err != nil {
		return coordinatedTurn{}, err
	}
	if ruleEvent.EventID != "" {
		visibleOutcomes = append(visibleOutcomes, ruleEvent)
	}
	if err = applyMechanicEffects(snapshot, &output, host); err != nil {
		return coordinatedTurn{}, err
	}
	output.SceneViews, err = applySceneUpdates(snapshot, run, intent, output, host)
	if err != nil {
		return coordinatedTurn{}, err
	}
	snapshot.SceneViews = output.SceneViews
	output.Scene = SceneFor(snapshot, "player")
	// The identifier behind the scene text, kept so presence is never decided by
	// comparing prose. A description that names no known place leaves the location
	// unchanged rather than clearing it.
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		output.SceneLocation = output.Positions["player"]
	} else {
		output.SceneLocation = story.LocationIDFor(snapshot.Definition, output.Scene)
		if output.SceneLocation == "" {
			output.SceneLocation = snapshot.SceneLocation
		}
	}
	playerNarrativeInput := run.Input
	visibleEvents := VisibleTurnEvents(output.Events, visibleOutcomes, playerNarrativeInput)
	return coordinatedTurn{host: host, output: output, visible: visibleEvents}, nil
}

func validateMovementSceneUpdates(host hostResult) error {
	for movementIndex, movement := range host.Movements {
		found := false
		for _, update := range host.SceneUpdates {
			if slices.Contains(update.Recipients, movement.EntityID) && slices.Contains(update.SourceIDs, movement.ActionID) {
				found = true
				break
			}
		}
		if !found {
			return coordinationInvalid("movement_scene_update_missing", fmt.Sprintf("movements[%d]", movementIndex), "scene-update-for-moved-entity-sourced-by-action")
		}
	}
	return nil
}

// validateSpatialOutcomeAudiences ties each model-proposed projection to the
// location where that action can be witnessed. An ordinary result stays at the
// actor's starting place. A movement result may additionally be witnessed at its
// validated destination; the whole turn's before/after roster is never a blanket
// audience grant.
func validateSpatialOutcomeAudiences(snapshot Snapshot, finalPositions map[string]string, events []wiaworld.Event, host hostResult) error {
	actions := map[string]wiaworld.Event{}
	for _, event := range events {
		if event.EventType == "npc_action_intent" || event.EventType == "player_action_intent" {
			actions[event.EventID] = event
		}
	}
	movements := map[string]movementResult{}
	for _, movement := range host.Movements {
		movements[wire.Clean(movement.ActionID)] = movement
	}
	characters := map[string]bool{}
	for _, character := range snapshot.Characters {
		characters[character.EntityID] = true
	}
	bystanders := map[string]bool{}
	for _, bystander := range snapshot.Definition.BystanderRefs {
		bystanders[bystander.BystanderID] = true
	}
	allowed := func(action wiaworld.Event, recipient string) bool {
		if recipient == action.ActorID {
			return true
		}
		actorStart := snapshot.Positions[action.ActorID]
		if actorStart != "" && snapshot.Positions[recipient] == actorStart {
			return true
		}
		movement, moved := movements[action.EventID]
		return moved && movement.To != "" && finalPositions[recipient] == movement.To
	}
	for outcomeIndex, outcome := range host.Outcomes {
		action, ok := actions[wire.Clean(outcome.ActionID)]
		if !ok {
			continue
		}
		for recipientIndex, recipient := range outcome.Recipients {
			recipient = wire.Clean(recipient)
			if recipient != "player" && !characters[recipient] {
				continue
			}
			if !allowed(action, recipient) {
				return coordinationInvalid("action_outcome_spatial_recipient", fmt.Sprintf("outcomes[%d].recipients[%d]", outcomeIndex, recipientIndex), "entity-at-action-origin-or-that-movement-destination")
			}
		}
		for bystanderIndex, bystander := range outcome.Bystanders {
			bystander = wire.Clean(bystander)
			if !bystanders[bystander] {
				continue
			}
			if !allowed(action, bystander) {
				return coordinationInvalid("action_outcome_spatial_bystander", fmt.Sprintf("outcomes[%d].bystanders[%d]", outcomeIndex, bystanderIndex), "bystander-at-action-origin-or-that-movement-destination")
			}
		}
	}
	return nil
}

// resolveSceneResult advances the plot and the generated events and folds their
// player-visible results into the events the narration stage renders.
//
// The visible events are written into the output rather than returned, because narration
// reads them and a return value would have to be carried across a stage boundary by
// whoever calls what is in between.
func (s *Service) resolveSceneResult(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, host hostResult, output *Output) error {
	if snapshot.Plot != nil || snapshot.Definition.EventGeneration != nil || snapshot.Definition.Progression != nil {
		elapsed := wiaworld.Event{EventID: run.RunID + ":clock", EventType: "time_advanced", ActorID: "world", Content: fmt.Sprintf("本轮实际经过 %d 分钟，从%s到%s。更长的等待请求仅执行到这个时点，剩余时段尚未发生。", host.TimeMinutes, snapshot.Summary.Clock, output.Clock), RunID: run.RunID, Stage: 3, SceneVersion: output.SceneVersion, SourceType: "world_clock", CreatedAt: time.Now().UTC()}
		output.Events = append(output.Events, elapsed)
		output.VisibleEvents = append(output.VisibleEvents, elapsed)
	}
	plotEvents, err := s.advancePlot(ctx, generator, *snapshot, run, output)
	if err != nil {
		return AtStage(StageCoordination, err)
	}
	output.VisibleEvents = append(output.VisibleEvents, plotEvents...)
	generatedVisible, err := s.advanceGeneratedEvents(ctx, generator, *snapshot, run, host.EventOpportunity, output)
	if err != nil {
		return AtStage(StageCoordination, err)
	}
	output.VisibleEvents = append(output.VisibleEvents, generatedVisible...)
	snapshot.SceneViews, snapshot.SceneVersion = output.SceneViews, output.SceneVersion
	snapshot.States = cloneStates(output.States)
	snapshot.Relationships = slices.Clone(output.Relationships)
	snapshot.Items = cloneItems(output.Items)
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		if err := applySpatialOutput(snapshot, *output); err != nil {
			return AtStage(StageCoordination, err)
		}
	}
	// Narration sees the final workspace, including knowledge granted by later
	// world events. Other recipients' private projections remain outside this view.
	if snapshot.Perceptions == nil {
		snapshot.Perceptions = map[string][]wiaworld.Perception{}
	}
	known := map[string]bool{}
	for _, perception := range snapshot.Perceptions["player"] {
		known[perception.SourceEventID] = true
	}
	for _, perception := range output.Perceptions {
		if perception.RecipientID == "player" && !known[perception.SourceEventID] {
			snapshot.Perceptions["player"] = append(snapshot.Perceptions["player"], perception)
			known[perception.SourceEventID] = true
		}
	}
	return nil
}

func appendHostOutcomes(output *Output, run wiaworld.Run, participants []wiaworld.Character, bystanders []story.Bystander, outcomes []hostActionResult) ([]wiaworld.Event, error) {
	actions := make(map[string]wiaworld.Event)
	for _, event := range output.Events {
		if event.EventType == "npc_action_intent" || event.EventType == "player_action_intent" {
			actions[event.EventID] = event
		}
	}
	if len(outcomes) != len(actions) {
		return nil, coordinationInvalid("action_outcome_count", "outcomes", fmt.Sprintf("exactly-%d-outcomes-one-per-action", len(actions)))
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
		field := fmt.Sprintf("outcomes[%d]", index)
		if !ok || seen[outcome.ActionID] {
			return nil, coordinationInvalid("action_outcome_id", field+".action_id", "unique-pending-action-id")
		}
		if outcome.Content == "" {
			return nil, coordinationInvalid("action_outcome_content", field+".content", "nonempty-string")
		}
		if outcome.Status != "succeeded" && outcome.Status != "failed" && outcome.Status != "partial" && outcome.Status != "not_executed" {
			return nil, coordinationInvalid("action_outcome_status", field+".status", "succeeded|failed|partial|not_executed")
		}
		if outcome.Recipients == nil {
			return nil, coordinationInvalid("action_outcome_recipients", field+".recipients", "array")
		}
		seen[outcome.ActionID] = true
		recipients := make(map[string]bool, len(outcome.Recipients)+1)
		for i, id := range outcome.Recipients {
			id = wire.Clean(id)
			if id != "player" && !participantIDs[id] {
				return nil, coordinationInvalid("action_outcome_recipient", fmt.Sprintf("%s.recipients[%d]", field, i), "player-or-participating-important-character")
			}
			recipients[id] = true
		}
		// An actor always observes the resolved result of its own attempt.
		recipients[action.ActorID] = true
		resultID := fmt.Sprintf("%s:result:%d", outcome.ActionID, index+1)
		resultEvent := wiaworld.Event{EventID: resultID, EventType: "npc_action_result", ActorID: action.ActorID, TargetID: action.TargetID, Content: outcome.Content, RunID: run.RunID, Stage: 3, SceneVersion: output.SceneVersion, SourceType: "action_" + outcome.Status, ProjectionParentID: action.EventID, CreatedAt: time.Now().UTC()}
		if action.ActorID == "player" {
			resultEvent.EventType = "player_action_result"
		}
		output.Events = append(output.Events, resultEvent)
		for i, id := range outcome.Bystanders {
			if id == "" || recipients[id] || !definedBystanders[id] {
				return nil, coordinationInvalid("action_outcome_bystander", fmt.Sprintf("%s.bystanders[%d]", field, i), "unique-defined-bystander")
			}
			recipients[id] = true
		}
		texts, err := actionProjectionText(outcome, recipients)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(recipients))
		for id := range recipients {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			projection := resultEvent
			projection.EventID = resultID + ":projection:" + id
			projection.EventType = "action_perceived"
			projection.TargetID, projection.Content, projection.ProjectionParentID = id, texts[id], resultID
			output.Events = append(output.Events, projection)
			output.Perceptions = append(output.Perceptions, wiaworld.Perception{RecipientID: id, SourceEventID: projection.EventID, SourceType: projection.SourceType, Content: projection.Content, Stage: 3, SceneVersion: output.SceneVersion, CreatedAt: projection.CreatedAt})
			if id == "player" {
				visible = append(visible, projection)
			}
		}
	}
	return visible, nil
}

func coordinationInvalid(code, field, expected string) error {
	return &GenerationError{Code: code, Field: field, Expected: expected, Cause: ErrGenerationFailed}
}

func applyMovements(snapshot Snapshot, events []wiaworld.Event, host hostResult) (map[string]string, []PositionChange, []string, error) {
	positions := clonePositions(snapshot.Positions)
	actions := map[string]wiaworld.Event{}
	for _, event := range events {
		if event.EventType == "npc_action_intent" || event.EventType == "player_action_intent" {
			actions[event.EventID] = event
		}
	}
	type outcomeInfo struct {
		status string
		index  int
	}
	outcomes := map[string]outcomeInfo{}
	for index, outcome := range host.Outcomes {
		outcomes[wire.Clean(outcome.ActionID)] = outcomeInfo{status: strings.ToLower(wire.Clean(outcome.Status)), index: index}
	}
	graph := story.PlaceGraph(snapshot.Definition)
	changes := make([]PositionChange, 0, len(host.Movements))
	seenEntity, seenAction := map[string]bool{}, map[string]bool{}
	for index, movement := range host.Movements {
		field := fmt.Sprintf("movements[%d]", index)
		movement.EntityID, movement.From, movement.To, movement.ActionID = wire.Clean(movement.EntityID), wire.Clean(movement.From), wire.Clean(movement.To), wire.Clean(movement.ActionID)
		action, exists := actions[movement.ActionID]
		outcome, settled := outcomes[movement.ActionID]
		if !exists || !settled || seenAction[movement.ActionID] {
			return nil, nil, nil, coordinationInvalid("movement_source_invalid", field+".action_id", "unique-current-action-with-outcome")
		}
		if movement.EntityID == "" || positions[movement.EntityID] == "" || seenEntity[movement.EntityID] || action.ActorID != movement.EntityID {
			return nil, nil, nil, coordinationInvalid("movement_entity_invalid", field+".entity_id", "positioned-actor-of-source-action")
		}
		if outcome.status != "succeeded" && outcome.status != "partial" {
			return nil, nil, nil, coordinationInvalid("movement_outcome_invalid", field+".action_id", "succeeded-or-partial-outcome")
		}
		if positions[movement.EntityID] != movement.From || movement.From == movement.To {
			return nil, nil, nil, coordinationInvalid("movement_origin_invalid", field+".from", "current-location-and-different-destination")
		}
		if err := wiaworld.ValidateRoute(movement.Route, movement.From, movement.To, graph); err != nil {
			return nil, nil, nil, coordinationInvalid("movement_route_invalid", field+".route", "directed-connected-place-route")
		}
		seenEntity[movement.EntityID], seenAction[movement.ActionID] = true, true
		positions[movement.EntityID] = movement.To
		changes = append(changes, PositionChange{
			EntityID: movement.EntityID, To: movement.To, ActionID: movement.ActionID,
			SourceEventID:         fmt.Sprintf("%s:result:%d", movement.ActionID, outcome.index+1),
			PreviousSourceEventID: snapshot.PositionSources[movement.EntityID],
		})
	}
	return positions, changes, spatialSceneCharacters(snapshot.Characters, positions), nil
}

func spatialSceneCharacters(characters []wiaworld.Character, positions map[string]string) []string {
	playerLocation := positions["player"]
	result := []string{}
	for _, character := range characters {
		if playerLocation != "" && positions[character.EntityID] == playerLocation {
			result = append(result, character.EntityID)
		}
	}
	return result
}

func (s *Service) coordinateTurn(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, intent TurnIntent, output Output) (coordinatedTurn, int, error) {
	if generator == nil {
		return coordinatedTurn{}, 0, ErrModelNotConfigured
	}
	material := composeCoordination(snapshot, run, intent, output.Decisions, output.Events, strings.Join(output.PublicReplies, "\n"), output.ActionResolution)
	generator = s.generator(generator, material, snapshot, run, "coordination", "coordinator", 3, coordinationPromptVersion)
	input := material.Required
	var result hostResult
	var resolved coordinatedTurn
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()
	check := func() error {
		if err := validateCoordination(&result, snapshot, run, intent, output.Events); err != nil {
			return err
		}
		if err := validateActionResolutionOutcome(output, result.Outcomes); err != nil {
			return err
		}
		var err error
		resolved, err = prepareCoordination(snapshot, run, intent, output, result)
		return err
	}
	requiredFields := []string{"time_minutes", "scene", "scene_characters", "outcomes", "scene_updates"}
	requiredFields = append(requiredFields, coordinationCapabilityFields(snapshot)...)
	repairCount, err := GenerateJSONCheckedMetrics(callCtx, generator, material.System, input, &result, structuredTurnOutputTokens, nil, requiredFields, check)
	if err != nil {
		return coordinatedTurn{}, repairCount, err
	}
	return resolved, repairCount, nil
}

func validateCoordination(result *hostResult, snapshot Snapshot, run wiaworld.Run, intent TurnIntent, events []wiaworld.Event) error {
	result.Scene = wire.Clean(result.Scene)
	if intent.WaitMinutes > 0 {
		if result.TimeMinutes > min(intent.WaitMinutes, PlotTimeLimit(snapshot)) {
			return coordinationInvalid("wait_duration_exceeded", "time_minutes", fmt.Sprintf("integer:0..%d", min(intent.WaitMinutes, PlotTimeLimit(snapshot))))
		}
		for i, id := range result.InterruptSources {
			e, ok := EventByID(events, id)
			current := ok && e.RunID == run.RunID && e.Stage >= 1 && e.Stage <= 2
			_, committed := EventByID(snapshot.Events, id)
			if !current && !committed {
				return coordinationInvalid("wait_interruption_source", fmt.Sprintf("interrupt_source_ids[%d]", i), "current-stage-1-or-2-event-or-committed-event-id")
			}
		}
		if result.TimeMinutes < min(intent.WaitMinutes, PlotTimeLimit(snapshot)) && len(result.InterruptSources) == 0 {
			return coordinationInvalid("wait_interruption_missing", "interrupt_source_ids", "nonempty-evidence-for-shortened-wait")
		}
	}
	if result.Scene == "" {
		return coordinationInvalid("scene_required", "scene", "nonempty-string")
	}
	if result.TimeMinutes < 0 || result.TimeMinutes > PlotTimeLimit(snapshot) {
		return coordinationInvalid("scene_time_invalid", "time_minutes", fmt.Sprintf("integer:0..%d", PlotTimeLimit(snapshot)))
	}
	result.SceneCharacters = NormalizeSceneCharacters(result.SceneCharacters)
	if err := validateMechanicCapabilities(snapshot, *result); err != nil {
		return err
	}
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		if result.Movements == nil {
			return coordinationInvalid("movements_required", "movements", "array")
		}
		if snapshot.Definition.Progression != nil && len(result.Movements) > 0 && result.TimeMinutes == 0 {
			return coordinationInvalid("movement_time_invalid", "time_minutes", "positive-elapsed-time-for-movement")
		}
		return nil
	}
	if len(result.Movements) > 0 {
		return coordinationInvalid("movements_unsupported", "movements", "empty-or-omitted-for-world-without-spatial-capability")
	}
	return validateSceneCharacters(result.SceneCharacters, snapshot.Characters)
}

type sceneUpdate struct {
	Content    string   `json:"content"`
	SourceIDs  []string `json:"source_ids"`
	Recipients []string `json:"recipients"`
}

// Opportunities reference resolved actions, never literary additions or reads.
type eventOpportunity struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	ActionID string `json:"action_id"`
}
