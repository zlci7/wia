package gateway

import (
	"testing"

	"gameagent/backend/internal/agent"
	protocolv1alpha2 "gameagent/protocol/gen/go/gameagent/protocol/v1alpha2"
)

func TestResolveAgentTargetAcceptsDuplicateIdenticalTargetEntity(t *testing.T) {
	event := targetResolutionEvent(" npc:Abigail ")
	event.Entities = []*protocolv1alpha2.EntityRef{
		{
			EntityId:     "npc:Abigail",
			EntityType:   "npc",
			DisplayName:  "Abigail",
			DefinitionId: "npc:Abigail",
		},
		{
			EntityId:     " npc:Abigail ",
			EntityType:   " npc ",
			DisplayName:  " Abigail ",
			DefinitionId: " npc:Abigail ",
		},
	}

	resolved, ackErr := resolveAgentTarget(agent.ConnectionContext{GameID: "stardew-valley"}, event)
	if ackErr != nil {
		t.Fatalf("resolveAgentTarget returned error: %+v", ackErr)
	}
	if resolved.Key.GameID != "stardew-valley" || resolved.Key.WorldID != "world-a" || resolved.Key.EntityID != "npc:Abigail" {
		t.Fatalf("AgentSessionKey = %+v, want stardew-valley/world-a/npc:Abigail", resolved.Key)
	}
	if resolved.Target.GetEntityId() != "npc:Abigail" {
		t.Fatalf("Target.EntityId = %q, want npc:Abigail", resolved.Target.GetEntityId())
	}
	if resolved.Target.GetEntityType() != "npc" {
		t.Fatalf("Target.EntityType = %q, want npc", resolved.Target.GetEntityType())
	}
	if resolved.Target.GetDisplayName() != "Abigail" {
		t.Fatalf("Target.DisplayName = %q, want Abigail", resolved.Target.GetDisplayName())
	}
	if resolved.Target.GetDefinitionId() != "npc:Abigail" {
		t.Fatalf("Target.DefinitionId = %q, want npc:Abigail", resolved.Target.GetDefinitionId())
	}
}

func TestResolveAgentTargetRejectsMissingTargetEntity(t *testing.T) {
	event := targetResolutionEvent("npc:Missing")
	event.Entities = []*protocolv1alpha2.EntityRef{
		{EntityId: "npc:Abigail", EntityType: "npc", DisplayName: "Abigail", DefinitionId: "npc:Abigail"},
	}

	_, ackErr := resolveAgentTarget(agent.ConnectionContext{GameID: "stardew-valley"}, event)
	if ackErr == nil {
		t.Fatal("resolveAgentTarget returned nil error, want target_entity_not_in_event")
	}
	if ackErr.Code != "target_entity_not_in_event" {
		t.Fatalf("error code = %q, want target_entity_not_in_event", ackErr.Code)
	}
}

func TestResolveAgentTargetComparesEntityIDCaseSensitively(t *testing.T) {
	event := targetResolutionEvent("npc:Abigail")
	event.Entities = []*protocolv1alpha2.EntityRef{
		{EntityId: "NPC:Abigail", EntityType: "npc", DisplayName: "Abigail", DefinitionId: "npc:Abigail"},
	}

	_, ackErr := resolveAgentTarget(agent.ConnectionContext{GameID: "stardew-valley"}, event)
	if ackErr == nil {
		t.Fatal("resolveAgentTarget returned nil error, want target_entity_not_in_event")
	}
	if ackErr.Code != "target_entity_not_in_event" {
		t.Fatalf("error code = %q, want target_entity_not_in_event", ackErr.Code)
	}
}

func TestResolveAgentTargetRejectsDuplicateTargetEntityConflicts(t *testing.T) {
	tests := []struct {
		name     string
		first    *protocolv1alpha2.EntityRef
		second   *protocolv1alpha2.EntityRef
		wantCode string
	}{
		{
			name:     "entity_type conflict",
			first:    &protocolv1alpha2.EntityRef{EntityId: "npc:Abigail", EntityType: "npc", DisplayName: "Abigail", DefinitionId: "npc:Abigail"},
			second:   &protocolv1alpha2.EntityRef{EntityId: "npc:Abigail", EntityType: "monster", DisplayName: "Abigail", DefinitionId: "npc:Abigail"},
			wantCode: "target_entity_conflict",
		},
		{
			name:     "display_name conflict",
			first:    &protocolv1alpha2.EntityRef{EntityId: "npc:Abigail", EntityType: "npc", DisplayName: "Abigail", DefinitionId: "npc:Abigail"},
			second:   &protocolv1alpha2.EntityRef{EntityId: "npc:Abigail", EntityType: "npc", DisplayName: "Abby", DefinitionId: "npc:Abigail"},
			wantCode: "target_entity_conflict",
		},
		{
			name:     "definition_id conflict",
			first:    &protocolv1alpha2.EntityRef{EntityId: "npc:Abigail", EntityType: "npc", DisplayName: "Abigail", DefinitionId: "npc:Abigail"},
			second:   &protocolv1alpha2.EntityRef{EntityId: "npc:Abigail", EntityType: "npc", DisplayName: "Abigail", DefinitionId: "archetype:town_villager"},
			wantCode: "target_entity_conflict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := targetResolutionEvent("npc:Abigail")
			event.Entities = []*protocolv1alpha2.EntityRef{tt.first, tt.second}

			_, ackErr := resolveAgentTarget(agent.ConnectionContext{GameID: "stardew-valley"}, event)
			if ackErr == nil {
				t.Fatal("resolveAgentTarget returned nil error, want conflict")
			}
			if ackErr.Code != tt.wantCode {
				t.Fatalf("error code = %q, want %s", ackErr.Code, tt.wantCode)
			}
		})
	}
}

func TestResolveAgentTargetAcceptsDefinitionIDDifferentFromEntityID(t *testing.T) {
	event := targetResolutionEvent("creature:alpha")
	event.Entities = []*protocolv1alpha2.EntityRef{
		{
			EntityId:     "creature:alpha",
			EntityType:   "creature",
			DisplayName:  "Alpha",
			DefinitionId: "villager/farmer",
		},
	}

	resolved, ackErr := resolveAgentTarget(agent.ConnectionContext{GameID: "fake-game"}, event)
	if ackErr != nil {
		t.Fatalf("resolveAgentTarget returned error: %+v", ackErr)
	}
	if resolved.Key.EntityID != "creature:alpha" {
		t.Fatalf("AgentSessionKey.EntityID = %q, want creature:alpha", resolved.Key.EntityID)
	}
	if resolved.Target.GetDefinitionId() != "villager/farmer" {
		t.Fatalf("Target.DefinitionId = %q, want villager/farmer", resolved.Target.GetDefinitionId())
	}
}

func targetResolutionEvent(targetEntityID string) *protocolv1alpha2.GameEvent {
	return &protocolv1alpha2.GameEvent{
		EventId:        "event-1",
		EventType:      "player_interacted_with_npc",
		WorldId:        "world-a",
		TargetEntityId: targetEntityID,
	}
}
