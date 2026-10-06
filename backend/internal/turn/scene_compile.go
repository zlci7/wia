package turn

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"gameagent/backend/internal/plot"
	wiaworld "gameagent/backend/internal/world"
)

type sceneActorExpansion struct {
	EntityID  string
	EntityIDs []string
}

func (e *sceneActorExpansion) Error() string { return "scene participant material required" }

// compileScene validates against isolated working copies. Neither accepted
// prefixes nor rejected whole drafts can mutate the loaded world snapshot.
func compileScene(snapshot Snapshot, run wiaworld.Run, draft *SceneDraft, selected []string, ledger *SourceLedger, resolution *ActionResolution) (Output, error) {
	return compileSceneWorkspace(snapshot, run, draft, selected, ledger, resolution, false)
}

func compileSceneWorkspace(snapshot Snapshot, run wiaworld.Run, draft *SceneDraft, selected []string, ledger *SourceLedger, resolution *ActionResolution, prefix bool) (Output, error) {
	if !prefix {
		if err := draft.validateGeneratedFields(); err != nil {
			return Output{}, err
		}
	} else {
		if err := validateSceneBeats(draft.Beats, draft.ElapsedMinutes); err != nil {
			return Output{}, err
		}
	}
	if err := validateSceneInput(draft, run); err != nil {
		return Output{}, err
	}
	if !prefix {
		if err := validateSceneInputOutcomes(draft); err != nil {
			return Output{}, err
		}
	}
	if draft.ElapsedMinutes > PlotTimeLimit(snapshot) {
		return Output{}, coordinationInvalid("scene_time_invalid", "elapsed_minutes", "within-current-world-window")
	}
	work := snapshot
	work.sceneEntities = slices.Clone(selected)
	work.Characters = slices.Clone(snapshot.Characters)
	work.Positions = clonePositions(snapshot.Positions)
	work.PositionSources = clonePositions(snapshot.PositionSources)
	work.Sources = map[string]SourceMetadata{}
	for id, m := range snapshot.Sources {
		work.Sources[id] = m
	}
	work.Perceptions = map[string][]wiaworld.Perception{}
	for id, p := range snapshot.Perceptions {
		work.Perceptions[id] = slices.Clone(p)
	}
	out := Output{Clock: snapshot.Summary.Clock, Scene: snapshot.Summary.Scene, SceneLocation: snapshot.SceneLocation, SceneVersion: snapshot.SceneVersion + 1, SceneCharacters: CharacterIDs(InScene(snapshot.Characters)), SceneViews: slices.Clone(snapshot.SceneViews), Positions: clonePositions(snapshot.Positions), States: cloneStates(snapshot.States), Relationships: slices.Clone(snapshot.Relationships), Items: cloneItems(snapshot.Items), OpenProgress: cloneOpenProgress(snapshot.OpenProgress), ActionResolution: resolution, PlayerEventID: run.RunID + ":input"}
	out.Events = append(out.Events, wiaworld.Event{EventID: out.PlayerEventID, EventType: "player_input", ActorID: "player", Content: run.Input, RunID: run.RunID, SourceType: "author_input", SceneVersion: snapshot.SceneVersion, CreatedAt: time.Now().UTC()})
	worldCompiler := newSceneWorldCompiler(snapshot, draft, &out)
	done := map[string]bool{}
	inputs := map[int]bool{}
	visible := map[string]bool{}
	plansSeen := map[string]bool{}
	planCounts := map[string]int{}
	resolvedActionSeen := false
	for index, beat := range draft.Beats {
		clock, err := plot.AdvanceClock(snapshot.Summary.Clock, beat.OffsetMinutes)
		if err != nil {
			return Output{}, err
		}
		stage := index + 1
		// Introduce each raw fragment at its first associated node, using the
		// actual audience at that point rather than broadcasting the whole input.
		for inputIndex, part := range draft.InputMap {
			if inputs[inputIndex] || !slices.Contains(part.BeatIDs, beat.LocalID) {
				continue
			}
			if err := appendSceneInput(&work, &out, run, part, inputIndex, stage, ledger); err != nil {
				return Output{}, err
			}
			inputs[inputIndex] = true
		}
		if !prefix {
			for _, p := range draft.ProgressUpdates {
				if len(p.BeatIDs) == 0 && p.OffsetMinutes < beat.OffsetMinutes {
					assessmentClock, _ := plot.AdvanceClock(snapshot.Summary.Clock, p.OffsetMinutes)
					previousMinute, _ := plot.ClockMinute(out.Clock)
					assessmentMinute, _ := plot.ClockMinute(assessmentClock)
					if assessmentMinute >= previousMinute {
						out.Clock = assessmentClock
						if err := worldCompiler.applyReady(&work, &out, run, ledger, done, plansSeen); err != nil {
							return Output{}, err
						}
					}
				}
			}
		}
		out.Clock = clock
		worldActor := beat.Kind == "world_change" && beat.ActorID == "world"
		if !slices.Contains(selected, beat.ActorID) && !worldActor {
			if c, ok := characterByID(work.Characters, beat.ActorID); ok && (c.InScene || worldCompiler.stimulated[beat.ActorID] || sceneOffsceneEligible(work, beat.ActorID, clock)) {
				if expansion := sceneDueOwnerExpansion(work, out.Clock); expansion != nil && slices.Contains(expansion.EntityIDs, beat.ActorID) {
					return Output{}, expansion
				}
				return Output{}, &sceneActorExpansion{EntityID: beat.ActorID}
			}
			return Output{}, coordinationInvalid("scene_actor_invalid", "beats.actor_id", "selected-scene-participant")
		}
		if beat.ActorID != "player" && !worldActor {
			c, ok := characterByID(work.Characters, beat.ActorID)
			if !ok || !c.InScene && !sceneOffsceneEligible(work, beat.ActorID, clock) && !worldCompiler.stimulated[beat.ActorID] {
				return Output{}, coordinationInvalid("scene_actor_not_due", "beats.actor_id", "current-contact-or-actually-due-selected-owner")
			}
		}
		if beat.TargetID != "" && beat.TargetID != "player" {
			if _, ok := characterByID(work.Characters, beat.TargetID); !ok {
				return Output{}, coordinationInvalid("scene_target_invalid", "beats.target_id", "defined-person")
			}
		}
		if err := worldCompiler.authorizeBeat(work, out, beat, ledger); err != nil {
			return Output{}, err
		}
		author := beat.Kind == "observation" || beat.Kind == "action_result" || beat.Kind == "world_change"
		basis, err := ledger.resolve(beat.ActorID, beat.Basis, author)
		if err != nil {
			return Output{}, err
		}
		if err := sceneDurableSources(&work, basis); err != nil {
			return Output{}, err
		}
		startEvents, startPerceptions := len(out.Events), len(out.Perceptions)
		rootID := ""
		source := mechanicSource{}
		switch beat.Kind {
		case "dialogue":
			if err := validatePlayerSceneSpeech(draft, beat); err != nil {
				return Output{}, err
			}
			if err := validateSpeechAudience(work, beat.ActorID, *beat.Scope, beat.Recipients); err != nil {
				return Output{}, err
			}
			c, ok := characterByID(work.Characters, beat.ActorID)
			if beat.ActorID == "player" {
				c = wiaworld.Character{EntityID: "player", Name: snapshot.PlayerName, Role: "主角"}
				ok = true
			}
			if !ok {
				return Output{}, coordinationInvalid("scene_speaker_invalid", "beats.actor_id", "selected-person")
			}
			if work.Definition.Capabilities["spatial"] == 1 {
				out.speechAudience = sceneSpeechAudience(work, beat.ActorID)
			} else {
				out.speechAudience = append(CharacterIDs(InScene(work.Characters)), "player")
			}
			rootID, _ = appendSpeech(&out, run, c, beat.Content, *beat.Scope, beat.Recipients, InScene(work.Characters), out.SceneVersion, stage)
		case "action_result":
			actionID := fmt.Sprintf("%s:scene:%d:action", run.RunID, index+1)
			if beat.ActorID == "player" {
				fragment := *beat.Attempt.InputFragmentIndex
				if fragment >= len(draft.InputMap) || !slices.Contains(draft.InputMap[fragment].BeatIDs, beat.LocalID) {
					return Output{}, coordinationInvalid("scene_attempt_binding_invalid", "attempt.input_fragment_index", "associated-original-fragment")
				}
				part := draft.InputMap[fragment]
				if part.Visibility == "private" {
					for _, id := range beat.Recipients {
						if id != "player" && id != part.AddresseeID {
							return Output{}, coordinationInvalid("scene_private_result_forbidden", "recipients", "player-or-private-addressee")
						}
					}
					if len(beat.Bystanders) > 0 {
						return Output{}, coordinationInvalid("scene_private_result_forbidden", "bystanders", "empty-for-private-player-result")
					}
				}
				if draft.InputMap[fragment].ActionRuleID != "" {
					if resolution == nil && *beat.Status == "not_executed" {
						rule, ok := actionRuleByID(work.Definition, draft.InputMap[fragment].ActionRuleID)
						if !ok {
							return Output{}, ErrInvalidRequest
						}
						met, _ := evaluateFactConditions(work, Output{Positions: out.Positions, States: out.States, Relationships: out.Relationships, Items: out.Items}, rule.Conditions, "player")
						if met {
							return Output{}, coordinationInvalid("program_resolution_missing", "beats", "prepared-result-for-applicable-rule")
						}
					} else if resolvedActionSeen || resolution == nil || resolution.RuleID != draft.InputMap[fragment].ActionRuleID || resolution.Status != *beat.Status {
						return Output{}, coordinationInvalid("program_resolution_mismatch", "beats.status", "prepared-rule-result")
					} else {
						actionID = resolution.ActionID
						resolvedActionSeen = true
					}
				}
			}
			action := wiaworld.Event{EventID: actionID, EventType: "npc_action_intent", ActorID: beat.ActorID, TargetID: beat.TargetID, Content: beat.Attempt.Content, RunID: run.RunID, Stage: stage, SceneVersion: out.SceneVersion, SourceType: "scene_attempt", BasisEventIDs: eventBasisSources(basis), CreatedAt: time.Now().UTC()}
			if beat.ActorID == "player" {
				action.EventType = "player_action_intent"
				action.ProjectionParentID = out.PlayerEventID
			}
			resultID := actionID + ":result:1"
			source = mechanicSource{action: action, status: *beat.Status, resultID: resultID, recipients: map[string]bool{beat.ActorID: true}}
			for _, id := range beat.Recipients {
				source.recipients[id] = true
			}
			moves := []movementResult{}
			for _, m := range beat.Effects.Movements {
				moves = append(moves, movementResult{EntityID: m.EntityID, From: m.From, To: m.To, Route: m.Route, ActionID: actionID})
			}
			if len(moves) > 0 {
				if snapshot.Definition.Capabilities["spatial"] != 1 || beat.OffsetMinutes == 0 {
					return Output{}, coordinationInvalid("scene_movement_invalid", "effects.movements", "enabled-spatial-capability-and-positive-elapsed-time")
				}
				positions, changes, characters, err := applyMovements(work, moves, map[string]mechanicSource{actionID: source})
				if err != nil {
					return Output{}, err
				}
				out.Positions = positions
				out.PositionChanges = append(out.PositionChanges, changes...)
				out.SceneCharacters = characters
				work.Positions = clonePositions(positions)
				for _, change := range changes {
					work.PositionSources[change.EntityID] = change.SourceEventID
				}
				if err := refreshSpatialProjection(&work); err != nil {
					return Output{}, err
				}
			}
			if err := applySceneLegacyTransition(&work, &out, beat, source); err != nil {
				return Output{}, err
			}
			if err := validateSceneRecipients(work, beat); err != nil {
				return Output{}, err
			}
			result := action
			result.EventID = resultID
			result.EventType = "npc_action_result"
			if beat.ActorID == "player" {
				result.EventType = "player_action_result"
			}
			result.Content = beat.Content
			result.SourceType = "action_" + *beat.Status
			result.ProjectionParentID = actionID
			texts, err := actionProjectionText(hostActionResult{Projections: beat.Projections}, sceneProjectionRecipients(beat))
			if err != nil {
				return Output{}, err
			}
			out.Events = append(out.Events, action, result)
			out.VisibleEvents = append(out.VisibleEvents, appendActionProjections(&out, result, texts)...)
			rootID = resultID
		case "observation":
			if err := validateSceneRecipients(work, beat); err != nil {
				return Output{}, err
			}
			rootID = fmt.Sprintf("%s:scene:%d:observation", run.RunID, index+1)
			root := wiaworld.Event{EventID: rootID, EventType: "scene_observation", ActorID: beat.ActorID, Content: beat.Content, RunID: run.RunID, Stage: stage, SceneVersion: out.SceneVersion, SourceType: "plot_observed", BasisEventIDs: eventBasisSources(basis), CreatedAt: time.Now().UTC()}
			texts, err := actionProjectionText(hostActionResult{Projections: beat.Projections}, sceneProjectionRecipients(beat))
			if err != nil {
				return Output{}, err
			}
			out.Events = append(out.Events, root)
			out.VisibleEvents = append(out.VisibleEvents, appendActionProjections(&out, root, texts)...)
		case "world_change":
			if !worldActor {
				return Output{}, coordinationInvalid("scene_world_actor_invalid", "world_change.actor_id", "world")
			}
			rootID = sceneWorldEventID(run, index)
			root := wiaworld.Event{EventID: rootID, EventType: "plot_result", ActorID: "world", Content: beat.Content, RunID: run.RunID, Stage: stage, SceneVersion: out.SceneVersion, SourceType: "plot_occurred", BasisEventIDs: eventBasisSources(basis), CreatedAt: time.Now().UTC()}
			source = mechanicSource{action: root, status: "succeeded", resultID: rootID, recipients: map[string]bool{}}
			for _, id := range beat.Recipients {
				source.recipients[id] = true
				if id != "player" {
					if _, ok := characterByID(work.Characters, id); !ok {
						return Output{}, coordinationInvalid("scene_world_recipient_invalid", "recipients", "defined-person-or-player")
					}
				}
			}
			if len(beat.Bystanders) > 0 {
				return Output{}, coordinationInvalid("scene_world_bystander_invalid", "bystanders", "background-reactions-in-content")
			}
			if err := applySceneWorldMovements(&work, &out, beat, source); err != nil {
				return Output{}, err
			}
			if err := applySceneLegacyTransition(&work, &out, beat, source); err != nil {
				return Output{}, err
			}
			texts, err := actionProjectionText(hostActionResult{Projections: beat.Projections}, sceneProjectionRecipients(beat))
			if err != nil {
				return Output{}, err
			}
			out.Events = append(out.Events, root)
			projectionStart := len(out.Events)
			appendActionProjections(&out, root, texts)
			for i := projectionStart; i < len(out.Events); i++ {
				out.Events[i].EventType, out.Events[i].SourceType = "plot_perceived", "plot_observed"
				worldCompiler.stimulated[out.Events[i].TargetID] = true
			}
			for i := startPerceptions; i < len(out.Perceptions); i++ {
				out.Perceptions[i].SourceType = "plot_observed"
			}
		}
		for i := startEvents; i < len(out.Events); i++ {
			if out.Events[i].EventID == rootID {
				out.Events[i].BasisEventIDs = eventBasisSources(basis)
			}
		}
		for _, e := range out.Events[startEvents:] {
			work.Sources[e.EventID] = SourceMetadata{ID: e.EventID, Actor: e.ActorID, Kind: e.EventType, RunID: e.RunID, Stage: e.Stage, SceneVersion: e.SceneVersion}
		}
		if source.action.EventID != "" && resolution != nil && source.action.EventID == resolution.ActionID {
			_, err := applyActionResolution(work, &out, hostResult{Outcomes: []hostActionResult{{ActionID: source.action.EventID, Status: source.status, Recipients: beat.Recipients}}})
			if err != nil {
				return Output{}, err
			}
			for i := startEvents; i < len(out.Events); i++ {
				out.Events[i].Stage = stage
			}
			for i := startPerceptions; i < len(out.Perceptions); i++ {
				out.Perceptions[i].Stage = stage
			}
		}
		if err := applySceneEffects(&work, &out, run, beat, index, source, ledger, plansSeen, planCounts); err != nil {
			return Output{}, err
		}
		newPerceptions := out.Perceptions[startPerceptions:]
		ledger.recordBeat(beat.LocalID, rootID, newPerceptions)
		for _, p := range newPerceptions {
			if p.RecipientID == "player" {
				visible[beat.LocalID] = true
				if event, ok := EventByID(out.Events, p.SourceEventID); ok {
					if _, included := EventByID(out.VisibleEvents, event.EventID); !included {
						out.VisibleEvents = append(out.VisibleEvents, event)
					}
				}
			}
			updateScenePersonalView(&out, p)
		}
		// The program binds this new experience to world time. Wall-clock save
		// metadata remains separate, and older records keep their original wording.
		for i := startPerceptions; i < len(out.Perceptions); i++ {
			out.Perceptions[i].Content = "[世界时间=" + clock + "] " + out.Perceptions[i].Content
		}
		for i := startEvents; i < len(out.Events); i++ {
			if out.Events[i].EventType == "action_perceived" || out.Events[i].EventType == "npc_dialogue" || out.Events[i].EventType == "plot_perceived" {
				out.Events[i].Content = "[世界时间=" + clock + "] " + out.Events[i].Content
			}
		}
		mergePerceptions(&work, newPerceptions)
		work.States = cloneStates(out.States)
		work.Relationships = slices.Clone(out.Relationships)
		work.Items = cloneItems(out.Items)
		work.OpenProgress = cloneOpenProgress(out.OpenProgress)
		work.SceneViews = slices.Clone(out.SceneViews)
		done[beat.LocalID] = true
		if !prefix {
			if err := worldCompiler.applyReady(&work, &out, run, ledger, done, plansSeen); err != nil {
				return Output{}, err
			}
		}
	}
	for i, part := range draft.InputMap {
		if !inputs[i] && !prefix {
			if err := appendSceneInput(&work, &out, run, part, i, len(draft.Beats)+1, ledger); err != nil {
				return Output{}, err
			}
		}
	}
	if prefix {
		out.elapsedMinutes = draft.ElapsedMinutes
		return out, nil
	}
	if resolution != nil && !resolvedActionSeen {
		return Output{}, coordinationInvalid("program_resolution_missing", "beats", "prepared-action-result")
	}
	responded := len(visible) > 0
	for _, part := range draft.InputMap {
		if part.UnexecutedReason != "" {
			responded = true
		}
	}
	if !responded {
		return Output{}, coordinationInvalid("scene_response_invisible", "beats", "player-visible-response-or-explicit-unexecuted-reason")
	}
	paragraphs := []string{}
	for _, block := range draft.NarrativeBlocks {
		for _, id := range block.BeatIDs {
			if !visible[id] {
				return Output{}, coordinationInvalid("scene_narrative_forbidden", "narrative_blocks.beat_ids", "player-visible-node")
			}
		}
		paragraphs = append(paragraphs, block.Text)
	}
	out.Narrative = strings.Join(paragraphs, "\n\n")
	out.elapsedMinutes = draft.ElapsedMinutes
	clock, err := plot.AdvanceClock(snapshot.Summary.Clock, draft.ElapsedMinutes)
	if err != nil {
		return Output{}, err
	}
	out.Clock = clock
	if err := worldCompiler.applyReady(&work, &out, run, ledger, done, plansSeen); err != nil {
		return Output{}, err
	}
	if err := worldCompiler.finish(work, &out, run, ledger, plansSeen); err != nil {
		return Output{}, err
	}
	out.SceneLocation = out.Positions["player"]
	if out.SceneLocation == "" {
		out.SceneLocation = snapshot.SceneLocation
	}
	work.SceneViews = out.SceneViews
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		out.Scene = SceneFor(work, "player")
	}
	settled := "结束时间：" + clock + "\n玩家已经历结果：" + worldProgressionRecords(out.VisibleEvents)
	out.Events = append(out.Events, wiaworld.Event{EventID: run.RunID + ":clock", EventType: "time_advanced", ActorID: "world", Content: fmt.Sprintf("本轮实际经过%d分钟，从%s到%s。", draft.ElapsedMinutes, snapshot.Summary.Clock, clock), RunID: run.RunID, Stage: len(draft.Beats) + 1, SceneVersion: out.SceneVersion, SourceType: "world_clock", CreatedAt: time.Now().UTC()}, wiaworld.Event{EventID: run.RunID + ":settled", EventType: "turn_settled", ActorID: "world", Content: settled, RunID: run.RunID, Stage: len(draft.Beats) + 2, SceneVersion: out.SceneVersion, SourceType: "settled_turn", CreatedAt: time.Now().UTC()})
	return out, nil
}

