package turn

import (
	"fmt"
	"slices"
	"strings"

	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

// applyStateEffects applies model decisions to declared state. Causality belongs
// to coordination; types, authored limits and provenance belong to this module.
func applyStateEffects(snapshot Snapshot, output *Output, effects []stateEffect, sources map[string]mechanicSource) error {
	budgets := map[string]int64{}
	for _, change := range output.StateChanges {
		definition, _ := stateDefinition(snapshot.Definition, change.After.StateID)
		if definition.UpdatePolicy.Kind != "bounded_proposal" {
			continue
		}
		distance, safe := checkedDistance(change.Before.Value.Integer, change.After.Value.Integer)
		key := change.After.EntityID + "\x00" + change.After.StateID
		if !safe || distance > int64(definition.UpdatePolicy.MaxChangePerTurn)-budgets[key] {
			return coordinationInvalid("state_budget_exceeded", "state_effects", "within-whole-turn-budget")
		}
		budgets[key] += distance
	}
	for index, effect := range effects {
		field := fmt.Sprintf("state_effects[%d]", index)
		effect.EntityID, effect.StateID, effect.ActionID = strings.TrimSpace(effect.EntityID), strings.TrimSpace(effect.StateID), strings.TrimSpace(effect.ActionID)
		source, found := sources[effect.ActionID]
		definition, defined := stateDefinition(snapshot.Definition, effect.StateID)
		current, exists := output.States[effect.EntityID][effect.StateID]
		modelOwned := definition.UpdatePolicy.Kind == "model"
		validOutcome := source.status == "succeeded" || source.status == "partial" || (modelOwned && source.status == "failed")
		if !found || !defined || !exists || !validOutcome || (!modelOwned && definition.UpdatePolicy.Kind != "bounded_proposal") {
			return coordinationInvalid("state_effect_invalid", field, "declared-model-state-and-resolved-action")
		}
		value := current.Value
		if effect.Value != nil {
			if !modelOwned || effect.Delta != 0 {
				return coordinationInvalid("state_effect_invalid", field, "model-value-or-integer-delta")
			}
			value = *effect.Value
		} else {
			if definition.Type != "integer" || effect.Delta == 0 {
				return coordinationInvalid("state_effect_invalid", field, "typed-value-or-nonzero-integer-delta")
			}
			next, safe := checkedAdd(value.Integer, effect.Delta)
			if !safe {
				code := "state_range_exceeded"
				if !modelOwned {
					code = "state_budget_exceeded"
				}
				return coordinationInvalid(code, field+".delta", "representable-integer")
			}
			value.Integer = next
		}
		if !modelOwned {
			key := effect.EntityID + "\x00" + effect.StateID
			distance, safe := checkedDistance(current.Value.Integer, value.Integer)
			if !safe || distance > int64(definition.UpdatePolicy.MaxChangePerTurn)-budgets[key] {
				return coordinationInvalid("state_budget_exceeded", field+".delta", "within-whole-turn-budget")
			}
			budgets[key] += distance
		}
		if !validStateValue(definition, value) {
			return coordinationInvalid("state_range_exceeded", field, "declared-type-and-range")
		}
		if value == current.Value {
			continue
		}
		next := current
		next.Value, next.SourceEvent, next.UpdatedTurn, next.Version = value, source.resultID, snapshot.Summary.TurnSeq+1, current.Version+1
		order := nextEffectOrder(*output)
		output.States[effect.EntityID][effect.StateID] = next
		output.StateChanges = append(output.StateChanges, StateChange{Before: current, After: next, ActionID: effect.ActionID, SourceEventID: source.resultID, Order: order})
	}
	return nil
}

func validStateValue(definition story.StateDefinition, value wiaworld.StateValue) bool {
	if value.Type != definition.Type {
		return false
	}
	switch value.Type {
	case "integer":
		return !value.Boolean && value.Enum == "" && definition.Minimum != nil && definition.Maximum != nil && value.Integer >= *definition.Minimum && value.Integer <= *definition.Maximum
	case "boolean":
		return value.Integer == 0 && value.Enum == ""
	case "enum":
		return value.Integer == 0 && !value.Boolean && slices.Contains(definition.EnumValues, value.Enum)
	}
	return false
}

func applyRuleStateEffects(snapshot Snapshot, output *Output, actor, ruleID, actionID string, event wiaworld.Event, effects []story.RuleEffect) error {
	states := cloneStates(output.States)
	changes := slices.Clone(output.StateChanges)
	order := len(changes) + len(output.RelationshipChanges) + len(output.ItemTransfers)
	for index, effect := range effects {
		entity := effect.EntityID
		if entity == "actor" {
			entity = actor
		}
		definition, ok := stateDefinition(snapshot.Definition, effect.StateID)
		current, exists := states[entity][effect.StateID]
		if effect.Kind != "state_delta" || !ok || !exists || definition.Type != "integer" || definition.UpdatePolicy.Kind != "rule_only" {
			return coordinationInvalid("rule_effect_invalid", fmt.Sprintf("action_rules.%s.effects[%d]", ruleID, index), "rule-owned-integer-state")
		}
		nextValue, safe := checkedAdd(current.Value.Integer, effect.Delta)
		if definition.Currency != nil && (!safe || nextValue < *definition.Minimum || nextValue > *definition.Maximum) {
			return coordinationInvalid("currency_range_exceeded", fmt.Sprintf("action_rules.%s.effects[%d]", ruleID, index), "exact-nonnegative-currency-settlement")
		}
		if !safe {
			if effect.Delta > 0 {
				nextValue = *definition.Maximum
			} else {
				nextValue = *definition.Minimum
			}
		}
		if nextValue < *definition.Minimum {
			nextValue = *definition.Minimum
		} else if nextValue > *definition.Maximum {
			nextValue = *definition.Maximum
		}
		next := current
		next.Value.Integer = nextValue
		next.SourceEvent = event.EventID
		next.UpdatedTurn = snapshot.Summary.TurnSeq + 1
		next.Version++
		order++
		states[entity][effect.StateID] = next
		changes = append(changes, StateChange{Before: current, After: next, ActionID: actionID, SourceEventID: event.EventID, Order: order})
	}
	output.States, output.StateChanges = states, changes
	return nil
}

type stateEffect struct {
	EntityID string               `json:"entity_id"`
	StateID  string               `json:"state_id"`
	Delta    int                  `json:"delta,omitempty"`
	Value    *wiaworld.StateValue `json:"value,omitempty"`
	ActionID string               `json:"action_id"`
}

func stateCoordinationContract() string {
	return "\nstate_effects 每项包含 entity_id、state_id、action_id，以及 delta 或 value，二者择一。model 策略由你判断因果和变化量；整数可用 delta，或 value={type:integer,integer:数值}；布尔用 value={type:boolean,boolean:布尔值}，枚举用 value={type:enum,enum:已定义取值}。来源为当前 succeeded/partial/failed 结果，失败也可能产生疲劳等后果；not_executed 不产生变化。bounded_proposal 只用成功或部分成功结果的整数 delta，并遵守整轮累计预算。数值在声明范围内，rule_only 和 readonly 不由你更新。"
}
