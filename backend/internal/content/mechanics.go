package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

func parseStateValue(def story.StateDefinition, raw json.RawMessage) (wiaworld.StateValue, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if len(raw) == 0 || decoder.Decode(&value) != nil {
		return wiaworld.StateValue{}, fmt.Errorf("invalid value")
	}
	result := wiaworld.StateValue{Type: def.Type}
	switch def.Type {
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return result, fmt.Errorf("expected integer")
		}
		parsed, err := number.Int64()
		if err != nil || parsed < int64(*def.Minimum) || parsed > int64(*def.Maximum) {
			return result, fmt.Errorf("integer outside range")
		}
		result.Integer = int(parsed)
	case "boolean":
		parsed, ok := value.(bool)
		if !ok {
			return result, fmt.Errorf("expected boolean")
		}
		result.Boolean = parsed
	case "enum":
		parsed, ok := value.(string)
		if !ok || !slices.Contains(def.EnumValues, parsed) {
			return result, fmt.Errorf("expected declared enum value")
		}
		result.Enum = parsed
	default:
		return result, fmt.Errorf("unsupported state type")
	}
	return result, nil
}

func compileStateDefinitions(definitions []PackStateDefinition) ([]story.StateDefinition, map[string]story.StateDefinition, error) {
	result := make([]story.StateDefinition, 0, len(definitions))
	byID := make(map[string]story.StateDefinition, len(definitions))
	for _, pack := range definitions {
		if !packID.MatchString(pack.ID) || strings.TrimSpace(pack.Name) == "" || byID[pack.ID].ID != "" {
			return nil, nil, fmt.Errorf("invalid or duplicate state definition")
		}
		if !slices.Contains([]string{"player", "npc", "all"}, pack.Scope) || !slices.Contains([]string{"self", "public", "hidden"}, pack.Projection) || !slices.Contains([]string{"owner", "public", "host_only"}, pack.Knowledge) {
			return nil, nil, fmt.Errorf("invalid state visibility")
		}
		if pack.UpdatePolicy.Kind == "" {
			pack.UpdatePolicy.Kind = "readonly"
		}
		if pack.UpdatePolicy.Kind != "model" && pack.UpdatePolicy.Kind != "readonly" && pack.UpdatePolicy.Kind != "bounded_proposal" && pack.UpdatePolicy.Kind != "rule_only" {
			return nil, nil, fmt.Errorf("invalid state update policy")
		}
		if pack.UpdatePolicy.Kind == "model" && pack.UpdatePolicy.MaxChangePerTurn != 0 {
			return nil, nil, fmt.Errorf("model policy has no fixed turn budget")
		}
		if pack.Type == "integer" {
			if pack.Minimum == nil || pack.Maximum == nil || *pack.Minimum > *pack.Maximum || len(pack.EnumValues) != 0 {
				return nil, nil, fmt.Errorf("invalid integer state range")
			}
			if pack.UpdatePolicy.Kind == "bounded_proposal" && pack.UpdatePolicy.MaxChangePerTurn < 1 {
				return nil, nil, fmt.Errorf("bounded proposal needs a positive budget")
			}
			if pack.UpdatePolicy.Kind == "rule_only" && pack.UpdatePolicy.MaxChangePerTurn != 0 {
				return nil, nil, fmt.Errorf("state %s rule_only policy cannot declare a proposal budget", pack.ID)
			}
		} else if pack.Type == "boolean" {
			if pack.Minimum != nil || pack.Maximum != nil || len(pack.EnumValues) != 0 || (pack.UpdatePolicy.Kind != "readonly" && pack.UpdatePolicy.Kind != "model") {
				return nil, nil, fmt.Errorf("invalid boolean state definition")
			}
		} else if pack.Type == "enum" {
			seen := map[string]bool{}
			for _, value := range pack.EnumValues {
				if strings.TrimSpace(value) == "" || seen[value] {
					return nil, nil, fmt.Errorf("invalid enum values")
				}
				seen[value] = true
			}
			if len(pack.EnumValues) == 0 || pack.Minimum != nil || pack.Maximum != nil || (pack.UpdatePolicy.Kind != "readonly" && pack.UpdatePolicy.Kind != "model") {
				return nil, nil, fmt.Errorf("invalid enum state definition")
			}
		} else {
			return nil, nil, fmt.Errorf("invalid state type")
		}
		definition := story.StateDefinition{ID: pack.ID, Name: strings.TrimSpace(pack.Name), Type: pack.Type, Minimum: pack.Minimum, Maximum: pack.Maximum, EnumValues: append([]string(nil), pack.EnumValues...), Scope: pack.Scope, Projection: pack.Projection, Knowledge: pack.Knowledge, UpdatePolicy: pack.UpdatePolicy, Description: strings.TrimSpace(pack.Description), Unit: strings.TrimSpace(pack.Unit)}
		value, err := parseStateValue(definition, pack.Default)
		if err != nil {
			return nil, nil, fmt.Errorf("state %s default: %w", pack.ID, err)
		}
		definition.Default = value
		result, byID[definition.ID] = append(result, definition), definition
	}
	return result, byID, nil
}

