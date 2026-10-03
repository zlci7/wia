// Package story holds the frozen runtime definition of a story: what a world was
// started from, normalized and immutable.
//
// It is the data contract between the tools that prepare material and the engine
// that runs it. It performs no I/O, no model calls, no persistence and no turn
// orchestration, and it knows nothing about the external file format a pack uses:
// parsing, schema checks, legacy compatibility, assets and digests belong to
// content, which compiles its packs into values of these types.
//
// The reason for the distinction is that a pack is a file format while this is a
// definition a running world reasons about. They start out looking alike and
// diverge as soon as the format grows a region hierarchy or a normalized legacy
// shape, and a shared structure would drag the format into the engine.
package story

import (
	"strings"

	"gameagent/backend/internal/plot"
	wiaworld "gameagent/backend/internal/world"
)

// CharacterByID finds one frozen runtime character by its stable entity ID.
func CharacterByID(definition Definition, id string) (wiaworld.Character, bool) {
	id = strings.TrimSpace(id)
	for _, character := range definition.Characters {
		if character.EntityID == id {
			return character, true
		}
	}
	return wiaworld.Character{}, false
}

// LocationIDFor maps a stored scene description back to its frozen location.
// Scene text may add detail after the location name, so exact and prefix matches
// are both accepted.
func LocationIDFor(definition Definition, scene string) string {
	scene = strings.TrimSpace(scene)
	if scene == "" {
		return ""
	}
	for _, location := range definition.Locations {
		if location.Name == scene {
			return location.ID
		}
	}
	for _, location := range definition.Locations {
		if location.Name != "" && strings.HasPrefix(scene, location.Name) {
			return location.ID
		}
	}
	return ""
}

// Location is a place a story starts in and the places reachable from it, after
// validation and normalization. The pack format has its own representation of this.
//
// Like Bystander below, the JSON names are the shape a world's frozen definition has
// always been stored under, so they are part of the contract with existing saves.
type Location struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Connections []string `json:"connections"`
	Public      bool     `json:"public"`
}

// LocationByID resolves one frozen location.
func LocationByID(definition Definition, id string) (Location, bool) {
	for _, location := range definition.Locations {
		if location.ID == id {
			return location, true
		}
	}
	return Location{}, false
}

// PlaceGraph returns the directed graph of places. Regions never appear as nodes.
func PlaceGraph(definition Definition) map[string][]string {
	result := map[string][]string{}
	for _, location := range definition.Locations {
		if location.Kind == "place" || location.Kind == "" {
			result[location.ID] = append([]string(nil), location.Connections...)
		}
	}
	return result
}

// Bystander is a background person the story may place in the world: stable identity,
// display name, and the public material used to present them.
//
// The JSON names are the wire contract this shape has always had: a world's frozen
// definition is stored as JSON and read back by later releases, and the API hands the
// bystander list to the client. Without the tags the fields would serialise under their
// Go names, which both breaks the stored form and changes what the client sees.
type Bystander struct {
	BystanderID     string `json:"bystander_id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	InitialLocation string `json:"initial_location,omitempty"`
	Avatar          string `json:"avatar,omitempty"`
}

// Summary is the running world's view of what this story is. It carries only what a
// turn needs to read; the catalog the content tooling and the API display is a
// separate, wider shape that belongs to them — a cover URL, for instance, is a route
// the API serves, not part of the definition.
//
// The story is identified once, by ID. There is deliberately no second field holding the
// same value: a world's frozen definition is read back from storage, and a duplicate
// identifier is one more thing that can disagree with itself.
type Summary struct {
	ID          string
	Revision    string
	Title       string
	Mode        string
	Gameplay    string
	Description string
	Player      Player
}

// Player is the fixed player character a story may prescribe. Editable false means
// the story requires this character and a world may not replace it.
type Player struct {
	Name     string
	Profile  string
	Editable bool
}

// Definition is everything a world needs to initialize and run its story: the
// author's material as the engine consumes it, not as the pack stores it. Its
// locations and bystanders are the normalized shapes, never the pack's own.
type Definition struct {
	SchemaVersion    int
	Capabilities     map[string]int
	Revision         string
	Background       string
	Rules            string
	Locations        []Location
	InitialLocations map[string]string
	Settings         wiaworld.NarrativeSettings
	SettingsSource   string
	Summary          Summary
	Opening          string
	// Scene is the authored opening scene description, kept beside Opening.
	Scene string
	// InitialLocation is the identifier of the scene the story starts in.
	InitialLocation string
	Clock           string
	Calendar        *wiaworld.Calendar `json:"calendar,omitempty"`
	KnownLocations  []string           `json:"known_locations,omitempty"`
	Characters      []wiaworld.Character
	// Bystanders are the display names; BystanderRefs carries the normalized
	// definitions next to them so identity survives the pack boundary.
	Bystanders          []string
	BystanderRefs       []Bystander
	Secret              string
	Plot                *plot.Definition
	EventGeneration     *plot.EventGenerationPolicy
	StateDefinitions    []StateDefinition                         `json:"state_definitions,omitempty"`
	InitialStates       map[string]map[string]wiaworld.StateValue `json:"initial_states,omitempty"`
	RelationDefinitions []RelationDefinition                      `json:"relation_definitions,omitempty"`
	InitialRelations    []InitialRelation                         `json:"initial_relations,omitempty"`
	ItemDefinitions     []ItemDefinition                          `json:"item_definitions,omitempty"`
	InitialItems        []InitialItem                             `json:"initial_items,omitempty"`
	ActionRules         []ActionRule                              `json:"action_rules,omitempty"`
	Materials           []Material                                `json:"materials,omitempty"`
	Progression         *plot.OpenDefinition                      `json:"progression,omitempty"`
}
