package world

import "testing"

func TestValidateRouteUsesDirectedPlaceConnections(t *testing.T) {
	graph := map[string][]string{"office": {"street"}, "street": {"clinic"}, "clinic": {}}
	if err := ValidateRoute([]string{"office", "street", "clinic"}, "office", "clinic", graph); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRoute([]string{"clinic", "street"}, "clinic", "street", graph); err == nil {
		t.Fatal("reverse traversal was accepted without a directed edge")
	}
}

func TestValidEntityRefDoesNotTreatSystemSourcesAsEntities(t *testing.T) {
	for _, id := range []string{"player", "npc:reporter", "bystander:clerk"} {
		if !ValidEntityRef(id) {
			t.Fatalf("valid entity rejected: %s", id)
		}
	}
	for _, id := range []string{"world", "system", "npc:missing space", "npc:"} {
		if ValidEntityRef(id) {
			t.Fatalf("invalid entity accepted: %s", id)
		}
	}
}