func appendSceneInput(snapshot *Snapshot, out *Output, run wiaworld.Run, part sceneInput, index, stage int, ledger *SourceLedger) error {
	if index == 0 && run.AddresseeID != "" && part.AddresseeID != run.AddresseeID {
		return coordinationInvalid("scene_input_target_changed", "input_map.addressee_id", "explicit-ui-addressee")
	}
	participants := InScene(snapshot.Characters)
	if part.AddresseeID != "" {
		if _, ok := FindSceneCharacter(snapshot.Characters, part.AddresseeID); !ok {
			participants = nil
			if part.Status != "not_executed" && part.Visibility == "private" {
				return coordinationInvalid("scene_input_target_unreachable", "input_map", "reachable-private-addressee-or-not-executed")
			}
		}
	}
	partRun := run
	partRun.InputPart = index + 1
	partRun.Input = part.Text
	local := openOutput(snapshot, TurnIntent{IntentType: part.IntentType, Visibility: part.Visibility, AddresseeID: part.AddresseeID, Input: part.Text}, partRun, participants)
	local.Events[0].ProjectionParentID = out.PlayerEventID
	for i := range local.Events {
		local.Events[i].Stage = stage
	}
	for i := range local.Perceptions {
		local.Perceptions[i].Stage = stage
		local.Perceptions[i].Content = "[世界时间=" + out.Clock + "] " + local.Perceptions[i].Content
	}
	out.Events = append(out.Events, local.Events...)
	out.Perceptions = append(out.Perceptions, local.Perceptions...)
	alias := fmt.Sprintf("input:%d", index)
	ledger.grant("", alias, []string{local.PlayerEventID})
	ledger.grant("player", alias, []string{local.PlayerEventID})
	for _, p := range local.Perceptions {
		if p.SourceEventID == local.PlayerEventID {
			ledger.grant(p.RecipientID, alias, []string{p.SourceEventID})
		}
	}
	for _, e := range local.Events {
		snapshot.Sources[e.EventID] = SourceMetadata{ID: e.EventID, Actor: e.ActorID, Kind: e.EventType, RunID: e.RunID, Stage: e.Stage}
	}
	mergePerceptions(snapshot, local.Perceptions)
	return nil
}

