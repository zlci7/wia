package content

import (
	"fmt"
	"strings"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
)

func compileActionRules(pack StoryPack, def story.Definition, characters map[string]bool) ([]story.ActionRule, error) {
	def.ActionRules = pack.ActionRules
	seen := map[string]bool{}
	result := make([]story.ActionRule, 0, len(pack.ActionRules))
	for _, rule := range pack.ActionRules {
		rule.ID, rule.Name, rule.Guidance = strings.TrimSpace(rule.ID), strings.TrimSpace(rule.Name), strings.TrimSpace(rule.Guidance)
		if !packID.MatchString(rule.ID) || seen[rule.ID] || rule.Name == "" || rule.Guidance == "" || strings.TrimSpace(rule.SuccessText) == "" {
			return nil, fmt.Errorf("invalid unique identity or text")
		}
		seen[rule.ID] = true
		for _, condition := range rule.Conditions {
			if err := validateFactReferences(condition, def, characters, true); err != nil {
				return nil, err
			}
		}
		if rule.Risk != nil {
			if rule.Risk.Minimum < 1 || rule.Risk.Maximum > 100 || rule.Risk.Minimum > rule.Risk.Maximum || rule.Risk.BaseTarget < rule.Risk.Minimum || rule.Risk.BaseTarget > rule.Risk.Maximum || strings.TrimSpace(rule.FailureText) == "" {
				return nil, fmt.Errorf("rule %s has invalid percentile range", rule.ID)
			}
			for _, modifier := range rule.Risk.Modifiers {
				if strings.TrimSpace(modifier.Label) == "" || modifier.Amount == 0 || modifier.Amount < -100 || modifier.Amount > 100 {
					return nil, fmt.Errorf("rule %s has invalid modifier", rule.ID)
				}
				if err := validateFactReferences(modifier.Condition, def, characters, true); err != nil {
					return nil, err
				}
			}
		}
		for _, effect := range append(append([]story.RuleEffect{}, rule.SuccessEffects...), rule.FailureEffects...) {
			if effect.Kind != "state_delta" || effect.Delta == 0 || (effect.EntityID != "actor" && effect.EntityID != "player") {
				return nil, fmt.Errorf("rule %s has unsupported effect", rule.ID)
			}
			state, ok := stateByID(def.StateDefinitions, effect.StateID)
			if !ok || state.Type != "integer" || state.UpdatePolicy.Kind != "rule_only" || !stateAllowsEntity(state, effect.EntityID, characters) {
				return nil, fmt.Errorf("rule %s effect must target a rule_only integer state", rule.ID)
			}
		}
		result = append(result, rule)
	}
	for _, rule := range result {
		conditions := append([]plot.FactCondition{}, rule.Conditions...)
		if rule.Risk != nil {
			for _, modifier := range rule.Risk.Modifiers {
				conditions = append(conditions, modifier.Condition)
			}
		}
		for _, condition := range conditions {
			if (condition.Kind == "rule_result" || condition.Kind == "rule_result_absent") && !seen[condition.FactID] {
				return nil, fmt.Errorf("rule %s references unknown rule %s", rule.ID, condition.FactID)
			}
		}
	}
	return result, nil
}

func stateByID(definitions []story.StateDefinition, id string) (story.StateDefinition, bool) {
	for _, definition := range definitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return story.StateDefinition{}, false
}

func stateAllowsEntity(definition story.StateDefinition, entity string, characters map[string]bool) bool {
	if entity == "actor" {
		entity = "player"
	}
	switch definition.Scope {
	case "all":
		return entity == "player" || characters[entity]
	case "player":
		return entity == "player"
	case "npc":
		return characters[entity]
	default:
		return false
	}
}

func validateFactReferences(condition plot.FactCondition, def story.Definition, characters map[string]bool, allowActor bool) error {
	if err := plot.ValidateFactCondition(condition, allowActor); err != nil {
		return err
	}
	entityOK := func(id string) bool { return id == "player" || (allowActor && id == "actor") || characters[id] }
	if condition.EntityID != "" && !entityOK(condition.EntityID) {
		return fmt.Errorf("unknown condition entity %s", condition.EntityID)
	}
	if condition.TargetID != "" && !entityOK(condition.TargetID) {
		return fmt.Errorf("unknown condition target %s", condition.TargetID)
	}
	switch condition.Kind {
	case "location_is":
		if _, ok := story.LocationByID(def, condition.LocationID); !ok {
			return fmt.Errorf("unknown condition location %s", condition.LocationID)
		}
	case "item_at":
		if _, ok := story.LocationByID(def, condition.LocationID); !ok {
			return fmt.Errorf("unknown condition location %s", condition.LocationID)
		}
		found := false
		for _, item := range def.InitialItems {
			found = found || item.InstanceID == condition.FactID
		}
		if !found {
			return fmt.Errorf("unknown condition item %s", condition.FactID)
		}
	case "state_at_least", "state_at_most":
		state, ok := stateByID(def.StateDefinitions, condition.FactID)
		if !ok {
			return fmt.Errorf("unknown condition state %s", condition.FactID)
		}
		if state.Type != "integer" {
			return fmt.Errorf("condition state %s must be integer", condition.FactID)
		}
		if !stateAllowsEntity(state, condition.EntityID, characters) {
			return fmt.Errorf("condition state %s is not defined for %s", condition.FactID, condition.EntityID)
		}
	case "relation_at_least", "relation_at_most":
		found := false
		for _, item := range def.RelationDefinitions {
			found = found || item.ID == condition.FactID
		}
		if !found {
			return fmt.Errorf("unknown condition relation %s", condition.FactID)
		}
	case "item_held":
		found := false
		for _, item := range def.InitialItems {
			found = found || item.InstanceID == condition.FactID
		}
		if !found {
			return fmt.Errorf("unknown condition item %s", condition.FactID)
		}
	case "rule_result", "rule_result_absent":
		found := false
		for _, item := range def.ActionRules {
			found = found || item.ID == condition.FactID
		}
		if !found {
			return fmt.Errorf("unknown condition rule %s", condition.FactID)
		}
	}
	return nil
}
