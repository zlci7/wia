package plot

import (
	"errors"
	"fmt"
)

// The two validations are separate because they answer different questions, and a
// caller usually has only one of them to ask.
//
//	ValidateDefinition — does this plot graph describe a legal story line?
//	ValidateProgress   — is this world's progress consistent with that story line?
//
// The content tooling asks only the first, so publishing a pack no longer has to
// invent an empty Progress to satisfy the check. The runtime asks both.
//
// The errors are this package's own. What a bad definition means depends on the
// entry point: to a publisher it is invalid content, to a running world it is an
// unreadable save, to the HTTP layer it is a status code. Those mappings belong to
// the callers, not here.
var (
	ErrInvalidDefinition = errors.New("invalid plot definition")
	ErrInvalidProgress   = errors.New("invalid plot progress")
)

// ValidateDefinition checks the story line itself: that it has a revision and a
// bounded number of nodes, that node identifiers are unique, that every dependency
// names an earlier node, and that each node carries what a stage needs to run it.
func ValidateDefinition(def Definition) error {
	if def.Revision == "" {
		return fmt.Errorf("%w: revision is required", ErrInvalidDefinition)
	}
	if len(def.Nodes) == 0 || len(def.Nodes) > 32 {
		return fmt.Errorf("%w: node count %d is outside 1..32", ErrInvalidDefinition, len(def.Nodes))
	}
	// Dependencies must point backwards in the slice: that is what makes the graph
	// acyclic without a separate cycle check, so the order of Nodes is a contract.
	known := map[string]bool{}
	for _, node := range def.Nodes {
		if node.ID == "" {
			return fmt.Errorf("%w: a node has no id", ErrInvalidDefinition)
		}
		if known[node.ID] {
			return fmt.Errorf("%w: duplicate node id %q", ErrInvalidDefinition, node.ID)
		}
		if node.AtMinute < 0 {
			return fmt.Errorf("%w: node %q has a negative at_minute", ErrInvalidDefinition, node.ID)
		}
		if node.Condition == "" {
			return fmt.Errorf("%w: node %q has no condition", ErrInvalidDefinition, node.ID)
		}
		if node.Development == "" {
			return fmt.Errorf("%w: node %q has no development", ErrInvalidDefinition, node.ID)
		}
		if node.Audience == nil {
			return fmt.Errorf("%w: node %q has no audience list", ErrInvalidDefinition, node.ID)
		}
		if node.OnUnmet != "" && node.OnUnmet != "defer" && node.OnUnmet != "skip" {
			return fmt.Errorf("%w: node %q has invalid on_unmet", ErrInvalidDefinition, node.ID)
		}
		for _, condition := range node.Requirements {
			if err := ValidateFactCondition(condition, false); err != nil {
				return fmt.Errorf("%w: node %q: %v", ErrInvalidDefinition, node.ID, err)
			}
		}
		for _, dep := range node.After {
			if !known[dep] {
				return fmt.Errorf("%w: node %q depends on %q, which is not an earlier node", ErrInvalidDefinition, node.ID, dep)
			}
		}
		known[node.ID] = true
	}
	return nil
}

// ValidateFactCondition validates the closed v1 fact vocabulary without knowing a
// particular story's entity catalog. Content performs those reference checks.
func ValidateFactCondition(condition FactCondition, allowActor bool) error {
	if condition.EntityID == "actor" && !allowActor {
		return fmt.Errorf("actor placeholder is not allowed here")
	}
	switch condition.Kind {
	case "location_is":
		if condition.EntityID == "" || condition.LocationID == "" {
			return fmt.Errorf("location_is requires entity_id and location_id")
		}
	case "state_at_least", "state_at_most":
		if condition.EntityID == "" || condition.FactID == "" {
			return fmt.Errorf("state condition requires entity_id and fact_id")
		}
	case "relation_at_least", "relation_at_most":
		if condition.EntityID == "" || condition.TargetID == "" || condition.FactID == "" {
			return fmt.Errorf("relation condition requires entity_id, target_id and fact_id")
		}
	case "item_held", "item_at":
		if condition.FactID == "" {
			return fmt.Errorf("item condition requires fact_id")
		}
		if condition.Kind == "item_held" && condition.EntityID == "" {
			return fmt.Errorf("item_held requires entity_id")
		}
		if condition.Kind == "item_at" && condition.LocationID == "" {
			return fmt.Errorf("item_at requires location_id")
		}
	case "rule_result", "rule_result_absent":
		if condition.FactID == "" {
			return fmt.Errorf("rule condition requires fact_id")
		}
		if condition.Status != "" && condition.Status != "succeeded" && condition.Status != "failed" {
			return fmt.Errorf("rule condition status is invalid")
		}
	default:
		return fmt.Errorf("unknown fact condition kind %q", condition.Kind)
	}
	return nil
}

// ValidateProgress checks a world's progress against the definition it belongs to:
// that it carries a version, that every recorded node exists, that each state is one
// of the three endings a node can have, and that a settled node names the event that
// settled it. A deferred node has no event yet by definition.
func ValidateProgress(def Definition, progress Progress) error {
	if progress.Version < 1 {
		return fmt.Errorf("%w: version %d is not positive", ErrInvalidProgress, progress.Version)
	}
	if progress.Nodes == nil {
		return fmt.Errorf("%w: nodes are missing", ErrInvalidProgress)
	}
	known := make(map[string]bool, len(def.Nodes))
	for _, node := range def.Nodes {
		known[node.ID] = true
	}
	for id, state := range progress.Nodes {
		if !known[id] {
			return fmt.Errorf("%w: state for unknown node %q", ErrInvalidProgress, id)
		}
		if state.Status != "occurred" && state.Status != "deferred" && state.Status != "skipped" {
			return fmt.Errorf("%w: node %q has status %q", ErrInvalidProgress, id, state.Status)
		}
		if state.Status != "deferred" && state.EventID == "" {
			return fmt.Errorf("%w: settled node %q names no event", ErrInvalidProgress, id)
		}
	}
	return nil
}

// Validate checks a definition and a progress together, which is what a running
// world needs when it reads both from a save.
func Validate(def Definition, progress Progress) error {
	if err := ValidateDefinition(def); err != nil {
		return err
	}
	return ValidateProgress(def, progress)
}