func sceneProjectionRecipients(beat sceneBeat) map[string]bool {
	ids := map[string]bool{}
	if beat.ActorID != "world" {
		ids[beat.ActorID] = true
	}
	for _, id := range append(slices.Clone(beat.Recipients), beat.Bystanders...) {
		ids[id] = true
	}
	return ids
}

func validateSceneRecipients(snapshot Snapshot, beat sceneBeat) error {
	for _, id := range beat.Recipients {
		if id != "player" {
			if _, ok := characterByID(snapshot.Characters, id); !ok {
				return coordinationInvalid("scene_recipient_invalid", "recipients", "defined-person")
			}
		}
		if snapshot.Definition.Capabilities["spatial"] == 1 && !currentlyCoLocated(snapshot, beat.ActorID, id) {
			return coordinationInvalid("scene_recipient_unreachable", "recipients", "same-node-place")
		}
		if snapshot.Definition.Capabilities["spatial"] != 1 && id != "player" {
			if _, ok := FindSceneCharacter(snapshot.Characters, id); !ok {
				return coordinationInvalid("scene_recipient_unreachable", "recipients", "current-contact")
			}
		}
	}
	for _, id := range beat.Bystanders {
		found := false
		for _, b := range snapshot.Definition.BystanderRefs {
			if b.BystanderID == id {
				found = true
			}
		}
		if !found || snapshot.Definition.Capabilities["spatial"] == 1 && !currentlyCoLocated(snapshot, beat.ActorID, id) {
			return coordinationInvalid("scene_bystander_invalid", "bystanders", "defined-and-present-background-entity")
		}
	}
	return nil
}

