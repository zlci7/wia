package turn

import (
	"context"
	"fmt"
	"reflect"
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
			return fmt.Errorf("%w: invalid scene character %q at index %d", ErrGenerationFailed, id, index)
		}
		ids[index] = id
		seen[id] = true
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
	// Bystanders names the defined passers-by this outcome actually involved, so
	// their personal experience has a real attribution instead of a guess from prose.
	Bystanders []string `json:"bystanders,omitempty"`
}

const (
	coordinationPromptVersion = "story.coordination.v15"
)

// coordinateStage resolves the scene: time, roster, action outcomes and the
// scene views, and returns the events the player can perceive so far.
func (s *Service) coordinate(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, intent TurnIntent, output *Output) error {
	participants := InScene(snapshot.Characters)
	host, visibleEvents, err := s.coordinateStage(ctx, generator, snapshot, run, intent, participants, intent.AddresseeID, output)
	if err != nil {
		return err
	}
	output.VisibleEvents = append(output.VisibleEvents, visibleEvents...)
	return s.resolveSceneResult(ctx, generator, snapshot, run, host, output)
}

func (s *Service) coordinateStage(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, intent TurnIntent, participants []wiaworld.Character, recipient string, output *Output) (hostResult, []wiaworld.Event, error) {
	publicReplies := strings.Join(output.PublicReplies, "\n")
	coordinationStarted := time.Now()
	host, coordinationRepairs, err := s.coordinateTurn(ctx, generator, *snapshot, run, intent, output.Decisions, output.Events, publicReplies)
	if err != nil {
		return hostResult{}, nil, AtStage(StageCoordination, err)
	}
	s.host.LogStage(snapshot.Summary.WorldID, run, StageCoordination, "coordinate_scene", "scene", 0, coordinationPromptVersion, EventIDs(output.Events), recipient, coordinationRepairs, time.Since(coordinationStarted))
	output.Clock = AdvanceClock(snapshot.Summary.Clock, host.TimeMinutes)
	output.SceneCharacters = append([]string(nil), host.SceneCharacters...)
	if len(host.SceneUpdates) > 0 || !reflect.DeepEqual(output.SceneCharacters, CharacterIDs(participants)) || SceneFor(*snapshot, "player") != snapshot.Summary.Scene {
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
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("story coordination validation failed: run_id=%q boundary=action_outcomes", run.RunID)
		}
		return hostResult{}, nil, AtStage(StageCoordination, err)
	}
	output.SceneViews, err = applySceneUpdates(*snapshot, run, intent, *output, host)
	if err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("story coordination validation failed: run_id=%q boundary=scene_sources", run.RunID)
		}
		return hostResult{}, nil, AtStage(StageCoordination, err)
	}
	snapshot.SceneViews = output.SceneViews
	output.Scene = SceneFor(*snapshot, "player")
	// The identifier behind the scene text, kept so presence is never decided by
	// comparing prose. A description that names no known place leaves the location
	// unchanged rather than clearing it.
	output.SceneLocation = story.LocationIDFor(snapshot.Definition, output.Scene)
	if output.SceneLocation == "" {
		output.SceneLocation = snapshot.SceneLocation
	}
	snapshot.SceneVersion = output.SceneVersion
	playerNarrativeInput := run.Input
	visibleEvents := VisibleTurnEvents(output.Events, visibleOutcomes, playerNarrativeInput)
	return host, visibleEvents, nil
}

