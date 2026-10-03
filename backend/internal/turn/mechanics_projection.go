package turn

import (
	"encoding/json"
	"slices"
	"strings"

	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

// PlayerStateProjection filters at the server boundary; hidden NPC state never
// enters an ordinary play response.
func PlayerStateProjection(snapshot Snapshot) []wiaworld.PublicState {
	result := []wiaworld.PublicState{}
	for entityID, values := range snapshot.States {
		for stateID, current := range values {
			definition, ok := stateDefinition(snapshot.Definition, stateID)
			if !ok || definition.Projection == "hidden" || (entityID != "player" && (definition.Projection != "public" || !visibleCurrentSource(snapshot, "player", entityID, current.SourceEvent))) {
				continue
			}
			state := wiaworld.PublicState{EntityID: entityID, StateID: stateID, Name: definition.Name, Value: current.Value, Unit: definition.Unit, Category: definition.Category, Currency: definition.Currency, Description: definition.Description}
			if definition.Currency != nil {
				state.DisplayValue = definition.Currency.Format(current.Value.Integer)
			}
			result = append(result, state)
		}
	}
	slices.SortFunc(result, func(a, b wiaworld.PublicState) int {
		if a.EntityID == b.EntityID {
			if a.StateID < b.StateID {
				return -1
			}
			if a.StateID > b.StateID {
				return 1
			}
			return 0
		}
		if a.EntityID < b.EntityID {
			return -1
		}
		return 1
	})
	return result
}

func PlayerItemProjection(snapshot Snapshot) []wiaworld.PublicItem {
	result := []wiaworld.PublicItem{}
	for _, item := range snapshot.Items {
		definition, ok := itemDefinition(snapshot.Definition, item.DefinitionID)
		if !ok || definition.Projection == "hidden" {
			continue
		}
		allowed := itemVisibleTo(snapshot, "player", item, definition)
		if !allowed {
			continue
		}
		result = append(result, wiaworld.PublicItem{InstanceID: item.InstanceID, DefinitionID: item.DefinitionID, Name: definition.Name, Description: definition.Description, HolderID: item.HolderID, LocationID: item.LocationID})
	}
	slices.SortFunc(result, func(a, b wiaworld.PublicItem) int {
		if a.InstanceID < b.InstanceID {
			return -1
		}
		if a.InstanceID > b.InstanceID {
			return 1
		}
		return 0
	})
	return result
}

type mechanicsProjection struct {
	States              []wiaworld.PublicState     `json:"states,omitempty"`
	Relationships       []wiaworld.Relationship    `json:"relationships,omitempty"`
	RelationDefinitions []story.RelationDefinition `json:"relation_definitions,omitempty"`
	Items               []wiaworld.PublicItem      `json:"items,omitempty"`
}

func perceivedSource(snapshot Snapshot, recipient, source string) bool {
	if snapshot.PerceivedSources[recipient][source] {
		return true
	}
	for _, perception := range snapshot.Perceptions[recipient] {
		if perception.SourceEventID == source {
			return true
		}
		if strings.HasPrefix(perception.SourceEventID, source+":projection:") {
			if event, ok := EventByID(snapshot.Events, perception.SourceEventID); ok && event.EventType == "action_perceived" && event.ProjectionParentID == source && event.TargetID == recipient {
				return true
			}
		}
	}
	return false
}

func currentlyCoLocated(snapshot Snapshot, left, right string) bool {
	return left == right || (snapshot.Positions[left] != "" && snapshot.Positions[left] == snapshot.Positions[right])
}

func visibleCurrentSource(snapshot Snapshot, recipient, entityID, source string) bool {
	return currentlyCoLocated(snapshot, recipient, entityID) && (source == "opening" || perceivedSource(snapshot, recipient, source))
}

// MechanicsContext builds a recipient-specific projection before prompt assembly.
func MechanicsContext(snapshot Snapshot, recipient string) string {
	view := mechanicsProjection{}
	if recipient == "player" {
		view.States, view.Items = PlayerStateProjection(snapshot), PlayerItemProjection(snapshot)
	} else {
		for entityID, values := range snapshot.States {
			for stateID, current := range values {
				definition, ok := stateDefinition(snapshot.Definition, stateID)
				if !ok || definition.Knowledge == "host_only" {
					continue
				}
				allowed := entityID == recipient && (definition.Knowledge == "owner" || definition.Knowledge == "public")
				allowed = allowed || (entityID != recipient && definition.Knowledge == "public" && visibleCurrentSource(snapshot, recipient, entityID, current.SourceEvent))
				if allowed {
					state := wiaworld.PublicState{EntityID: entityID, StateID: stateID, Name: definition.Name, Value: current.Value, Unit: definition.Unit, Category: definition.Category, Currency: definition.Currency, Description: definition.Description}
					if definition.Currency != nil {
						state.DisplayValue = definition.Currency.Format(current.Value.Integer)
					}
					view.States = append(view.States, state)
				}
			}
		}
		seenRelations := map[string]bool{}
		for _, relation := range snapshot.Relationships {
			if relation.SubjectID == recipient {
				view.Relationships = append(view.Relationships, relation)
				if definition, ok := relationDefinition(snapshot.Definition, relation.RelationType); ok && !seenRelations[definition.ID] {
					view.RelationDefinitions = append(view.RelationDefinitions, definition)
					seenRelations[definition.ID] = true
				}
			}
		}
		for _, item := range snapshot.Items {
			definition, ok := itemDefinition(snapshot.Definition, item.DefinitionID)
			if !ok || definition.Projection == "hidden" {
				continue
			}
			if itemVisibleTo(snapshot, recipient, item, definition) {
				view.Items = append(view.Items, wiaworld.PublicItem{InstanceID: item.InstanceID, DefinitionID: item.DefinitionID, Name: definition.Name, Description: definition.Description, HolderID: item.HolderID, LocationID: item.LocationID})
			}
		}
	}
	encoded, _ := json.Marshal(view)
	return string(encoded)
}

func HostMechanicsContext(snapshot Snapshot) string {
	stateDefinitions := newContextTable("id", "name", "type", "minimum", "maximum", "enum_values", "projection", "knowledge", "update_policy", "description", "unit", "currency")
	for _, definition := range snapshot.Definition.StateDefinitions {
		stateDefinitions.add(definition.ID, definition.Name, definition.Type, definition.Minimum, definition.Maximum, definition.EnumValues, definition.Projection, definition.Knowledge, definition.UpdatePolicy, definition.Description, definition.Unit, definition.Currency)
	}
	stateValues := map[string]map[string]any{}
	for entityID, states := range snapshot.States {
		stateValues[entityID] = map[string]any{}
		for stateID, current := range states {
			var value any
			switch current.Value.Type {
			case "integer":
				value = current.Value.Integer
			case "boolean":
				value = current.Value.Boolean
			case "enum":
				value = current.Value.Enum
			}
			stateValues[entityID][stateID] = value
		}
	}
	relationDefinitions := newContextTable("id", "name", "minimum", "maximum", "default", "max_change_per_turn", "projection", "description")
	for _, definition := range snapshot.Definition.RelationDefinitions {
		relationDefinitions.add(definition.ID, definition.Name, definition.Minimum, definition.Maximum, definition.Default, definition.MaxChangePerTurn, definition.Projection, definition.Description)
	}
	nonDefaultRelationships := newContextTable("subject_id", "target_id", "relation_type", "value", "source_event_id")
	for _, relation := range snapshot.Relationships {
		definition, ok := relationDefinition(snapshot.Definition, relation.RelationType)
		if ok && (relation.Value != definition.Default || relation.UpdatedTurn > 0) {
			nonDefaultRelationships.add(relation.SubjectID, relation.TargetID, relation.RelationType, relation.Value, relation.SourceEvent)
		}
	}
	nonDefaultRelationships.sortRows()
	itemDefinitions := newContextTable("id", "name", "description", "projection")
	for _, definition := range snapshot.Definition.ItemDefinitions {
		itemDefinitions.add(definition.ID, definition.Name, definition.Description, definition.Projection)
	}
	items := newContextTable("instance_id", "definition_id", "holder_id", "location_id", "source_event_id")
	for _, item := range snapshot.Items {
		items.add(item.InstanceID, item.DefinitionID, item.HolderID, item.LocationID, item.SourceEvent)
	}
	items.sortRows()
	value := struct {
		StateDefinitions    contextTable              `json:"state_definitions"`
		States              map[string]map[string]any `json:"states"`
		RelationDefinitions contextTable              `json:"relation_definitions"`
		Relationships       contextTable              `json:"relationships"`
		ItemDefinitions     contextTable              `json:"item_definitions"`
		Items               contextTable              `json:"items"`
	}{stateDefinitions, stateValues, relationDefinitions, nonDefaultRelationships, itemDefinitions, items}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func itemVisibleTo(snapshot Snapshot, recipient string, item wiaworld.ItemInstance, definition story.ItemDefinition) bool {
	if definition.Projection == "hidden" {
		return false
	}
	if item.HolderID == recipient {
		return true
	}
	if definition.Projection != "public" {
		return false
	}
	location := snapshot.Positions[recipient]
	if location == "" {
		return false
	}
	if item.LocationID != "" {
		return item.LocationID == location
	}
	return snapshot.Positions[item.HolderID] == location && (item.SourceEvent == "opening" || perceivedSource(snapshot, recipient, item.SourceEvent))
}
