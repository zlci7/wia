package turn

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func relationshipIndex(values []wiaworld.Relationship, subject, target, relationType string) int {
	for index, value := range values {
		if value.SubjectID == subject && value.TargetID == target && value.RelationType == relationType {
			return index
		}
	}
	return -1
}

type relationshipProposalOption struct {
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
}

func relationshipProposalOptions(snapshot Snapshot, subject string) []relationshipProposalOption {
	result := []relationshipProposalOption{}
	for _, sourceID := range relationshipExperienceSources(snapshot, subject) {
		_, ok := snapshot.Sources[sourceID]
		if !ok {
			continue
		}
		for _, relation := range snapshot.Relationships {
			key := sourceID + "\x00" + subject + "\x00" + relation.TargetID + "\x00" + relation.RelationType
			if relation.SubjectID == subject && relation.TargetID != subject && !snapshot.AppliedRelationshipSources[key] {
				result = append(result, relationshipProposalOption{SourceID: sourceID, TargetID: relation.TargetID, RelationType: relation.RelationType})
			}
		}
	}
	slices.SortFunc(result, func(left, right relationshipProposalOption) int {
		if left.SourceID != right.SourceID {
			return strings.Compare(left.SourceID, right.SourceID)
		}
		if left.TargetID != right.TargetID {
			return strings.Compare(left.TargetID, right.TargetID)
		}
		return strings.Compare(left.RelationType, right.RelationType)
	})
	return result
}

// Sources with the same permitted relations share one group. Each group denotes
// exactly its source_ids × relations, including exclusions already applied above.
func relationshipProposalContext(options []relationshipProposalOption) string {
	type relationPair struct {
		TargetID     string `json:"target_id"`
		RelationType string `json:"relation_type"`
	}
	type sourceGroup struct {
		SourceIDs []string       `json:"source_ids"`
		Relations []relationPair `json:"relations"`
	}
	groups := []sourceGroup{}
	indexes := map[string]int{}
	for start := 0; start < len(options); {
		end := start + 1
		for end < len(options) && options[end].SourceID == options[start].SourceID {
			end++
		}
		pairs := make([]relationPair, 0, end-start)
		for _, option := range options[start:end] {
			pairs = append(pairs, relationPair{TargetID: option.TargetID, RelationType: option.RelationType})
		}
		key := wire.MarshalJSON(pairs)
		index, found := indexes[key]
		if !found {
			index = len(groups)
			indexes[key] = index
			groups = append(groups, sourceGroup{SourceIDs: []string{}, Relations: pairs})
		}
		groups[index].SourceIDs = append(groups[index].SourceIDs, options[start].SourceID)
		start = end
	}
	return wire.MarshalJSON(groups)
}

func validateRelationshipProposals(snapshot Snapshot, subject string, decision *NPCDecision) error {
	if snapshot.Definition.Capabilities["relations"] != 1 {
		if len(decision.RelationshipProposals) > 0 {
			return &GenerationError{Code: "json_field_value", Field: "relationship_proposals", Expected: "empty-when-relations-disabled", Cause: ErrGenerationFailed}
		}
		return nil
	}
	seen := map[string]bool{}
	options := map[string]bool{}
	for _, option := range relationshipProposalOptions(snapshot, subject) {
		options[option.SourceID+"\x00"+option.TargetID+"\x00"+option.RelationType] = true
	}
	for index := range decision.RelationshipProposals {
		proposal := &decision.RelationshipProposals[index]
		proposal.TargetID = strings.TrimSpace(proposal.TargetID)
		proposal.RelationType = strings.TrimSpace(proposal.RelationType)
		proposal.SourceID = strings.TrimSpace(proposal.SourceID)
		definition, defined := relationDefinition(snapshot.Definition, proposal.RelationType)
		position := relationshipIndex(snapshot.Relationships, subject, proposal.TargetID, proposal.RelationType)
		field := fmt.Sprintf("relationship_proposals[%d]", index)
		if !defined || position < 0 || proposal.TargetID == subject || !options[proposal.SourceID+"\x00"+proposal.TargetID+"\x00"+proposal.RelationType] || proposal.Delta == 0 || (definition.MaxChangePerTurn > 0 && (proposal.Delta > definition.MaxChangePerTurn || proposal.Delta < -definition.MaxChangePerTurn)) {
			return &GenerationError{Code: "json_field_value", Field: field, Expected: "owned-existing-relationship-within-turn-budget", Cause: ErrGenerationFailed}
		}
		key := proposal.TargetID + "\x00" + proposal.RelationType
		if seen[key] {
			return &GenerationError{Code: "json_field_value", Field: field, Expected: "one-proposal-per-target-and-type", Cause: ErrGenerationFailed}
		}
		seen[key] = true
		next, safe := checkedAdd(snapshot.Relationships[position].Value, proposal.Delta)
		if !safe || next < definition.Minimum || next > definition.Maximum {
			return &GenerationError{Code: "json_field_value", Field: field + ".delta", Expected: "result-within-declared-range", Cause: ErrGenerationFailed}
		}
	}
	return nil
}

