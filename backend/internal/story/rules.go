package story

import "gameagent/backend/internal/plot"

// ActionRule is an authored, program-settled action available to the intent stage.
// The model may select a rule, but it never supplies the roll, threshold or effects.
type ActionRule struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Guidance       string               `json:"guidance"`
	Conditions     []plot.FactCondition `json:"conditions,omitempty"`
	Risk           *RiskRule            `json:"risk,omitempty"`
	SuccessText    string               `json:"success_text"`
	FailureText    string               `json:"failure_text,omitempty"`
	SuccessEffects []RuleEffect         `json:"success_effects,omitempty"`
	FailureEffects []RuleEffect         `json:"failure_effects,omitempty"`
}

// RiskRule is the single supported percentile roll-under contract. A roll at or
// below the final target succeeds. Authored modifiers are activated only by facts
// the program can read from the turn working state.
type RiskRule struct {
	BaseTarget int            `json:"base_target"`
	Minimum    int            `json:"minimum"`
	Maximum    int            `json:"maximum"`
	Modifiers  []RiskModifier `json:"modifiers,omitempty"`
}

type RiskModifier struct {
	Label     string             `json:"label"`
	Amount    int                `json:"amount"`
	Condition plot.FactCondition `json:"condition"`
}

// RuleEffect is deliberately small. More effect kinds can be versioned later;
// v1 settles only an integer state delta through the existing state work state.
type RuleEffect struct {
	Kind     string `json:"kind"`
	EntityID string `json:"entity_id"`
	StateID  string `json:"state_id"`
	Delta    int    `json:"delta"`
}
