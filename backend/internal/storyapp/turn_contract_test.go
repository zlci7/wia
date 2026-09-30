package storyapp

// These mirrors describe model JSON used by application integration tests. The
// runtime contract is private to turn; tests need only serialize and inspect it.
type hostResult struct {
	EventOpportunity *eventOpportunity  `json:"event_opportunity,omitempty"`
	InterruptSources []string           `json:"interrupt_source_ids,omitempty"`
	TimeMinutes      int                `json:"time_minutes"`
	Scene            string             `json:"scene"`
	SceneCharacters  []string           `json:"scene_characters"`
	SceneUpdates     []sceneUpdate      `json:"scene_updates"`
	Outcomes         []hostActionResult `json:"outcomes"`
}

type hostActionResult struct {
	ActionID   string   `json:"action_id"`
	Status     string   `json:"status"`
	Content    string   `json:"content"`
	Recipients []string `json:"recipients"`
	Bystanders []string `json:"bystanders,omitempty"`
}

type sceneUpdate struct {
	Content    string   `json:"content"`
	SourceIDs  []string `json:"source_ids"`
	Recipients []string `json:"recipients"`
}

type sceneSource struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	Recipients []string `json:"recipients"`
}

type eventOpportunity struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	ActionID string `json:"action_id"`
}

type plotProjection struct {
	Recipient string `json:"recipient"`
	Content   string `json:"content"`
	Scene     string `json:"scene,omitempty"`
}

type plotResolution struct {
	Status           string           `json:"status"`
	Content          string           `json:"content"`
	SourceIDs        []string         `json:"source_ids"`
	Projections      []plotProjection `json:"projections"`
	DecisionRequests []string         `json:"decision_requests"`
	Ending           string           `json:"ending"`
}

type plotActionResult struct {
	hostActionResult
	ActorInScene *bool `json:"actor_in_scene,omitempty"`
}

type eventCandidate struct {
	Condition    string         `json:"condition"`
	Development  string         `json:"development"`
	AfterMinutes int            `json:"after_minutes"`
	Initial      plotResolution `json:"initial"`
}
