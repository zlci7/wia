package turn

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

type AppliedModifier struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
}

// ActionResolution is the program-owned result consumed by coordination and
// narration. Prepared identifies a durable choice; it is not itself a world fact.
type ActionResolution struct {
	RuleID    string             `json:"rule_id"`
	RuleName  string             `json:"rule_name"`
	ActionID  string             `json:"action_id"`
	Roll      int                `json:"roll,omitempty"`
	Target    int                `json:"target,omitempty"`
	Status    string             `json:"status"`
	Summary   string             `json:"summary"`
	Modifiers []AppliedModifier  `json:"modifiers,omitempty"`
	Effects   []story.RuleEffect `json:"-"`
	Reused    bool               `json:"reused"`
}

func actionRuleByID(def story.Definition, id string) (story.ActionRule, bool) {
	for _, rule := range def.ActionRules {
		if rule.ID == id {
			return rule, true
		}
	}
	return story.ActionRule{}, false
}

func prepareActionResolution(ctx context.Context, store *storage.WorldStore, snapshot Snapshot, run wiaworld.Run, ruleID string) (ActionResolution, error) {
	rule, ok := actionRuleByID(snapshot.Definition, ruleID)
	if !ok || run.InputID == "" || store == nil {
		return ActionResolution{}, ErrInvalidRequest
	}
	met, _ := evaluateFactConditions(snapshot, Output{Positions: snapshot.Positions, States: snapshot.States, Relationships: snapshot.Relationships, Items: snapshot.Items}, rule.Conditions, "player")
	if !met {
		return ActionResolution{}, ErrInvalidRequest
	}
	resolution := ActionResolution{RuleID: rule.ID, RuleName: rule.Name, ActionID: run.RunID + ":player-action", Status: "succeeded", Summary: rule.SuccessText, Effects: slices.Clone(rule.SuccessEffects)}
	if rule.Risk != nil {
		resolution.Target = rule.Risk.BaseTarget
	}
	if rule.Risk != nil {
		for _, modifier := range rule.Risk.Modifiers {
			if matched, _ := evaluateFactCondition(snapshot, Output{Positions: snapshot.Positions, States: snapshot.States, Relationships: snapshot.Relationships, Items: snapshot.Items}, modifier.Condition, "player"); matched {
				resolution.Target += modifier.Amount
				resolution.Modifiers = append(resolution.Modifiers, AppliedModifier{Label: modifier.Label, Amount: modifier.Amount})
			}
		}
	}
	if rule.Risk != nil && resolution.Target < rule.Risk.Minimum {
		resolution.Target = rule.Risk.Minimum
	}
	if rule.Risk != nil && resolution.Target > rule.Risk.Maximum {
		resolution.Target = rule.Risk.Maximum
	}
	encoded, _ := json.Marshal(resolution.Modifiers)
	roll := 0
	if rule.Risk != nil {
		n, err := rand.Int(rand.Reader, big.NewInt(100))
		if err != nil {
			return ActionResolution{}, err
		}
		roll = int(n.Int64()) + 1
	}
	prepared, reused, err := store.PrepareActionResolution(ctx, storage.ActionResolutionRecord{InputID: run.InputID, RuleID: rule.ID, Roll: roll, Target: resolution.Target, ModifiersJSON: string(encoded), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return ActionResolution{}, err
	}
	resolution.Roll, resolution.Target, resolution.Reused = prepared.Roll, prepared.Target, reused
	if err := json.Unmarshal([]byte(prepared.ModifiersJSON), &resolution.Modifiers); err != nil {
		return ActionResolution{}, err
	}
	if rule.Risk != nil && prepared.Roll > prepared.Target {
		resolution.Status, resolution.Summary, resolution.Effects = "failed", rule.FailureText, slices.Clone(rule.FailureEffects)
	}
	return resolution, nil
}

func validateActionResolutionOutcome(output Output, outcomes []hostActionResult) error {
	if output.ActionResolution == nil {
		return nil
	}
	for _, outcome := range outcomes {
		if strings.TrimSpace(outcome.ActionID) == output.ActionResolution.ActionID {
			if strings.ToLower(strings.TrimSpace(outcome.Status)) != output.ActionResolution.Status {
				return coordinationInvalid("program_resolution_mismatch", "outcomes.status", output.ActionResolution.Status)
			}
			return nil
		}
	}
	return coordinationInvalid("program_resolution_missing", "outcomes", "program-resolved-player-action")
}

func applyActionResolution(snapshot Snapshot, output *Output, host hostResult) (wiaworld.Event, error) {
	resolution := output.ActionResolution
	if resolution == nil {
		return wiaworld.Event{}, nil
	}
	var source mechanicSource
	found := false
	for _, candidate := range mechanicSources(output.Events, host.Outcomes) {
		if candidate.action.EventID == resolution.ActionID {
			source, found = candidate, true
			break
		}
	}
	if !found {
		return wiaworld.Event{}, coordinationInvalid("program_resolution_source", "outcomes", "matching-action-result")
	}
	text := resolution.Summary
	if resolution.Roll > 0 {
		text = fmt.Sprintf("%s：掷骰 %d，目标 %d，判定%s。%s", resolution.RuleName, resolution.Roll, resolution.Target, map[bool]string{true: "成功", false: "失败"}[resolution.Status == "succeeded"], resolution.Summary)
	}
	event := wiaworld.Event{EventID: fmt.Sprintf("%s:rule:%s", strings.TrimSuffix(resolution.ActionID, ":player-action"), resolution.RuleID), EventType: "rule_result", ActorID: "player", Content: text, RunID: source.action.RunID, Stage: 3, SceneVersion: output.SceneVersion, SourceType: "rule:" + resolution.RuleID + ":" + resolution.Status, ProjectionParentID: source.resultID, CreatedAt: time.Now().UTC()}
	output.Events = append(output.Events, event)
	for recipient := range source.recipients {
		output.Perceptions = append(output.Perceptions, wiaworld.Perception{RecipientID: recipient, SourceEventID: event.EventID, SourceType: event.SourceType, Content: text, Stage: 3, SceneVersion: output.SceneVersion, CreatedAt: event.CreatedAt})
	}
	if err := applyRuleStateEffects(snapshot, output, source.action.ActorID, resolution.RuleID, resolution.ActionID, event, resolution.Effects); err != nil {
		return wiaworld.Event{}, err
	}
	return event, nil
}
