package definition

import (
	"fmt"
	"strings"
)

type Catalog struct {
	games  map[string]GameDefinition
	agents map[agentCatalogKey]AgentDefinition
}

type agentCatalogKey struct {
	gameID       string
	definitionID string
}

func NewCatalog(games []GameDefinition, agents []AgentDefinition) (Catalog, error) {
	catalog := Catalog{
		games:  make(map[string]GameDefinition, len(games)),
		agents: make(map[agentCatalogKey]AgentDefinition, len(agents)),
	}

	for _, game := range games {
		normalized, err := normalizeGameDefinition(game)
		if err != nil {
			return Catalog{}, err
		}
		if _, exists := catalog.games[normalized.GameID]; exists {
			return Catalog{}, fmt.Errorf("duplicate game_id %q", normalized.GameID)
		}
		catalog.games[normalized.GameID] = normalized
	}

	for _, agent := range agents {
		normalized, err := normalizeAgentDefinition(agent)
		if err != nil {
			return Catalog{}, err
		}
		key := agentCatalogKey{gameID: normalized.GameID, definitionID: normalized.DefinitionID}
		if _, exists := catalog.agents[key]; exists {
			return Catalog{}, fmt.Errorf("duplicate agent definition %q for game_id %q", normalized.DefinitionID, normalized.GameID)
		}
		catalog.agents[key] = normalized
	}

	return catalog, nil
}

func (c Catalog) FindGame(gameID string) (GameDefinition, bool) {
	if c.games == nil {
		return GameDefinition{}, false
	}
	game, ok := c.games[strings.TrimSpace(gameID)]
	if !ok {
		return GameDefinition{}, false
	}
	return copyGameDefinition(game), true
}

func (c Catalog) FindAgent(gameID string, definitionID string) (AgentDefinition, bool) {
	if c.agents == nil {
		return AgentDefinition{}, false
	}
	agent, ok := c.agents[agentCatalogKey{
		gameID:       strings.TrimSpace(gameID),
		definitionID: strings.TrimSpace(definitionID),
	}]
	if !ok {
		return AgentDefinition{}, false
	}
	return copyAgentDefinition(agent), true
}

func normalizeGameDefinition(game GameDefinition) (GameDefinition, error) {
	game.SchemaVersion = strings.TrimSpace(game.SchemaVersion)
	game.GameID = strings.TrimSpace(game.GameID)
	if game.SchemaVersion == "" {
		return GameDefinition{}, fmt.Errorf("game definition schema_version is required")
	}
	if game.SchemaVersion != SchemaVersionV1Alpha1 {
		return GameDefinition{}, fmt.Errorf("unsupported schema_version %q for game definition %q", game.SchemaVersion, game.GameID)
	}
	if game.GameID == "" {
		return GameDefinition{}, fmt.Errorf("game definition game_id is required")
	}
	return copyGameDefinition(game), nil
}

func normalizeAgentDefinition(agent AgentDefinition) (AgentDefinition, error) {
	agent.SchemaVersion = strings.TrimSpace(agent.SchemaVersion)
	agent.GameID = strings.TrimSpace(agent.GameID)
	agent.DefinitionID = strings.TrimSpace(agent.DefinitionID)
	if agent.SchemaVersion == "" {
		return AgentDefinition{}, fmt.Errorf("agent definition schema_version is required")
	}
	if agent.SchemaVersion != SchemaVersionV1Alpha1 {
		return AgentDefinition{}, fmt.Errorf("unsupported schema_version %q for agent definition %q", agent.SchemaVersion, agent.DefinitionID)
	}
	if agent.GameID == "" {
		return AgentDefinition{}, fmt.Errorf("agent definition game_id is required")
	}
	if agent.DefinitionID == "" {
		return AgentDefinition{}, fmt.Errorf("agent definition definition_id is required")
	}
	return copyAgentDefinition(agent), nil
}

func copyGameDefinition(game GameDefinition) GameDefinition {
	game.WorldRules = cloneStrings(game.WorldRules)
	game.Lore = cloneStrings(game.Lore)
	game.NarrativeConstraints = cloneStrings(game.NarrativeConstraints)
	return game
}

func copyAgentDefinition(agent AgentDefinition) AgentDefinition {
	agent.Personality = cloneStrings(agent.Personality)
	agent.SpeechStyle = cloneStrings(agent.SpeechStyle)
	agent.Preferences = cloneStrings(agent.Preferences)
	agent.BehaviorGuidelines = cloneStrings(agent.BehaviorGuidelines)
	return agent
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
