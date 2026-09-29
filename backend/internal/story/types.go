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
	"gameagent/backend/internal/plot"
	wiaworld "gameagent/backend/internal/world"
)

// Location is a place a story starts in and the places reachable from it, after
// validation and normalization. The pack format has its own representation of this.
type Location struct {
	ID          string
	Name        string
	Description string
	Connections []string
}

// Bystander is a background person the story may place in the world: stable identity,
// display name, and the public material used to present them.
type Bystander struct {
	BystanderID     string
	Name            string
	Description     string
	InitialLocation string
	Avatar          string
}

// Summary is the running world's view of what this story is. It carries only what a
// turn needs to read; the catalog the content tooling and the API display is a
// separate, wider shape that belongs to them.
type Summary struct {
	GameID   string
	Revision string
	Title    string
	Mode     string
	Gameplay string
}

// Player is the fixed player character a story may prescribe.
type Player struct {
	Name     string
	Profile  string
	Editable bool
}

// Definition is everything a world needs to initialize and run its story: the
// author's material as the engine consumes it, not as the pack stores it.
type Definition struct {
	Revision         string
	Background       string
	Rules            string
	Locations        []Location
	InitialLocations map[string]string
	Settings         wiaworld.NarrativeSettings
	SettingsSource   string
	Summary          Summary
	Player           Player
	Opening          string
	InitialLocation  string
	Clock            string
	Characters       []wiaworld.Character
	Bystanders       []Bystander
	Secret           string
	Plot             *plot.Definition
	EventGeneration  *plot.EventGenerationPolicy
}
