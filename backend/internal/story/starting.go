package story

import (
	"fmt"
	"maps"
	"slices"
)

// WithStartingOption derives a session's initial conditions without modifying its pack.
func WithStartingOption(def Definition, id string) (Definition, error) {
	if id == "" && len(def.StartingOptions) > 0 {
		id = def.StartingOptions[0].ID
	}
	if id == "" {
		return def, nil
	}
	for _, option := range def.StartingOptions {
		if option.ID != id {
			continue
		}
		def.Summary.Player = option.Player
		def.Opening, def.InitialLocation = option.Opening, option.InitialLocation
		location, ok := LocationByID(def, option.InitialLocation)
		if !ok {
			return Definition{}, fmt.Errorf("starting option has no place")
		}
		def.Scene = location.Name
		def.InitialLocations = maps.Clone(def.InitialLocations)
		def.InitialLocations["player"] = option.InitialLocation
		def.KnownLocations = slices.Clone(option.KnownLocations)
		def.InitialStates = maps.Clone(def.InitialStates)
		def.InitialStates["player"] = maps.Clone(option.States)
		def.InitialItems = slices.Clone(option.Items)
		def.Characters = slices.Clone(def.Characters)
		for i := range def.Characters {
			def.Characters[i].InScene = def.InitialLocations[def.Characters[i].EntityID] == def.InitialLocation
		}
		return def, nil
	}
	return Definition{}, fmt.Errorf("unknown starting option %q", id)
}
