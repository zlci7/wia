package world

// PersonalPlan is an intention, not an action result. Source IDs belong to the
// owner's knowledge; a later decision can revise, pause, or end that intention.
type PersonalPlan struct {
	ID           string   `json:"id"`
	OwnerID      string   `json:"owner_id"`
	Content      string   `json:"content"`
	SourceIDs    []string `json:"source_ids"`
	NextCheck    int      `json:"next_check"`
	LastCheck    int      `json:"last_check"`
	Status       string   `json:"status"`
	Version      int64    `json:"version"`
	ContextEpoch int64    `json:"context_epoch"`
}

type OpenProgress struct {
	Plans             []PersonalPlan    `json:"plans"`
	ExternalApplied   map[string]string `json:"external_applied"`
	DevelopmentChecks map[string]string `json:"development_checks"`
	DevelopmentCursor string            `json:"development_cursor,omitempty"`
}
