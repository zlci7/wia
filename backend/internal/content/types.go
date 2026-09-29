// Package content owns the author's material: the story packs a world is started from, and the shape a pack has to have.
package content

type GameSummary struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Modes       []string       `json:"modes"`
	DefaultMode string         `json:"default_mode"`
	Revision    string         `json:"revision"`
	Mode        string         `json:"mode"`
	Gameplay    string         `json:"gameplay"`
	Background  string         `json:"background"`
	CoverURL    string         `json:"cover_url,omitempty"`
	CoverAlt    string         `json:"cover_alt,omitempty"`
	Player      PlayerDefaults `json:"player"`
}

// PackBystander is the normalized bystander definition.
type PackBystander struct {
	BystanderID     string `json:"bystander_id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	InitialLocation string `json:"initial_location,omitempty"`
	Avatar          string `json:"avatar,omitempty"`
}

type PlayerDefaults struct {
	Name         string `json:"name"`
	Profile      string `json:"profile"`
	Requirements string `json:"requirements"`
	Editable     bool   `json:"editable"`
}

type PackLocation struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Connections []string `json:"connections"`
}
