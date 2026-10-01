package story

import wiaworld "gameagent/backend/internal/world"

type StateUpdatePolicy struct {
	Kind             string `json:"kind"`
	MaxChangePerTurn int    `json:"max_change_per_turn,omitempty"`
}

// StateDefinition gives engine-neutral meaning and limits to an author-defined
// value. The engine never assigns semantics to the identifier or display name.
type StateDefinition struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Minimum      *int                `json:"minimum,omitempty"`
	Maximum      *int                `json:"maximum,omitempty"`
	EnumValues   []string            `json:"enum_values,omitempty"`
	Default      wiaworld.StateValue `json:"default"`
	Scope        string              `json:"scope"`
	Projection   string              `json:"projection"`
	Knowledge    string              `json:"knowledge"`
	UpdatePolicy StateUpdatePolicy   `json:"update_policy"`
	Description  string              `json:"description,omitempty"`
	Unit         string              `json:"unit,omitempty"`
}

type RelationDefinition struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Minimum          int    `json:"minimum"`
	Maximum          int    `json:"maximum"`
	Default          int    `json:"default"`
	MaxChangePerTurn int    `json:"max_change_per_turn"`
	Projection       string `json:"projection"`
	Description      string `json:"description,omitempty"`
}

type InitialRelation struct {
	SubjectID    string `json:"subject_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
	Value        int    `json:"value"`
}

type ItemDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Projection  string `json:"projection"`
}

type InitialItem struct {
	InstanceID   string `json:"instance_id"`
	DefinitionID string `json:"definition_id"`
	HolderID     string `json:"holder_id,omitempty"`
	LocationID   string `json:"location_id,omitempty"`
}
