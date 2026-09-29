package definition

import (
	"strings"

	"gameagent/backend/internal/session"
	protocolv1alpha2 "gameagent/protocol/gen/go/gameagent/protocol/v1alpha2"
)

const SchemaVersionV1Alpha1 = "v1alpha1"

type GameDefinition struct {
	SchemaVersion        string   `json:"schema_version"`
	GameID               string   `json:"game_id"`
	Title                string   `json:"title,omitempty"`
	Summary              string   `json:"summary,omitempty"`
	WorldRules           []string `json:"world_rules,omitempty"`
	Lore                 []string `json:"lore,omitempty"`
	NarrativeConstraints []string `json:"narrative_constraints,omitempty"`
	SourceVersion        string   `json:"source_version,omitempty"`
}

type AgentDefinition struct {
	SchemaVersion      string   `json:"schema_version"`
	GameID             string   `json:"game_id"`
	DefinitionID       string   `json:"definition_id"`
	Identity           string   `json:"identity,omitempty"`
	Personality        []string `json:"personality,omitempty"`
	SpeechStyle        []string `json:"speech_style,omitempty"`
	Preferences        []string `json:"preferences,omitempty"`
	BehaviorGuidelines []string `json:"behavior_guidelines,omitempty"`
	SourceVersion      string   `json:"source_version,omitempty"`
}

type AgentInstanceDescriptor struct {
	SessionKey   session.AgentSessionKey
	EntityType   string
	DisplayName  string
	DefinitionID string
}

func NewAgentInstanceDescriptor(key session.AgentSessionKey, target *protocolv1alpha2.EntityRef) AgentInstanceDescriptor {
	descriptor := AgentInstanceDescriptor{SessionKey: key}
	if target == nil {
		return descriptor
	}
	descriptor.EntityType = strings.TrimSpace(target.GetEntityType())
	descriptor.DisplayName = strings.TrimSpace(target.GetDisplayName())
	descriptor.DefinitionID = strings.TrimSpace(target.GetDefinitionId())
	return descriptor
}
