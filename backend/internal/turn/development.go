package turn

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func developmentRelevant(snapshot Snapshot, events []wiaworld.Event, development plot.Development) bool {
	if len(development.LocationIDs) == 0 && len(development.EntityIDs) == 0 {
		return true
	}
	for _, place := range snapshot.Positions {
		if materialAtLocation(snapshot, story.Material{LocationIDs: development.LocationIDs}, place) {
			return true
		}
	}
	for _, event := range events {
		for _, entity := range development.EntityIDs {
			if event.ActorID == entity || event.TargetID == entity || strings.Contains(event.Content, entity) {
				return true
			}
		}
	}
	return false
}

// Evaluation changes with relevant values and event meaning. Run IDs, event IDs
// and persistence counters do not turn identical facts into a new situation.
func developmentBasis(snapshot Snapshot, output Output, development plot.Development) string {
	entities, locations, itemIDs := slices.Clone(development.EntityIDs), slices.Clone(development.LocationIDs), []string{}
	for _, id := range development.MaterialIDs {
		if material, ok := story.MaterialByID(snapshot.Definition, id); ok {
			entities = append(entities, material.EntityIDs...)
			locations = append(locations, material.LocationIDs...)
			itemIDs = append(itemIDs, material.ItemIDs...)
		}
	}
	global := len(entities) == 0 && len(locations) == 0 && len(itemIDs) == 0
	actors := map[string]bool{}
	for _, id := range entities {
		actors[id] = true
	}
	positions := map[string]string{}
	for entity, place := range snapshot.Positions {
		if global || actors[entity] || materialAtLocation(snapshot, story.Material{LocationIDs: locations}, place) {
			positions[entity], actors[entity] = place, true
		}
	}
	states := map[string]map[string]wiaworld.StateValue{}
	for entity, values := range snapshot.States {
		if global || actors[entity] {
			states[entity] = map[string]wiaworld.StateValue{}
			for id, value := range values {
				states[entity][id] = value.Value
			}
		}
	}
	relations := map[string]int{}
	for _, relation := range snapshot.Relationships {
		if global || actors[relation.SubjectID] || actors[relation.TargetID] {
			relations[relation.SubjectID+"\x00"+relation.TargetID+"\x00"+relation.RelationType] = relation.Value
		}
	}
	type placement struct{ Holder, Location string }
	items := map[string]placement{}
	for id, item := range snapshot.Items {
		if global || slices.Contains(itemIDs, id) || actors[item.HolderID] || materialAtLocation(snapshot, story.Material{LocationIDs: locations}, item.LocationID) {
			items[id] = placement{item.HolderID, item.LocationID}
		}
	}
	type fact struct{ Type, Actor, Target, Content, Status string }
	var facts []fact
	for _, event := range output.Events {
		related := global || actors[event.ActorID] || actors[event.TargetID]
		for _, entity := range entities {
			related = related || strings.Contains(event.Content, entity)
		}
		for _, item := range itemIDs {
			related = related || strings.Contains(event.Content, item)
		}
		if related {
			facts = append(facts, fact{event.EventType, event.ActorID, event.TargetID, event.Content, event.SourceType})
		}
	}
	values := struct {
		Clock     string
		Epoch     int64
		Positions map[string]string
		States    map[string]map[string]wiaworld.StateValue
		Relations map[string]int
		Items     map[string]placement
		Events    []fact
	}{output.Clock, snapshot.Summary.ContextEpoch, positions, states, relations, items, facts}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(wire.MarshalJSON(values))))
}
