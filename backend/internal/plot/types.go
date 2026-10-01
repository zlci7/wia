// Package plot owns the world's story line: the frozen definition of its nodes and
// the progress a world has made through them.
//
// It holds the definition of the plot and the pure rules that decide whether a
// definition and a progress are legal. It does not decide how characters respond,
// how the story is narrated, or how anything is stored: a narrative node carries
// author material, and a turning point is resolved by the turn that owns the model
// call.
//
// Both the content tooling and the running story validate through this package. The
// rules for what makes a legal plot graph exist once, here, so a pack that publishes
// cannot be one the runtime later refuses.
package plot

// Definition is the frozen story line of one world. Conditions are author material,
// while dependencies, time boundaries and audience are code contracts.
type Definition struct {
	Revision string `json:"revision"`
	Facts    string `json:"facts"`
	Nodes    []Node `json:"nodes"`
}

// Node is one potential development, eligible once its dependencies are met and its
// time has come.
type Node struct {
	ID           string          `json:"id"`
	After        []string        `json:"after"`
	AtMinute     int             `json:"at_minute"`
	Condition    string          `json:"condition"`
	Development  string          `json:"development"`
	Audience     []string        `json:"audience"`
	Terminal     bool            `json:"terminal"`
	Requirements []FactCondition `json:"requirements,omitempty"`
	OnUnmet      string          `json:"on_unmet,omitempty"`
}

// FactCondition is the shared, data-only vocabulary used by authored action rules
// and plot gates. The turn projects its current working state into these facts.
// EntityID may be "actor" only inside an ActionRule; plot nodes use concrete IDs.
type FactCondition struct {
	Kind       string `json:"kind"`
	EntityID   string `json:"entity_id,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	FactID     string `json:"fact_id,omitempty"`
	LocationID string `json:"location_id,omitempty"`
	Value      int    `json:"value,omitempty"`
	Status     string `json:"status,omitempty"`
}

// NodeState is what happened to one node: whether it occurred, was deferred, or was
// skipped, and which event settled it.
type NodeState struct {
	Status    string   `json:"status"`
	EventID   string   `json:"event_id,omitempty"`
	Content   string   `json:"content,omitempty"`
	NextCheck int      `json:"next_check"`
	Evidence  []string `json:"evidence"`
}

// Progress is how far this world has come through the definition.
type Progress struct {
	Version int64                `json:"version"`
	Nodes   map[string]NodeState `json:"nodes"`
	Ending  string               `json:"ending,omitempty"`
}
