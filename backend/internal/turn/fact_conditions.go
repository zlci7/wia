package turn

import (
	"fmt"
	"strings"

	"gameagent/backend/internal/plot"
	wiaworld "gameagent/backend/internal/world"
)

func evaluateFactConditions(snapshot Snapshot, output Output, conditions []plot.FactCondition, actor string) (bool, []string) {
	evidence := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		met, id := evaluateFactCondition(snapshot, output, condition, actor)
		evidence = append(evidence, id)
		if !met {
			return false, evidence
		}
	}
	return true, evidence
}

func evaluateFactCondition(snapshot Snapshot, output Output, condition plot.FactCondition, actor string) (bool, string) {
	entity := condition.EntityID
	if entity == "actor" {
		entity = actor
	}
	id := fmt.Sprintf("fact:%s:%s:%s:%s:%d:%s:%s", condition.Kind, entity, condition.TargetID, condition.FactID, condition.Value, condition.LocationID, condition.Status)
	switch condition.Kind {
	case "location_is":
		return output.Positions[entity] == condition.LocationID, id
	case "state_at_least", "state_at_most":
		state, ok := output.States[entity][condition.FactID]
		if !ok || state.Value.Type != "integer" {
			return false, id
		}
		if condition.Kind == "state_at_least" {
			return state.Value.Integer >= condition.Value, id
		}
		return state.Value.Integer <= condition.Value, id
	case "relation_at_least", "relation_at_most":
		index := relationshipIndex(output.Relationships, entity, condition.TargetID, condition.FactID)
		if index < 0 {
			return false, id
		}
		if condition.Kind == "relation_at_least" {
			return output.Relationships[index].Value >= condition.Value, id
		}
		return output.Relationships[index].Value <= condition.Value, id
	case "item_held":
		item, ok := output.Items[condition.FactID]
		return ok && item.HolderID == entity, id
	case "item_at":
		item, ok := output.Items[condition.FactID]
		return ok && item.LocationID == condition.LocationID, id
	case "rule_result", "rule_result_absent":
		found := false
		for _, event := range append(append([]wiaworld.Event{}, snapshot.Events...), output.Events...) {
			prefix := "rule:" + condition.FactID + ":"
			if strings.HasPrefix(event.SourceType, prefix) && (condition.Status == "" || strings.TrimPrefix(event.SourceType, prefix) == condition.Status) {
				found = true
				break
			}
		}
		if !found {
			for status := range snapshot.RuleResults[condition.FactID] {
				if condition.Status == "" || status == condition.Status {
					found = true
					break
				}
			}
		}
		if condition.Kind == "rule_result_absent" {
			return !found, id
		}
		return found, id
	}
	return false, id
}
