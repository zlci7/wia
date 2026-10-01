package turn

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	wiaworld "gameagent/backend/internal/world"
)

// InputFragment copies a contiguous part of the player's words. Rune offsets
// are derived by the engine so the model never has to count Unicode characters.
type InputFragment struct {
	Text         string `json:"text"`
	ActorID      string `json:"actor_id"`
	IntentType   string `json:"intent_type"`
	AddresseeID  string `json:"addressee_id"`
	Visibility   string `json:"visibility"`
	WaitMinutes  int    `json:"wait_minutes,omitempty"`
	ActionRuleID string `json:"action_rule_id,omitempty"`
	Start        int    `json:"rune_start,omitempty"`
	End          int    `json:"rune_end,omitempty"`
}

func inputPrefix(run wiaworld.Run) string {
	if run.InputPart == 0 {
		return run.RunID
	}
	return fmt.Sprintf("%s:part:%d", run.RunID, run.InputPart)
}

func inputStage(run wiaworld.Run, phase int) int {
	if run.InputPart == 0 {
		return phase
	}
	return phase + 3*(run.InputPart-1)
}

func validateInputFragments(snapshot Snapshot, run wiaworld.Run, intent *TurnIntent) error {
	if len(intent.Fragments) == 0 {
		return nil
	}
	if len(intent.Fragments) < 2 || len(intent.Fragments) > 4 {
		return coordinationInvalid("input_fragments_invalid", "fragments", "two-to-four-contiguous-original-text-segments")
	}
	raw, cursor, risks, wait := []rune(run.Input), 0, 0, 0
	for i := range intent.Fragments {
		part := &intent.Fragments[i]
		text := []rune(part.Text)
		if part.ActorID != "player" || len(text) == 0 || strings.TrimSpace(part.Text) == "" || !slices.Contains([]string{"speak", "observe", "act"}, part.IntentType) || !slices.Contains([]string{"public", "private"}, part.Visibility) {
			return coordinationInvalid("input_fragment_invalid", "fragments", "original-text-with-player-actor-and-valid-intent-scope")
		}
		// Whitespace between fragments is retained in the original input record.
		for cursor < len(raw) && unicode.IsSpace(raw[cursor]) && raw[cursor] != text[0] {
			cursor++
		}
		if cursor+len(text) > len(raw) || string(raw[cursor:cursor+len(text)]) != part.Text {
			return coordinationInvalid("input_fragment_span_invalid", "fragments.text", "ordered-contiguous-exact-copy-covering-all-non-whitespace-input")
		}
		part.Start, part.End = cursor, cursor+len(text)
		cursor = part.End
		if part.AddresseeID != "" {
			if _, ok := characterByID(snapshot.Characters, part.AddresseeID); !ok {
				return coordinationInvalid("input_fragment_recipient_invalid", "fragments.addressee_id", "defined-important-character")
			}
		}
		if part.Visibility == "private" && part.AddresseeID == "" || part.WaitMinutes < 0 || part.WaitMinutes > 120 {
			return coordinationInvalid("input_fragment_scope_invalid", "fragments", "private-addressee-and-bounded-wait")
		}
		wait += part.WaitMinutes
		if part.ActionRuleID != "" {
			risks++
			if _, ok := actionRuleByID(snapshot.Definition, part.ActionRuleID); !ok || part.IntentType == "speak" {
				return coordinationInvalid("input_fragment_rule_invalid", "fragments.action_rule_id", "listed-action-rule-for-action")
			}
		}
	}
	for cursor < len(raw) && unicode.IsSpace(raw[cursor]) {
		cursor++
	}
	if cursor != len(raw) || risks > 1 || wait > 120 {
		return coordinationInvalid("input_fragments_invalid", "fragments", "complete-input-with-one-fixed-rule-and-shared-120-minute-budget")
	}
	if run.PreparedActionRuleID != "" && risks != 1 {
		return coordinationInvalid("input_fragment_prepared_rule_missing", "fragments.action_rule_id", "same-prepared-rule-on-retry")
	}
	for _, part := range intent.Fragments {
		if run.PreparedActionRuleID != "" && part.ActionRuleID != "" && part.ActionRuleID != run.PreparedActionRuleID {
			return coordinationInvalid("input_fragment_prepared_rule_changed", "fragments.action_rule_id", "same-prepared-rule-on-retry")
		}
	}
	return nil
}

func mergeInputOutput(output *Output, local Output) {
	events, perceptions, memories, visible := output.Events, output.Perceptions, output.Memories, output.VisibleEvents
	positions, resolution := output.PositionChanges, output.ActionResolution
	*output = local
	output.Events = append(events, local.Events...)
	output.Perceptions = append(perceptions, local.Perceptions...)
	output.Memories = append(memories, local.Memories...)
	output.VisibleEvents = append(visible, local.VisibleEvents...)
	output.PositionChanges = append(positions, local.PositionChanges...)
	if output.ActionResolution == nil {
		output.ActionResolution = resolution
	}
}

func mergeConfirmedInput(snapshot *Snapshot, output Output) {
	if snapshot.Sources == nil {
		snapshot.Sources = map[string]SourceMetadata{}
	}
	if snapshot.Perceptions == nil {
		snapshot.Perceptions = map[string][]wiaworld.Perception{}
	}
	for _, event := range output.Events {
		snapshot.Events = append(snapshot.Events, event)
		snapshot.Sources[event.EventID] = SourceMetadata{ID: event.EventID, Actor: event.ActorID, Kind: event.EventType, RunID: event.RunID, Stage: event.Stage, SceneVersion: event.SceneVersion}
	}
	mergePerceptions(snapshot, output.Perceptions)
	snapshot.OpenProgress = cloneOpenProgress(output.OpenProgress)
	if snapshot.AppliedRelationshipSources == nil {
		snapshot.AppliedRelationshipSources = map[string]bool{}
	}
	for _, change := range output.RelationshipChanges {
		snapshot.AppliedRelationshipSources[change.ProposalSourceID+"\x00"+change.After.SubjectID+"\x00"+change.After.TargetID+"\x00"+change.After.RelationType] = true
	}
}

// Perception identity matches persistence: each recipient, source and body is one experience.
func mergePerceptions(snapshot *Snapshot, additions []wiaworld.Perception) {
	if snapshot.Perceptions == nil {
		snapshot.Perceptions = map[string][]wiaworld.Perception{}
	}
	type identity struct{ recipient, source, content string }
	seen := map[identity]bool{}
	for recipient, items := range snapshot.Perceptions {
		for _, p := range items {
			seen[identity{recipient, p.SourceEventID, p.Content}] = true
		}
	}
	for _, p := range additions {
		key := identity{p.RecipientID, p.SourceEventID, p.Content}
		if !seen[key] {
			snapshot.Perceptions[p.RecipientID] = append(snapshot.Perceptions[p.RecipientID], p)
			seen[key] = true
		}
	}
}