// The legacy stage grants only exact proposals based on committed personal
// experience. The effect engine consumes these grants, not NPCDecision shapes.
func committedRelationshipAuthority(snapshot Snapshot, decisions map[string]NPCDecision) map[string]bool {
	allowed := map[string]bool{}
	for subject, decision := range decisions {
		for _, proposal := range decision.RelationshipProposals {
			if slices.Contains(relationshipExperienceSources(snapshot, subject), proposal.SourceID) {
				effect := relationshipEffect{SubjectID: subject, TargetID: proposal.TargetID, RelationType: proposal.RelationType, Delta: proposal.Delta, ProposalSourceID: proposal.SourceID}
				allowed[relationshipAuthorityKey(effect)] = true
			}
		}
	}
	return allowed
}

func relationshipAuthorityKey(effect relationshipEffect) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", effect.SubjectID, effect.TargetID, effect.RelationType, effect.Delta, effect.ProposalSourceID)
}

func relationshipExperienceSources(snapshot Snapshot, recipient string) []string {
	seen := map[string]bool{}
	result := []string{}
	for index := len(snapshot.Perceptions[recipient]) - 1; index >= 0; index-- {
		perception := snapshot.Perceptions[recipient][index]
		id := perception.SourceEventID
		accepted := perception.SourceType == "action_succeeded" || perception.SourceType == "action_partial" || perception.SourceType == "action_failed" || perception.SourceType == "plot_observed"
		if !accepted || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func applyRelationshipEffects(snapshot Snapshot, output *Output, effects []relationshipEffect, authority map[string]bool) error {
	relations, relationChanges := output.Relationships, output.RelationshipChanges
	relationEvents := []wiaworld.Event{}
	relationBudget := map[string]int64{}
	relationSourceSeen := map[string]bool{}
	appliedRelationshipSources := map[string]bool{}
	for key, applied := range snapshot.AppliedRelationshipSources {
		appliedRelationshipSources[key] = applied
	}
	order := nextEffectOrder(*output) - 1
	for _, change := range relationChanges {
		key := change.After.SubjectID + "\x00" + change.After.TargetID + "\x00" + change.After.RelationType
		delta, safe := checkedDistance(change.After.Value, change.Before.Value)
		if !safe || relationBudget[key] > int64(^uint(0)>>1)-delta {
			return coordinationInvalid("relationship_budget_exceeded", "relationship_effects", "valid-existing-whole-turn-budget")
		}
		relationBudget[key] += delta
		relationSourceSeen[change.ProposalSourceID+"\x00"+key] = true
		appliedRelationshipSources[change.ProposalSourceID+"\x00"+key] = true
		if change.Order > order {
			order = change.Order
		}
	}
	for index, effect := range effects {
		field := fmt.Sprintf("relationship_effects[%d]", index)
		effect.SubjectID, effect.TargetID, effect.RelationType, effect.ProposalSourceID = strings.TrimSpace(effect.SubjectID), strings.TrimSpace(effect.TargetID), strings.TrimSpace(effect.RelationType), strings.TrimSpace(effect.ProposalSourceID)
		definition, defined := relationDefinition(snapshot.Definition, effect.RelationType)
		position := relationshipIndex(relations, effect.SubjectID, effect.TargetID, effect.RelationType)
		appliedKey := effect.ProposalSourceID + "\x00" + effect.SubjectID + "\x00" + effect.TargetID + "\x00" + effect.RelationType
		_, sourceExists := snapshot.Sources[effect.ProposalSourceID]
		if !sourceExists || !defined || position < 0 || effect.SubjectID == effect.TargetID || effect.Delta == 0 || appliedRelationshipSources[appliedKey] || !authority[relationshipAuthorityKey(effect)] {
			return coordinationInvalid("relationship_effect_invalid", field, "subject-proposed-change-from-committed-personal-experience")
		}
		key := effect.SubjectID + "\x00" + effect.TargetID + "\x00" + effect.RelationType
		sourceKey := effect.ProposalSourceID + "\x00" + key
		if relationSourceSeen[sourceKey] {
			return coordinationInvalid("relationship_source_duplicate", field+".action_id", "one-effect-per-source-and-relationship")
		}
		relationSourceSeen[sourceKey] = true
		delta, safe := checkedAbs(effect.Delta)
		if !safe || (definition.MaxChangePerTurn > 0 && relationBudget[key] > int64(definition.MaxChangePerTurn)-delta) {
			return coordinationInvalid("relationship_budget_exceeded", field+".delta", "within-whole-turn-budget")
		}
		relationBudget[key] += delta
		if definition.MaxChangePerTurn > 0 && relationBudget[key] > int64(definition.MaxChangePerTurn) {
			return coordinationInvalid("relationship_budget_exceeded", field+".delta", "within-whole-turn-budget")
		}
		current := relations[position]
		next := current
		nextValue, safe := checkedAdd(next.Value, effect.Delta)
		if !safe {
			return coordinationInvalid("relationship_range_exceeded", field+".delta", "result-within-declared-range")
		}
		next.Value = nextValue
		if next.Value < definition.Minimum || next.Value > definition.Maximum {
			return coordinationInvalid("relationship_range_exceeded", field+".delta", "result-within-declared-range")
		}
		runID, stage, sceneVersion := "turn", 3, output.SceneVersion
		for _, event := range output.Events {
			if event.RunID != "" {
				runID = event.RunID
			}
			if event.Stage > stage {
				stage = event.Stage
			}
			if event.SceneVersion > sceneVersion {
				sceneVersion = event.SceneVersion
			}
		}
		relationEventID := fmt.Sprintf("%s:relationship-effect:%d", runID, len(relationChanges)+1)
		relationEvents = append(relationEvents, wiaworld.Event{EventID: relationEventID, EventType: "relationship_change", ActorID: effect.SubjectID, TargetID: effect.TargetID, Content: "关系主体依据个人经历更新了有向关系。", RunID: runID, Stage: stage, SceneVersion: sceneVersion, SourceType: "relationship_effect", ProjectionParentID: effect.ProposalSourceID, CreatedAt: time.Now().UTC()})
		next.SourceEvent, next.UpdatedTurn, next.Version = relationEventID, snapshot.Summary.TurnSeq+1, current.Version+1
		order++
		relations[position] = next
		relationChanges = append(relationChanges, RelationshipChange{Before: current, After: next, ActionID: effect.ProposalSourceID, SourceEventID: relationEventID, ProposalSourceID: effect.ProposalSourceID, Order: order})
	}
	output.Relationships, output.RelationshipChanges = relations, relationChanges
	output.Events = append(output.Events, relationEvents...)
	return nil
}

type relationshipEffect struct {
	SubjectID        string `json:"subject_id"`
	TargetID         string `json:"target_id"`
	RelationType     string `json:"relation_type"`
	Delta            int    `json:"delta"`
	ProposalSourceID string `json:"proposal_source_id"`
}

func relationshipCoordinationContract() string {
	return "\nrelationship_effects 每项含 subject_id、target_id、relation_type、delta、proposal_source_id；逐项采用对应主体本人 relationship_proposals 的值，source_id 复制到 proposal_source_id。协调器不代替人物判断关系；max_change_per_turn 为正时遵守整轮预算，为0时没有单轮预算。"
}
