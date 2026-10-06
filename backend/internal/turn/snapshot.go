// Package turn owns one turn of the story: the input it runs on, the stages it passes
// through, and the vocabulary of what a turn produces.
//
// A turn is the unit the product is built from — the player says something, characters
// decide, the world moves, the result is narrated and committed — so the types that
// describe that unit live here rather than in whatever assembles them.
package turn

import (
	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

// Snapshot is one turn's frozen input: everything the turn reads before it decides
// anything, taken at one moment so every stage sees the same world.
//
// It is a read, not a state holder. The turn writes its results back through the commit
// path, and nothing outside a turn may edit a snapshot in place.
type Snapshot struct {
	GeneratedEvents GeneratedEventState
	Definition      story.Definition
	Summary         wiaworld.WorldSummary
	SceneLocation   string
	// Positions is the authoritative current place of every spatial entity. It is
	// empty for worlds that have not entered the spatial capability contract.
	Positions                  map[string]string
	PositionSources            map[string]string
	States                     map[string]map[string]wiaworld.EntityState
	Relationships              []wiaworld.Relationship
	AppliedRelationshipSources map[string]bool
	Items                      map[string]wiaworld.ItemInstance
	// RuleResults keeps the first committed event for every authored rule/status
	// pair. Unlike Events, it is not bounded by the recent context window.
	RuleResults map[string]map[string]string
	// InputBudgetTokens is how many input tokens this world's requests may use; the long
	// memory projection reads it so a small model window shrinks the recent window
	// instead of failing the turn.
	InputBudgetTokens int
	PlayerName        string
	PlayerProfile     string
	Narrative         wiaworld.NarrativeSettings
	Bystanders        []string
	BystanderRefs     []story.Bystander
	SceneVersion      int64
	Characters        []wiaworld.Character
	Messages          []wiaworld.Message
	Events            []wiaworld.Event
	Dialogue          []wiaworld.Event
	SceneViews        []SceneView
	Sources           map[string]SourceMetadata
	Perceptions       map[string][]wiaworld.Perception
	// PerceivedSources records durable authorization for current structured
	// facts. It is independent from the bounded recent Perceptions prompt window.
	PerceivedSources map[string]map[string]bool
	Memories         map[string][]wiaworld.Memory
	Plot             *plot.Definition
	PlotProgress     plot.Progress
	LongMemory       map[string]MemoryContext
	OpenProgress     *wiaworld.OpenProgress
	materialReads    *materialReadBudget
	materialGroup    *materialReadGroup
	speechRoster     []string
	elapsedMinutes   int
	// sceneEntities bounds creation rights for the temporary centralized request.
	sceneEntities []string
}

// SceneView is one character's picture of where they are: what they last understood
// their surroundings to be, and which events that understanding came from, so a later
// correction can tell whether the view it invalidates is still the current one.
type SceneView struct {
	Recipient string   `json:"recipient"`
	Content   string   `json:"content"`
	SourceIDs []string `json:"source_ids"`
	Version   int64    `json:"version"`
}

// SourceMetadata is the provenance a projection carries: which event it came from, who
// produced it, and where in the committed stream it sits. It is metadata only — joining
// an event grants a reader these facts, never the event's body.
type SourceMetadata struct {
	ID           string
	Actor        string
	Kind         string
	Seq          int64
	RunID        string
	Stage        int
	SceneVersion int64
}

// MemoryContext is what one scope of memory contributes to a turn: the standing digest
// plus the sources it does not yet cover, split into the part still inside the recent
// window and the part already archived.
type MemoryContext struct {
	Digest  memory.MemoryDigest
	Tail    []memory.MemorySource
	Archive []memory.MemorySource
}

// GeneratedEventState is the world's own open events and how far the world has got in
// offering them.
type GeneratedEventState struct {
	Active        []GeneratedEvent `json:"active"`
	LastOfferTurn int64            `json:"last_offer_turn"`
	Completed     int64            `json:"completed"`
}

// GeneratedEvent is one event the world opened by itself, together with the story node
// that describes it and the events that started and triggered it.
type GeneratedEvent struct {
	Node      plot.Node      `json:"node"`
	State     plot.NodeState `json:"state"`
	StartID   string         `json:"start_id"`
	TriggerID string         `json:"trigger_id"`
	Location  string         `json:"location"`
	Premise   string         `json:"premise"`
}
