package turn

import (
	"fmt"
	"slices"
	"strings"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

type mechanicSource struct {
	action     wiaworld.Event
	status     string
	recipients map[string]bool
	resultID   string
}

func mechanicSources(events []wiaworld.Event, outcomes []hostActionResult) map[string]mechanicSource {
	actions := map[string]wiaworld.Event{}
	for _, event := range events {
		if event.EventType == "player_action_intent" || event.EventType == "npc_action_intent" {
			actions[event.EventID] = event
		}
	}
	result := map[string]mechanicSource{}
	for index, outcome := range outcomes {
		actionID := strings.TrimSpace(outcome.ActionID)
		action, ok := actions[actionID]
		if !ok {
			continue
		}
		recipients := map[string]bool{action.ActorID: true}
		for _, id := range outcome.Recipients {
			recipients[strings.TrimSpace(id)] = true
		}
		result[actionID] = mechanicSource{action: action, status: strings.ToLower(strings.TrimSpace(outcome.Status)), recipients: recipients, resultID: fmt.Sprintf("%s:result:%d", actionID, index+1)}
	}
	return result
}

func validMechanicSource(sources map[string]mechanicSource, actionID string) (mechanicSource, bool) {
	source, ok := sources[strings.TrimSpace(actionID)]
	return source, ok && (source.status == "succeeded" || source.status == "partial")
}

func placeExists(def story.Definition, id string) bool { _, ok := story.PlaceGraph(def)[id]; return ok }

func checkedAbs(value int) (int64, bool) {
	if value != 0 && value == -value {
		return 0, false
	}
	converted := int64(value)
	if converted < 0 {
		converted = -converted
	}
	return converted, true
}

func checkedAdd(left, right int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	if (right > 0 && left > maxInt-right) || (right < 0 && left < minInt-right) {
		return 0, false
	}
	return left + right, true
}

func checkedDistance(left, right int) (int64, bool) {
	var distance uint64
	if left >= right {
		distance = uint64(left) - uint64(right)
	} else {
		distance = uint64(right) - uint64(left)
	}
	if distance > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return int64(distance), true
}

// applyMechanicEffects publishes only a complete candidate. Individual modules
// own their state and return changes without committing to storage.
func applyMechanicEffects(snapshot Snapshot, output *Output, host hostResult) error {
	if err := validateMechanicCapabilities(snapshot, host); err != nil {
		return err
	}
	candidate := *output
	candidate.States = cloneStates(output.States)
	candidate.Relationships = slices.Clone(output.Relationships)
	candidate.Items = cloneItems(output.Items)
	candidate.StateChanges = slices.Clone(output.StateChanges)
	candidate.RelationshipChanges = slices.Clone(output.RelationshipChanges)
	candidate.ItemTransfers = slices.Clone(output.ItemTransfers)
	candidate.Events = slices.Clone(output.Events)
	candidate.Perceptions = slices.Clone(output.Perceptions)
	sources := mechanicSources(output.Events, host.Outcomes)
	if err := applyStateEffects(snapshot, &candidate, host.StateEffects, sources); err != nil {
		return err
	}
	if err := applyRelationshipEffects(snapshot, &candidate, host.RelationshipEffects); err != nil {
		return err
	}
	if err := applyItemTransfers(snapshot, &candidate, host.ItemTransfers, sources); err != nil {
		return err
	}
	playerSources := map[string]bool{}
	for _, change := range candidate.StateChanges {
		definition, _ := stateDefinition(snapshot.Definition, change.After.StateID)
		if change.After.EntityID != "player" && definition.Projection == "public" && sources[change.ActionID].recipients["player"] {
			playerSources[change.SourceEventID] = true
		}
	}
	for _, change := range candidate.ItemTransfers {
		definition, _ := itemDefinition(snapshot.Definition, change.After.DefinitionID)
		if definition.Projection == "public" && sources[change.ActionID].recipients["player"] {
			playerSources[change.SourceEventID] = true
		}
	}
	for sourceID := range playerSources {
		if event, ok := EventByID(candidate.Events, sourceID); ok {
			candidate.Perceptions = append(candidate.Perceptions, wiaworld.Perception{RecipientID: "player", SourceEventID: sourceID, SourceType: event.SourceType, Content: event.Content, Stage: event.Stage, SceneVersion: event.SceneVersion, CreatedAt: event.CreatedAt})
		}
	}
	*output = candidate
	return nil
}

func nextEffectOrder(output Output) int {
	order := 0
	for _, c := range output.StateChanges {
		order = max(order, c.Order)
	}
	for _, c := range output.RelationshipChanges {
		order = max(order, c.Order)
	}
	for _, c := range output.ItemTransfers {
		order = max(order, c.Order)
	}
	return order + 1
}

func stateDefinition(def story.Definition, id string) (story.StateDefinition, bool) {
	for _, item := range def.StateDefinitions {
		if item.ID == id {
			return item, true
		}
	}
	return story.StateDefinition{}, false
}

func relationDefinition(def story.Definition, id string) (story.RelationDefinition, bool) {
	for _, item := range def.RelationDefinitions {
		if item.ID == id {
			return item, true
		}
	}
	return story.RelationDefinition{}, false
}

func itemDefinition(def story.Definition, id string) (story.ItemDefinition, bool) {
	for _, item := range def.ItemDefinitions {
		if item.ID == id {
			return item, true
		}
	}
	return story.ItemDefinition{}, false
}

func validateMechanicsSnapshot(snapshot Snapshot) error {
	entities := map[string]bool{"player": true}
	for _, character := range snapshot.Characters {
		entities[character.EntityID] = true
	}
	if snapshot.Definition.Capabilities["state"] == 1 {
		expectedStates := map[string]map[string]bool{}
		for entityID := range entities {
			expectedStates[entityID] = map[string]bool{}
			for _, definition := range snapshot.Definition.StateDefinitions {
				if (entityID == "player" && definition.Scope == "npc") || (entityID != "player" && definition.Scope == "player") {
					continue
				}
				state, ok := snapshot.States[entityID][definition.ID]
				if !ok || state.Value.Type != definition.Type || state.SourceEvent == "" || state.Version < 1 {
					return fmt.Errorf("%w: invalid state snapshot", memory.ErrStorageUnavailable)
				}
				expectedStates[entityID][definition.ID] = true
				if definition.Type == "integer" && (state.Value.Integer < *definition.Minimum || state.Value.Integer > *definition.Maximum) {
					return fmt.Errorf("%w: invalid state range", memory.ErrStorageUnavailable)
				}
				if definition.Type == "enum" && !slices.Contains(definition.EnumValues, state.Value.Enum) {
					return fmt.Errorf("%w: invalid enum state", memory.ErrStorageUnavailable)
				}
			}
		}
		for entityID, values := range snapshot.States {
			if !entities[entityID] {
				return fmt.Errorf("%w: unknown state entity", memory.ErrStorageUnavailable)
			}
			for stateID := range values {
				if !expectedStates[entityID][stateID] {
					return fmt.Errorf("%w: unknown entity state", memory.ErrStorageUnavailable)
				}
			}
		}
	} else if len(snapshot.States) > 0 {
		return fmt.Errorf("%w: state rows without capability", memory.ErrStorageUnavailable)
	}
	if snapshot.Definition.Capabilities["relations"] == 1 {
		seen := map[string]bool{}
		for _, relation := range snapshot.Relationships {
			definition, ok := relationDefinition(snapshot.Definition, relation.RelationType)
			if !ok || !entities[relation.SubjectID] || !entities[relation.TargetID] || relation.SubjectID == relation.TargetID || relation.Value < definition.Minimum || relation.Value > definition.Maximum || relation.SourceEvent == "" || relation.Version < 1 {
				return fmt.Errorf("%w: invalid relationship snapshot", memory.ErrStorageUnavailable)
			}
			seen[relation.SubjectID+"\x00"+relation.TargetID+"\x00"+relation.RelationType] = true
		}
		for subject := range entities {
			for target := range entities {
				if subject == target {
					continue
				}
				for _, definition := range snapshot.Definition.RelationDefinitions {
					if !seen[subject+"\x00"+target+"\x00"+definition.ID] {
						return fmt.Errorf("%w: missing relationship", memory.ErrStorageUnavailable)
					}
				}
			}
		}
	} else if len(snapshot.Relationships) > 0 {
		return fmt.Errorf("%w: relationship rows without capability", memory.ErrStorageUnavailable)
	}
	if snapshot.Definition.Capabilities["items"] == 1 {
		if len(snapshot.Items) != len(snapshot.Definition.InitialItems) {
			return fmt.Errorf("%w: invalid item snapshot", memory.ErrStorageUnavailable)
		}
		for _, item := range snapshot.Items {
			_, ok := itemDefinition(snapshot.Definition, item.DefinitionID)
			if !ok || (item.HolderID == "") == (item.LocationID == "") || (item.HolderID != "" && !entities[item.HolderID]) || (item.LocationID != "" && !placeExists(snapshot.Definition, item.LocationID)) || item.SourceEvent == "" || item.Version < 1 {
				return fmt.Errorf("%w: invalid item snapshot", memory.ErrStorageUnavailable)
			}
		}
	} else if len(snapshot.Items) > 0 {
		return fmt.Errorf("%w: item rows without capability", memory.ErrStorageUnavailable)
	}
	return nil
}