func initialStateFor(entityID string, npc bool, raw map[string]json.RawMessage, definitions []story.StateDefinition, byID map[string]story.StateDefinition) (map[string]wiaworld.StateValue, error) {
	for id := range raw {
		definition, ok := byID[id]
		if !ok || (npc && definition.Scope == "player") || (!npc && definition.Scope == "npc") {
			return nil, fmt.Errorf("state %s is not defined for %s", id, entityID)
		}
	}
	result := map[string]wiaworld.StateValue{}
	for _, definition := range definitions {
		if (npc && definition.Scope == "player") || (!npc && definition.Scope == "npc") {
			continue
		}
		value := definition.Default
		if encoded, ok := raw[definition.ID]; ok {
			var err error
			value, err = parseStateValue(definition, encoded)
			if err != nil {
				return nil, fmt.Errorf("state %s for %s: %w", definition.ID, entityID, err)
			}
		}
		result[definition.ID] = value
	}
	return result, nil
}

func compileMechanics(pack StoryPack, npcs map[string]PackNPC, locations map[string]PackLocation) ([]story.StateDefinition, map[string]map[string]wiaworld.StateValue, []story.RelationDefinition, []story.InitialRelation, []story.ItemDefinition, []story.InitialItem, error) {
	states, stateByID, err := compileStateDefinitions(pack.StateDefinitions)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	initialStates := map[string]map[string]wiaworld.StateValue{}
	initialStates["player"], err = initialStateFor("player", false, pack.Player.InitialState, states, stateByID)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	entitySet := map[string]bool{"player": true}
	for id, npc := range npcs {
		entitySet[id] = true
		initialStates[id], err = initialStateFor(id, true, npc.InitialState, states, stateByID)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
	}

	relations, relationByID := []story.RelationDefinition{}, map[string]story.RelationDefinition{}
	for _, item := range pack.RelationDefinitions {
		if !packID.MatchString(item.ID) || strings.TrimSpace(item.Name) == "" || relationByID[item.ID].ID != "" || item.Minimum > item.Maximum || item.Default < item.Minimum || item.Default > item.Maximum || item.MaxChangePerTurn < 0 || !slices.Contains([]string{"self", "public", "hidden"}, item.Projection) {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("invalid relation definition")
		}
		value := story.RelationDefinition(item)
		relations, relationByID[value.ID] = append(relations, value), value
	}
	initialRelations := []story.InitialRelation{}
	seenRelations := map[string]bool{}
	for _, item := range pack.InitialRelations {
		definition, ok := relationByID[item.RelationType]
		key := item.SubjectID + "\x00" + item.TargetID + "\x00" + item.RelationType
		if !ok || !entitySet[item.SubjectID] || !entitySet[item.TargetID] || item.SubjectID == item.TargetID || seenRelations[key] || item.Value < definition.Minimum || item.Value > definition.Maximum {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("invalid initial relation")
		}
		seenRelations[key] = true
		initialRelations = append(initialRelations, story.InitialRelation(item))
	}

	items, itemByID := []story.ItemDefinition{}, map[string]bool{}
	for _, item := range pack.ItemDefinitions {
		if !packID.MatchString(item.ID) || strings.TrimSpace(item.Name) == "" || itemByID[item.ID] || !slices.Contains([]string{"public", "holder", "hidden"}, item.Projection) {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("invalid item definition")
		}
		itemByID[item.ID] = true
		items = append(items, story.ItemDefinition(item))
	}
	instances := []story.InitialItem{}
	seenInstances := map[string]bool{}
	for _, item := range pack.ItemInstances {
		_, locationOK := locations[item.LocationID]
		if !packID.MatchString(item.InstanceID) || !itemByID[item.DefinitionID] || seenInstances[item.InstanceID] || (item.HolderID == "") == (item.LocationID == "") || (item.HolderID != "" && !entitySet[item.HolderID]) || (item.LocationID != "" && (!locationOK || locations[item.LocationID].Kind != "place")) {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("invalid item instance")
		}
		seenInstances[item.InstanceID] = true
		instances = append(instances, story.InitialItem(item))
	}
	return states, initialStates, relations, initialRelations, items, instances, nil
}