// resolveSceneResult advances the plot and the generated events and folds their
// player-visible results into the events the narration stage renders.
//
// The visible events are written into the output rather than returned, because narration
// reads them and a return value would have to be carried across a stage boundary by
// whoever calls what is in between.
func (s *Service) resolveSceneResult(ctx context.Context, generator model.TextGenerator, snapshot *Snapshot, run wiaworld.Run, host hostResult, output *Output) error {
	if snapshot.Plot != nil || snapshot.Definition.EventGeneration != nil {
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
		return nil, fmt.Errorf("%w: outcome count %d does not match action count %d", ErrGenerationFailed, len(outcomes), len(actions))
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
			return nil, fmt.Errorf("%w: invalid outcome at index %d", ErrGenerationFailed, index)
		}
		seen[outcome.ActionID] = true
		recipients := make(map[string]bool, len(outcome.Recipients)+1)
		for _, id := range outcome.Recipients {
			id = wire.Clean(id)
			if id != "player" && !participantIDs[id] {
				return nil, fmt.Errorf("%w: outcome %q has unknown recipient %q", ErrGenerationFailed, outcome.ActionID, id)
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
				return nil, fmt.Errorf("%w: outcome %q attributes an undefined bystander %q", ErrGenerationFailed, outcome.ActionID, id)
			}
			involved[id] = true
			output.Perceptions = append(output.Perceptions, wiaworld.Perception{RecipientID: id, SourceEventID: resultID, SourceType: "action_" + outcome.Status, Content: outcome.Content, Stage: 3, SceneVersion: output.SceneVersion, CreatedAt: time.Now().UTC()})
		}
	}
	return visible, nil
}

func (s *Service) coordinateTurn(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, intent TurnIntent, decisions map[string]NPCDecision, events []wiaworld.Event, publicReplies string) (hostResult, int, error) {
	if generator == nil {
		return hostResult{}, 0, ErrModelNotConfigured
	}
	material := composeCoordination(snapshot, run, intent, decisions, events, publicReplies)
	generator = s.generator(generator, material, snapshot, run, "coordination", "coordinator", 3, coordinationPromptVersion)
	input := material.Required
	var result hostResult
	callCtx, callCancel := context.WithTimeout(ctx, 60*time.Second)
	defer callCancel()
	repairCount, err := GenerateJSONMetrics(callCtx, generator, material.System, input, &result, structuredTurnOutputTokens, "time_minutes", "scene", "scene_characters", "outcomes", "scene_updates")
	if err != nil {
		return hostResult{}, repairCount, err
	}
	result.Scene = wire.Clean(result.Scene)
	if intent.WaitMinutes > 0 {
		if result.TimeMinutes > min(intent.WaitMinutes, PlotTimeLimit(snapshot)) {
			return hostResult{}, repairCount, fmt.Errorf("%w: waiting exceeds requested duration", ErrGenerationFailed)
		}
		for _, id := range result.InterruptSources {
			e, ok := EventByID(events, id)
			current := ok && e.RunID == run.RunID && e.Stage >= 1 && e.Stage <= 2
			_, committed := EventByID(snapshot.Events, id)
			if !current && !committed {
				if s.deps.Logger != nil {
					s.deps.Logger.Printf("story coordination validation failed: run_id=%q boundary=wait_interruption_source", run.RunID)
				}
				return hostResult{}, repairCount, fmt.Errorf("%w: invalid wait interruption source", ErrGenerationFailed)
			}
		}
		if result.TimeMinutes < min(intent.WaitMinutes, PlotTimeLimit(snapshot)) && len(result.InterruptSources) == 0 {
			if s.deps.Logger != nil {
				s.deps.Logger.Printf("story coordination validation failed: run_id=%q boundary=wait_shortened", run.RunID)
			}
			return hostResult{}, repairCount, fmt.Errorf("%w: waiting shortened without interruption evidence", ErrGenerationFailed)
		}
	}
	if result.Scene == "" || result.TimeMinutes < 0 || result.TimeMinutes > PlotTimeLimit(snapshot) || result.SceneCharacters == nil || result.Outcomes == nil || result.SceneUpdates == nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("story coordination validation failed: run_id=%q boundary=required_fields time_minutes=%d limit=%d", run.RunID, result.TimeMinutes, PlotTimeLimit(snapshot))
		}
		return hostResult{}, repairCount, fmt.Errorf("%w: invalid scene coordination fields", ErrGenerationFailed)
	}
	result.SceneCharacters = NormalizeSceneCharacters(result.SceneCharacters)
	if err := validateSceneCharacters(result.SceneCharacters, snapshot.Characters); err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("story coordination validation failed: run_id=%q boundary=scene_character_id", run.RunID)
		}
		return hostResult{}, repairCount, err
	}
	return result, repairCount, nil
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
