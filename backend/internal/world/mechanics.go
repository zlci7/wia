package world

// StateValue is the typed value of one author-defined state. Exactly one value
// field is meaningful according to Type; keeping the discriminator with the
// value makes persisted snapshots self-checking.
type StateValue struct {
	Type    string `json:"type"`
	Integer int    `json:"integer,omitempty"`
	Boolean bool   `json:"boolean,omitempty"`
	Enum    string `json:"enum,omitempty"`
}

// EntityState is one committed state value and its provenance.
type EntityState struct {
	EntityID    string     `json:"entity_id"`
	StateID     string     `json:"state_id"`
	Value       StateValue `json:"value"`
	SourceEvent string     `json:"source_event_id"`
	UpdatedTurn int64      `json:"updated_turn"`
	Version     int64      `json:"version"`
}

// Relationship is one subject's directed assessment of another entity.
type Relationship struct {
	SubjectID    string `json:"subject_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
	Value        int    `json:"value"`
	SourceEvent  string `json:"source_event_id"`
	UpdatedTurn  int64  `json:"updated_turn"`
	Version      int64  `json:"version"`
}

// ItemInstance is the single authoritative placement of an authored item.
// Exactly one of HolderID and LocationID is non-empty.
type ItemInstance struct {
	InstanceID   string `json:"instance_id"`
	DefinitionID string `json:"definition_id"`
	HolderID     string `json:"holder_id,omitempty"`
	LocationID   string `json:"location_id,omitempty"`
	SourceEvent  string `json:"source_event_id"`
	UpdatedTurn  int64  `json:"updated_turn"`
	Version      int64  `json:"version"`
}

// PublicState is the player-facing projection of an allowed state.
type PublicState struct {
	EntityID string     `json:"entity_id"`
	StateID  string     `json:"state_id"`
	Name     string     `json:"name"`
	Value    StateValue `json:"value"`
	Unit     string     `json:"unit,omitempty"`
}

// PublicItem is the player-facing projection of an item the player holds or can
// currently perceive.
type PublicItem struct {
	InstanceID   string `json:"instance_id"`
	DefinitionID string `json:"definition_id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	HolderID     string `json:"holder_id,omitempty"`
	LocationID   string `json:"location_id,omitempty"`
}
