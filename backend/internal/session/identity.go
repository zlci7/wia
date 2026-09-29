package session

import (
	"fmt"
	"strings"
)

type AgentSessionKey struct {
	GameID   string
	WorldID  string
	EntityID string
}

func Resolve(gameID, worldID, entityID string) (AgentSessionKey, error) {
	if strings.TrimSpace(gameID) == "" {
		return AgentSessionKey{}, fmt.Errorf("game_id is required")
	}
	if strings.TrimSpace(worldID) == "" {
		return AgentSessionKey{}, fmt.Errorf("world_id is required")
	}
	if strings.TrimSpace(entityID) == "" {
		return AgentSessionKey{}, fmt.Errorf("entity_id is required")
	}

	return AgentSessionKey{
		GameID:   gameID,
		WorldID:  worldID,
		EntityID: entityID,
	}, nil
}

func (k AgentSessionKey) DiagnosticID() string {
	return fmt.Sprintf("game=%q world=%q entity=%q", k.GameID, k.WorldID, k.EntityID)
}
