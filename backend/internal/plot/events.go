package plot

// EventGenerationPolicy describes how the world opens new events on its own while
// the story runs: which places and people may take part, how many may be open at
// once, and how long the world waits before offering another.
//
// It sits here because these are progression rules for the story line, not
// properties of any one pack format: the content tooling parses a pack into this
// shape, and the turn reads it to decide what the world may do next.
type EventGenerationPolicy struct {
	Scope         string   `json:"scope"`
	Locations     []string `json:"locations"`
	Participants  []string `json:"participants"`
	MaxActive     int      `json:"max_active"`
	CooldownTurns int64    `json:"cooldown_turns"`
}
