package session

import (
	"strings"
	"testing"
)

func TestResolveReturnsStableComparableKey(t *testing.T) {
	key1, err := Resolve("stardew-valley", "Farm_123", "npc:Linus")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	key2, err := Resolve("stardew-valley", "Farm_123", "npc:Linus")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	if key1 != key2 {
		t.Fatalf("expected same input to resolve to same key, got %+v and %+v", key1, key2)
	}
}

func TestResolveDistinguishesIdentityScopes(t *testing.T) {
	base := mustResolve(t, "stardew-valley", "Farm_123", "npc:Linus")

	tests := []struct {
		name     string
		gameID   string
		worldID  string
		entityID string
	}{
		{
			name:     "different game",
			gameID:   "another-game",
			worldID:  "Farm_123",
			entityID: "npc:Linus",
		},
		{
			name:     "different world",
			gameID:   "stardew-valley",
			worldID:  "Farm_456",
			entityID: "npc:Linus",
		},
		{
			name:     "different entity",
			gameID:   "stardew-valley",
			worldID:  "Farm_123",
			entityID: "npc:Abigail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustResolve(t, tt.gameID, tt.worldID, tt.entityID)
			if got == base {
				t.Fatalf("expected distinct key from base %+v, got %+v", base, got)
			}
		})
	}
}

func TestResolveRejectsMissingIdentityParts(t *testing.T) {
	tests := []struct {
		name     string
		gameID   string
		worldID  string
		entityID string
		wantPart string
	}{
		{
			name:     "missing game",
			worldID:  "Farm_123",
			entityID: "npc:Linus",
			wantPart: "game_id",
		},
		{
			name:     "missing world",
			gameID:   "stardew-valley",
			entityID: "npc:Linus",
			wantPart: "world_id",
		},
		{
			name:     "missing entity",
			gameID:   "stardew-valley",
			worldID:  "Farm_123",
			wantPart: "entity_id",
		},
		{
			name:     "blank entity",
			gameID:   "stardew-valley",
			worldID:  "Farm_123",
			entityID: " \t\n",
			wantPart: "entity_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.gameID, tt.worldID, tt.entityID)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantPart) {
				t.Fatalf("expected error to mention %q, got %q", tt.wantPart, err.Error())
			}
		})
	}
}

func TestResolveTreatsEntityIDAsOpaque(t *testing.T) {
	key := mustResolve(t, "stardew-valley", "Farm_123", "companion:foo:bar")

	if key.EntityID != "companion:foo:bar" {
		t.Fatalf("expected entity_id to be preserved as opaque string, got %q", key.EntityID)
	}
}

func TestAgentSessionKeyDiagnosticIDIsStableAndHumanReadable(t *testing.T) {
	key := mustResolve(t, "stardew-valley", "Farm_123", "npc:Linus")

	got := key.DiagnosticID()
	if got == "" {
		t.Fatal("expected diagnostic id")
	}
	for _, want := range []string{"stardew-valley", "Farm_123", "npc:Linus"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected diagnostic id %q to contain %q", got, want)
		}
	}

	if got != key.DiagnosticID() {
		t.Fatalf("expected diagnostic id to be stable, got %q then %q", got, key.DiagnosticID())
	}
}

func mustResolve(t *testing.T, gameID, worldID, entityID string) AgentSessionKey {
	t.Helper()

	key, err := Resolve(gameID, worldID, entityID)
	if err != nil {
		t.Fatalf("Resolve(%q, %q, %q) returned error: %v", gameID, worldID, entityID, err)
	}
	return key
}
