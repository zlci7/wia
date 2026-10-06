package turn

import (
	"fmt"
	"gameagent/backend/internal/plot"
	"slices"
	"time"

	wiaworld "gameagent/backend/internal/world"
)

type plotActionResolution struct {
	TimeMinutes         int                  `json:"time_minutes,omitempty"`
	Outcomes            []plotActionResult   `json:"outcomes"`
	SceneUpdates        []sceneUpdate        `json:"scene_updates"`
	StateEffects        []stateEffect        `json:"state_effects,omitempty"`
	RelationshipEffects []relationshipEffect `json:"relationship_effects,omitempty"`
	ItemTransfers       []itemTransferEffect `json:"item_transfers,omitempty"`
	Movements           []movementResult     `json:"movements,omitempty"`
}

type coordinatedPlotActions struct {
	output  Output
	extra   Output
	visible []wiaworld.Event
}

// Every attempt starts from the same facts. Validation only writes to its own
// event and perception storage; the caller publishes the accepted candidate.
func (s *Service) preparePlotActions(base Snapshot, run wiaworld.Run, rootID string, original Output, pending Output, allowed map[string][]string, resolved plotActionResolution, remainingMinutes int) (coordinatedPlotActions, error) {
	output, extra := original, pending
	extra.Events = slices.Clone(pending.Events)
	extra.Perceptions = slices.Clone(pending.Perceptions)
	var visible []wiaworld.Event
	var err error
	if base.Definition.Progression != nil && (resolved.TimeMinutes < 0 || resolved.TimeMinutes > remainingMinutes || len(resolved.Movements) > 0 && resolved.TimeMinutes == 0) {
		return coordinatedPlotActions{}, coordinationInvalid("world_action_time_invalid", "time_minutes", "bounded-actual-time-with-positive-movement-cost")
	}
	if base.Definition.Capabilities["spatial"] == 1 {
		for _, outcome := range resolved.Outcomes {
			if outcome.ActorInScene != nil {
				return coordinatedPlotActions{}, coordinationInvalid("spatial_plot_presence_unsupported", "outcomes.actor_in_scene", "omitted-when-authoritative-movements-are-enabled")
			}
		}
	}
	outcomes := make([]hostActionResult, 0, len(resolved.Outcomes))
	for _, o := range resolved.Outcomes {
		if base.Definition.Progression != nil && o.Projections == nil {
			return coordinatedPlotActions{}, coordinationInvalid("action_projections_required", "outcomes.projections", "explicit-personal-projections")
		}
		outcomes = append(outcomes, o.hostActionResult)
	}
	finalCharacters := append([]string{}, output.SceneCharacters...)
	movementHost := hostResult{Outcomes: outcomes, Movements: resolved.Movements, SceneUpdates: resolved.SceneUpdates}
	if base.Definition.Capabilities["spatial"] == 1 {
		positions, changes, derived, moveErr := applyMovements(base, movementHost.Movements, mechanicSources(extra.Events, movementHost.Outcomes))
		if moveErr != nil {
			return coordinatedPlotActions{}, moveErr
		}
		if err = validateMovementSceneUpdates(movementHost); err != nil {
			return coordinatedPlotActions{}, err
		}
		if err = validateSpatialOutcomeAudiences(base, positions, extra.Events, movementHost); err != nil {
			return coordinatedPlotActions{}, err
		}
		extra.Positions, extra.PositionChanges = positions, changes
		finalCharacters = derived
	} else {
		finalCharacters, err = plotActionPresence(output.SceneCharacters, extra.Events, resolved.Outcomes)
		if err != nil {
			return coordinatedPlotActions{}, err
		}
	}
	if err = validateSceneCharacters(finalCharacters, base.Characters); err != nil {
		return coordinatedPlotActions{}, err
	}
	for _, outcome := range resolved.Outcomes {
		if base.Definition.Capabilities["spatial"] == 1 {
			continue
		}
		for _, id := range outcome.Recipients {
			if !slices.Contains(allowed[outcome.ActionID], id) {
				if s.deps.Logger != nil {
					s.deps.Logger.Printf("story plot_actions validation failed: run_id=%q boundary=outcome_audience", run.RunID)
				}
				return coordinatedPlotActions{}, ErrGenerationFailed
			}
		}
	}
	results, err := appendHostOutcomes(&extra, run, base.Characters, base.Definition.BystanderRefs, outcomes)
	if err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("story plot_actions validation failed: run_id=%q boundary=action_correspondence", run.RunID)
		}
		return coordinatedPlotActions{}, err
	}
	for i := range results {
		results[i].Stage = 6
	}
	visible = append(visible, results...)
	for i := range extra.Events {
		if extra.Events[i].EventType == "npc_action_result" || extra.Events[i].EventType == "action_perceived" {
			extra.Events[i].Stage = 6
		}
	}
	for i := range extra.Perceptions {
		if extra.Perceptions[i].Stage == 3 {
			extra.Perceptions[i].Stage = 6
		}
	}
	if err = applyMechanicEffects(base, &extra, (hostResult{Outcomes: outcomes, Movements: resolved.Movements, StateEffects: resolved.StateEffects, RelationshipEffects: resolved.RelationshipEffects, ItemTransfers: resolved.ItemTransfers}).mechanics(base, extra)); err != nil {
		return coordinatedPlotActions{}, err
	}
	sources := plotSceneSources(output, nil)
	for i, outcome := range resolved.Outcomes {
		action, _ := EventByID(extra.Events, outcome.ActionID)
		sources[outcome.ActionID] = projectionSource(action, outcome.hostActionResult, i)
	}
	version := output.SceneVersion
	if err := applyPlotSceneUpdates(&output, sources, resolved.SceneUpdates); err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("story plot_actions validation failed: run_id=%q boundary=scene_sources detail=%q", run.RunID, err.Error())
		}
		return coordinatedPlotActions{}, err
	}
	if !slices.Equal(output.SceneCharacters, finalCharacters) && output.SceneVersion == version {
		output.SceneVersion++
	}
	output.SceneCharacters = finalCharacters
	if base.Definition.Progression != nil && resolved.TimeMinutes > 0 {
		output.elapsedMinutes += resolved.TimeMinutes
		output.Clock, err = plot.AdvanceClock(output.Clock, resolved.TimeMinutes)
		if err != nil {
			return coordinatedPlotActions{}, coordinationInvalid("world_clock_invalid", "time_minutes", "valid-world-date-and-bounded-time")
		}
		elapsed := wiaworld.Event{EventID: rootID + ":action-clock", EventType: "time_advanced", ActorID: "world", Content: fmt.Sprintf("随后实际经过%d分钟，当前游戏时间为%s。", resolved.TimeMinutes, output.Clock), RunID: run.RunID, Stage: 6, SceneVersion: output.SceneVersion, SourceType: "world_clock", CreatedAt: time.Now().UTC()}
		extra.Events = append(extra.Events, elapsed)
		visible = append(visible, elapsed)
	}
	return coordinatedPlotActions{output: output, extra: extra, visible: visible}, nil
}