func sceneSpeechAudience(snapshot Snapshot, speaker string) []string {
	ids := []string{}
	for id, place := range snapshot.Positions {
		if place != "" && place == snapshot.Positions[speaker] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func sceneDurableSources(snapshot *Snapshot, ids []string) error {
	for _, id := range eventBasisSources(ids) {
		if _, ok := snapshot.Sources[id]; ok {
			continue
		}
		found := false
		for _, e := range snapshot.Events {
			if e.EventID == id {
				found = true
				snapshot.Sources[id] = SourceMetadata{ID: id, Actor: e.ActorID, Kind: e.EventType}
			}
		}
		for owner, m := range snapshot.LongMemory {
			for _, r := range append(slices.Clone(m.Archive), m.Tail...) {
				if r.Scope == owner && r.EventID == id {
					found = true
					snapshot.Sources[id] = SourceMetadata{ID: id, Actor: r.Actor, Kind: r.Kind}
				}
			}
		}
		if !found {
			for _, source := range currentFactSources(*snapshot) {
				if source == id {
					found = true
					snapshot.Sources[id] = SourceMetadata{ID: id}
				}
			}
		}
		if !found {
			return coordinationInvalid("scene_source_not_durable", "basis", "valid-existing-event-or-program-compiled-node")
		}
	}
	return nil
}

func updateScenePersonalView(out *Output, p wiaworld.Perception) {
	if p.SourceType == "own_speech" || strings.HasPrefix(p.SourceType, "heard_") {
		return
	}
	for i, v := range out.SceneViews {
		if v.Recipient == p.RecipientID {
			out.SceneViews[i] = SceneView{Recipient: p.RecipientID, Content: p.Content, SourceIDs: []string{p.SourceEventID}, Version: out.SceneVersion}
			return
		}
	}
	out.SceneViews = append(out.SceneViews, SceneView{Recipient: p.RecipientID, Content: p.Content, SourceIDs: []string{p.SourceEventID}, Version: out.SceneVersion})
}

func validatePlayerSceneSpeech(draft *SceneDraft, beat sceneBeat) error {
	if beat.ActorID != "player" {
		return nil
	}
	associated := false
	for _, part := range draft.InputMap {
		if !slices.Contains(part.BeatIDs, beat.LocalID) {
			continue
		}
		associated = true
		if *beat.Scope != part.Visibility {
			return coordinationInvalid("scene_player_scope_changed", "beats.scope", "associated-original-input-scope")
		}
		if part.Visibility == "private" && (len(beat.Recipients) != 1 || beat.Recipients[0] != part.AddresseeID) {
			return coordinationInvalid("scene_player_scope_changed", "beats.recipients", "original-private-addressee")
		}
	}
	if !associated {
		return coordinationInvalid("scene_player_input_missing", "beats", "associated-original-player-input")
	}
	return nil
}

func validateSceneInputOutcomes(draft *SceneDraft) error {
	for index, part := range draft.InputMap {
		for _, beat := range draft.Beats {
			if beat.ActorID != "player" || beat.Attempt == nil || beat.Attempt.InputFragmentIndex == nil || *beat.Attempt.InputFragmentIndex != index {
				continue
			}
			if part.Status == "succeeded" && (*beat.Status == "failed" || *beat.Status == "not_executed") || part.Status == "not_executed" && *beat.Status != "not_executed" {
				return coordinationInvalid("scene_input_outcome_conflict", "input_map.status", "consistent-player-action-results")
			}
		}
	}
	return nil
}

func applySceneEffects(snapshot *Snapshot, out *Output, run wiaworld.Run, beat sceneBeat, index int, source mechanicSource, ledger *SourceLedger, plansSeen map[string]bool, planCounts map[string]int) error {
	effects := mechanicEffects{sources: map[string]mechanicSource{}, relationshipAuthority: map[string]bool{}}
	relationBases := [][]string{}
	if source.action.EventID != "" {
		effects.sources[source.action.EventID] = source
	}
	for _, m := range beat.Effects.Movements {
		effects.Movements = append(effects.Movements, movementResult{EntityID: m.EntityID})
	}
	for _, e := range beat.Effects.StateEffects {
		if !slices.Contains(snapshot.sceneEntities, e.EntityID) {
			return coordinationInvalid("scene_effect_entity_unselected", "state_effects.entity_id", "selected-entity")
		}
		effects.StateEffects = append(effects.StateEffects, stateEffect{EntityID: e.EntityID, StateID: e.StateID, Delta: e.Delta, Value: e.Value, ActionID: source.action.EventID})
	}
	for _, e := range beat.Effects.ItemTransfers {
		effects.ItemTransfers = append(effects.ItemTransfers, itemTransferEffect{InstanceID: e.InstanceID, FromHolderID: e.FromHolderID, FromLocationID: e.FromLocationID, ToHolderID: e.ToHolderID, ToLocationID: e.ToLocationID, ActionID: source.action.EventID})
	}
	for _, e := range beat.Effects.RelationshipEffects {
		if !slices.Contains(snapshot.sceneEntities, e.SubjectID) || len(e.Basis) < 1 || len(e.Basis) > 8 {
			return coordinationInvalid("scene_relationship_owner_invalid", "relationship_effects", "selected-subject-with-personal-basis")
		}
		basis, err := ledger.resolve(e.SubjectID, e.Basis, false)
		if err != nil {
			return err
		}
		if err := sceneDurableSources(snapshot, basis); err != nil {
			return err
		}
		events := eventBasisSources(basis)
		if len(events) == 0 {
			return coordinationInvalid("scene_relationship_basis_invalid", "relationship_effects.basis", "personal-event-source")
		}
		effect := relationshipEffect{SubjectID: e.SubjectID, TargetID: e.TargetID, RelationType: e.RelationType, Delta: e.Delta, ProposalSourceID: events[0]}
		effects.RelationshipEffects = append(effects.RelationshipEffects, effect)
		relationBases = append(relationBases, events)
		effects.relationshipAuthority[relationshipAuthorityKey(effect)] = true
	}
	startEvents := len(out.Events)
	if err := applyMechanicEffects(*snapshot, out, effects); err != nil {
		return err
	}
	// Keep every verified dependency, even though the existing relation ledger
	// uses the first personal event as its deduplication key.
	relationIndex := 0
	for i := startEvents; i < len(out.Events); i++ {
		if out.Events[i].EventType == "relationship_change" {
			out.Events[i].BasisEventIDs = slices.Clone(relationBases[relationIndex])
			relationIndex++
		}
	}
	for _, p := range beat.Effects.PlanUpdates {
		if p.OwnerID == "player" || !slices.Contains(snapshot.sceneEntities, p.OwnerID) || (p.ID == "") == (p.LocalPlanID == "") || p.LocalPlanID != "" && (strings.ContainsAny(p.LocalPlanID, ": \t\r\n") || len(p.LocalPlanID) > 32) {
			return coordinationInvalid("scene_plan_owner_invalid", "plan_updates", "selected-owner-and-id-or-local-plan-id")
		}
		id := p.ID
		if id == "" {
			id = p.OwnerID + ":" + run.RunID + ":" + p.LocalPlanID
		}
		if plansSeen[id] || planCounts[p.OwnerID] >= 4 {
			return coordinationInvalid("scene_plan_duplicate", "plan_updates", "one-update-per-plan-and-four-per-owner-per-turn")
		}
		if p.ID != "" {
			found := false
			if out.OpenProgress != nil {
				for _, existing := range out.OpenProgress.Plans {
					if existing.ID == id && existing.OwnerID == p.OwnerID {
						found = true
					}
				}
			}
			if !found {
				return coordinationInvalid("scene_plan_id_invalid", "plan_updates.id", "existing-owned-plan")
			}
		}
		basis, err := ledger.resolve(p.OwnerID, p.Basis, false)
		if err != nil {
			return err
		}
		if err := sceneDurableSources(snapshot, basis); err != nil {
			return err
		}
		for _, id := range basis {
			if strings.HasPrefix(id, "definition:") || strings.HasPrefix(id, "fact:") {
				return coordinationInvalid("scene_plan_basis_invalid", "plan_updates.basis", "durable-personal-event-or-frozen-material")
			}
		}
		update := planUpdate{ID: id, Content: p.Content, SourceIDs: basis, Status: p.Status, ReviewAfterMinutes: p.ReviewAfterMinutes}
		updates, err := validateOwnedPlanUpdates(*snapshot, p.OwnerID, []planUpdate{update}, basis)
		if err != nil {
			return err
		}
		applyPlanUpdates(out, run, p.OwnerID, updates, index+1)
		if len(out.OpenProgress.Plans) > 32 {
			return coordinationInvalid("scene_plan_capacity", "plan_updates", "at-most-32-current-plans")
		}
		snapshot.OpenProgress = cloneOpenProgress(out.OpenProgress)
		plansSeen[id] = true
		planCounts[p.OwnerID]++
	}
	return nil
}

func sceneOffsceneEligible(snapshot Snapshot, owner, clock string) bool {
	if snapshot.OpenProgress == nil {
		return false
	}
	minute, err := plot.ClockMinute(clock)
	if err != nil {
		return false
	}
	return slices.Contains(plot.DuePlanOwners(snapshot.OpenProgress.Plans, minute, 2), owner)
}

func applySceneWorldMovements(snapshot *Snapshot, out *Output, beat sceneBeat, source mechanicSource) error {
	if len(beat.Effects.Movements) > 1 {
		return coordinationInvalid("scene_world_movement_invalid", "effects.movements", "one-movement-per-resolved-world-node")
	}
	for _, move := range beat.Effects.Movements {
		if snapshot.Definition.Capabilities["spatial"] != 1 || beat.OffsetMinutes == 0 || !slices.Contains(snapshot.sceneEntities, move.EntityID) {
			return coordinationInvalid("scene_world_movement_invalid", "effects.movements", "declared-spatial-entity-and-positive-time")
		}
		bound := source
		bound.action.ActorID = move.EntityID
		movement := movementResult{EntityID: move.EntityID, From: move.From, To: move.To, Route: move.Route, ActionID: source.action.EventID}
		positions, changes, characters, err := applyMovements(*snapshot, []movementResult{movement}, map[string]mechanicSource{source.action.EventID: bound})
		if err != nil {
			return err
		}
		out.Positions, out.SceneCharacters = positions, characters
		out.PositionChanges = append(out.PositionChanges, changes...)
		snapshot.Positions = clonePositions(positions)
		for _, c := range changes {
			snapshot.PositionSources[c.EntityID] = c.SourceEventID
		}
	}
	if len(beat.Effects.Movements) > 0 {
		return refreshSpatialProjection(snapshot)
	}
	return nil
}

func applySceneLegacyTransition(snapshot *Snapshot, out *Output, beat sceneBeat, source mechanicSource) error {
	legacy := beat.Effects.LegacyScene
	if legacy == nil {
		return nil
	}
	if snapshot.Definition.Capabilities["spatial"] == 1 || source.status != "succeeded" && source.status != "partial" || strings.TrimSpace(legacy.Content) == "" || len([]rune(legacy.Content)) > 1200 || legacy.Characters == nil || !uniqueSceneIDs(legacy.Characters) {
		return coordinationInvalid("scene_legacy_transition_invalid", "effects.legacy_scene", "explicit-resolved-transition-in-nonspatial-world")
	}
	for _, id := range legacy.Characters {
		if _, ok := characterByID(snapshot.Characters, id); !ok {
			return coordinationInvalid("scene_legacy_presence_invalid", "effects.legacy_scene.characters", "frozen-important-character-ids")
		}
	}
	out.Scene, snapshot.Summary.Scene = legacy.Content, legacy.Content
	out.SceneCharacters = slices.Clone(legacy.Characters)
	for i := range snapshot.Characters {
		snapshot.Characters[i].InScene = slices.Contains(legacy.Characters, snapshot.Characters[i].EntityID)
	}
	return nil
}
