// Package world holds the domain vocabulary of a story: who exists, what
// happened, what they perceived, what they remember, and the identifiers that
// name worlds, characters and locations.
//
// These types are the shared language of the narrative modules. They live here
// rather than in whichever module used them first because every other module
// depends on them, and a type that every module must import from one particular
// module makes that module a hub by accident.
//
// This package holds concepts and the pure rules that read them. It does not
// touch a database, call a model, or understand HTTP; the primitives that remain
// once the concepts are named (text normalization, timestamps, JSON encoding,
// identity generation) belong to the wire package instead.
package world

import (
	"time"
)

// WorldSummary is the metadata of one save: what it is running, how far it has
// advanced, and the version counters a caller can use to detect a stale view.
type WorldSummary struct {
	GameTitle    string    `json:"game_title"`
	Revision     string    `json:"revision"`
	GameID       string    `json:"game_id"`
	WorldID      string    `json:"world_id"`
	Name         string    `json:"name"`
	Mode         string    `json:"mode"`
	TurnSeq      int64     `json:"turn_seq"`
	MessageHead  int64     `json:"message_head"`
	EventHead    int64     `json:"event_head"`
	ContextEpoch int64     `json:"context_epoch"`
	Clock        string    `json:"clock"`
	Calendar     *Calendar `json:"calendar,omitempty"`
	Scene        string    `json:"scene"`
	// SceneLocation is the current location's identifier. Presence is decided by
	// identity, not by comparing human-readable scene text.
	SceneLocation     string         `json:"scene_location,omitempty"`
	Location          *LocationView  `json:"location,omitempty"`
	AdjacentLocations []LocationView `json:"adjacent_locations,omitempty"`
	Status            string         `json:"status"`
	StoryEnded        bool           `json:"story_ended"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// LocationView is the player-visible projection of a place. It contains only places
// the frozen story definition allows the player to discover from the current place.
type LocationView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Character is one important person in a world, with the definition material a
// turn needs to speak through them.
type Character struct {
	DefinitionRevision string `json:"definition_revision,omitempty"`
	Appearance         string `json:"appearance,omitempty"`
	// Avatar is a package-relative asset reference from the definition, not a world
	// resource name; the world snapshot records its own copy separately.
	Avatar          string `json:"avatar,omitempty"`
	EntityID        string `json:"entity_id"`
	DefinitionID    string `json:"definition_id"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	Profile         string `json:"profile"`
	Knowledge       string `json:"knowledge"`
	InitialConcerns string `json:"initial_concerns"`
	// SpeakingExamples are authored dialogue samples showing how this character
	// sounds. They are style material, never events that happened.
	SpeakingExamples []string `json:"speaking_examples,omitempty"`
	InScene          bool     `json:"in_scene"`
}

// PublicCharacter is the player-facing character projection. Private role
// material stays inside the story runtime and is never sent through ordinary
// play routes.
type PublicCharacter struct {
	Appearance   string `json:"appearance,omitempty"`
	EntityID     string `json:"entity_id"`
	DefinitionID string `json:"definition_id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	InScene      bool   `json:"in_scene"`
}

// Message is one committed narrative message of a world.
type Message struct {
	Seq       int64     `json:"seq"`
	MessageID string    `json:"message_id"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	RunID     string    `json:"run_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Event is one committed fact of a world. Stage and SceneVersion record which
// phase of a turn produced it and which scene projection it belongs to.
type Event struct {
	ProjectionParentID string    `json:"-"`
	BasisEventIDs      []string  `json:"-"`
	Seq                int64     `json:"seq"`
	EventID            string    `json:"event_id"`
	EventType          string    `json:"event_type"`
	ActorID            string    `json:"actor_id,omitempty"`
	TargetID           string    `json:"target_id,omitempty"`
	Content            string    `json:"content"`
	RunID              string    `json:"run_id"`
	Stage              int       `json:"stage"`
	SceneVersion       int64     `json:"scene_version"`
	SourceType         string    `json:"source_type"`
	CreatedAt          time.Time `json:"created_at"`
}

// Perception is what one recipient was allowed to observe from an event.
type Perception struct {
	Seq           int64     `json:"seq"`
	RecipientID   string    `json:"recipient_id"`
	SourceEventID string    `json:"source_event_id"`
	SourceType    string    `json:"source_type"`
	Content       string    `json:"content"`
	Stage         int       `json:"stage"`
	SceneVersion  int64     `json:"scene_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// Memory is one character's own record of what they experienced or judged.
type Memory struct {
	Seq           int64     `json:"seq"`
	RecipientID   string    `json:"recipient_id"`
	Kind          string    `json:"kind"`
	Content       string    `json:"content"`
	SourceEventID string    `json:"source_event_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// Run is one attempt to advance a world by a single player input.
type Run struct {
	RunID                string    `json:"run_id"`
	RequestKey           string    `json:"request_key"`
	RequestHash          string    `json:"request_hash"`
	Input                string    `json:"input"`
	AddresseeID          string    `json:"addressee_id,omitempty"`
	Attempt              int       `json:"attempt"`
	Status               string    `json:"status"`
	Reason               string    `json:"reason,omitempty"`
	Error                string    `json:"error,omitempty"`
	MessageSeq           int64     `json:"message_seq,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
	InputID              string    `json:"-"`
	InputSeq             int64     `json:"-"`
	BaseTurnSeq          int64     `json:"-"`
	BaseMessageHead      int64     `json:"-"`
	BaseEventHead        int64     `json:"-"`
	BaseContextEpoch     int64     `json:"-"`
	BaseSceneVersion     int64     `json:"-"`
	PreparedActionRuleID string    `json:"-"`
	// InputPart identifies an ordered segment within one atomic input.
	InputPart int `json:"-"`
}

// PublicCharacterViews projects characters for the player. Private role material
// never crosses this boundary.
func PublicCharacterViews(characters []Character) []PublicCharacter {
	views := make([]PublicCharacter, 0, len(characters))
	for _, character := range characters {
		views = append(views, PublicCharacter{
			Appearance: character.Appearance,
			EntityID:   character.EntityID, DefinitionID: character.DefinitionID,
			Name: character.Name, Role: character.Role, InScene: character.InScene,
		})
	}
	return views
}

// SceneCharacters returns the characters currently present in the scene.
func SceneCharacters(characters []Character) []Character {
	result := make([]Character, 0, len(characters))
	for _, character := range characters {
		if character.InScene {
			result = append(result, character)
		}
	}
	return result
}

// ContainsID reports whether a list holds an identifier.
func ContainsID(values []string, id string) bool {
	for _, value := range values {
		if value == id {
			return true
		}
	}
	return false
}

// CharacterIDs lists the identifiers of a character slice.
func CharacterIDs(characters []Character) []string {
	result := make([]string, 0, len(characters))
	for _, character := range characters {
		result = append(result, character.EntityID)
	}
	return result
}
