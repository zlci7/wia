package turn

import (
	"fmt"
	"slices"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

// PlayerLocationProjection includes authored public knowledge and the current
// scene's public exits. The player's actual place remains visible; unrelated
// hidden places and remote character positions are excluded.
func PlayerLocationProjection(snapshot Snapshot) []wiaworld.KnownLocation {
	known := map[string]bool{}
	for _, id := range snapshot.Definition.KnownLocations {
		if location, ok := story.LocationByID(snapshot.Definition, id); ok && location.Public {
			known[id] = true
		}
	}
	current := snapshot.SceneLocation
	if snapshot.Positions["player"] != "" {
		current = snapshot.Positions["player"]
	}
	if location, ok := story.LocationByID(snapshot.Definition, current); ok {
		known[current] = true
		for _, id := range location.Connections {
			if exit, ok := story.LocationByID(snapshot.Definition, id); ok && exit.Public {
				known[id] = true
			}
		}
	}
	for _, location := range snapshot.Definition.Locations {
		if known[location.ID] && location.Parent != "" {
			if parent, ok := story.LocationByID(snapshot.Definition, location.Parent); ok && parent.Public {
				known[parent.ID] = true
			}
		}
	}
	result := []wiaworld.KnownLocation{}
	for _, location := range snapshot.Definition.Locations {
		if !known[location.ID] {
			continue
		}
		kind := location.Kind
		if kind == "" {
			kind = "place"
		}
		view := wiaworld.KnownLocation{ID: location.ID, Name: location.Name, Description: location.Description, Kind: kind, Connections: []string{}}
		if known[location.Parent] {
			view.Parent = location.Parent
		}
		for _, id := range location.Connections {
			if known[id] {
				view.Connections = append(view.Connections, id)
			}
		}
		result = append(result, view)
	}
	slices.SortFunc(result, func(a, b wiaworld.KnownLocation) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return result
}

func validateWorldInformation(definition story.Definition) error {
	hasInformation := definition.Calendar != nil || len(definition.KnownLocations) > 0
	if definition.Calendar != nil && (definition.Calendar.Kind != "gregorian" || !plot.IsDateClock(definition.Clock)) {
		return fmt.Errorf("invalid frozen calendar")
	}
	if definition.Calendar == nil && plot.IsDateClock(definition.Clock) {
		return fmt.Errorf("dated clock is missing its calendar")
	}
	for _, state := range definition.StateDefinitions {
		hasInformation = hasInformation || state.Category != "" || state.Currency != nil
		if state.Category != "" && !slices.Contains([]string{"condition", "skill", "resource"}, state.Category) {
			return fmt.Errorf("invalid frozen state category")
		}
		if state.Currency != nil {
			if state.Type != "integer" || state.Minimum == nil || *state.Minimum < 0 || state.Category != "resource" {
				return fmt.Errorf("invalid frozen currency state")
			}
			if err := state.Currency.Validate(); err != nil {
				return err
			}
		}
	}
	if hasInformation != (definition.Capabilities["world_info"] == 1) {
		return fmt.Errorf("frozen world information capability mismatch")
	}
	seen := map[string]bool{}
	for _, id := range definition.KnownLocations {
		location, ok := story.LocationByID(definition, id)
		if !ok || !location.Public || seen[id] {
			return fmt.Errorf("invalid frozen known location")
		}
		seen[id] = true
	}
	return nil
}
