package world

import (
	"fmt"
	"regexp"
	"strings"
)

var entityRefPattern = regexp.MustCompile(`^(npc|bystander):[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

// ValidEntityRef validates the common identity grammar. Existence and capability
// scope remain caller responsibilities because they depend on the frozen world.
func ValidEntityRef(id string) bool {
	id = strings.TrimSpace(id)
	return id == "player" || entityRefPattern.MatchString(id)
}

// ValidateRoute checks a route against the directed place graph. Regions organize
// places but are never valid positions or route steps.
func ValidateRoute(route []string, from, to string, places map[string][]string) error {
	if len(route) == 0 || route[0] != from || route[len(route)-1] != to {
		return fmt.Errorf("route endpoints do not match movement")
	}
	for i, id := range route {
		connections, ok := places[id]
		if !ok {
			return fmt.Errorf("route[%d] is not a place", i)
		}
		if i+1 == len(route) {
			continue
		}
		found := false
		for _, next := range connections {
			if next == route[i+1] {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("route step %s -> %s is not connected", id, route[i+1])
		}
	}
	return nil
}
