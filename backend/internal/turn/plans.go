package turn

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type planUpdate struct {
	ID                 string   `json:"id"`
	Content            string   `json:"content"`
	SourceIDs          []string `json:"source_ids"`
	Status             string   `json:"status"`
	ReviewAfterMinutes int      `json:"review_after_minutes"`
}

func InitialOpenProgress(def story.Definition) (*wiaworld.OpenProgress, error) {
	if def.Progression == nil {
		return nil, nil
	}
	minute, err := plot.ClockMinute(def.Clock)
	if err != nil {
		return nil, err
	}
	progress := &wiaworld.OpenProgress{Plans: []wiaworld.PersonalPlan{}, ExternalApplied: map[string]string{}, DevelopmentChecks: map[string]string{}}
	for _, initial := range def.Progression.InitialPlans {
		material, ok := story.MaterialByID(def, initial.MaterialID)
		if !ok || material.OwnerID != initial.OwnerID || material.Purpose != "npc_plan" {
			return nil, fmt.Errorf("invalid initial plan material")
		}
		progress.Plans = append(progress.Plans, wiaworld.PersonalPlan{ID: initial.ID, OwnerID: initial.OwnerID, Content: material.Body, SourceIDs: []string{material.SourceID(def.Revision)}, NextCheck: minute + initial.ReviewAfterMinutes, LastCheck: -1, Status: "active", Version: 1, ContextEpoch: 1})
	}
	return progress, nil
}

func cloneOpenProgress(progress *wiaworld.OpenProgress) *wiaworld.OpenProgress {
	if progress == nil {
		return nil
	}
	copy := *progress
	copy.Plans = slices.Clone(progress.Plans)
	for i := range copy.Plans {
		copy.Plans[i].SourceIDs = slices.Clone(copy.Plans[i].SourceIDs)
	}
	copy.ExternalApplied, copy.DevelopmentChecks = map[string]string{}, map[string]string{}
	for k, v := range progress.ExternalApplied {
		copy.ExternalApplied[k] = v
	}
	for k, v := range progress.DevelopmentChecks {
		copy.DevelopmentChecks[k] = v
	}
	return &copy
}

func validateOpenProgress(progress *wiaworld.OpenProgress, def story.Definition, epoch int64) error {
	if progress == nil {
		return nil
	}
	if def.Progression == nil || len(progress.Plans) > 32 {
		return fmt.Errorf("invalid open progression state")
	}
	seen := map[string]bool{}
	for _, plan := range progress.Plans {
		_, owner := story.CharacterByID(def, plan.OwnerID)
		if !owner || plan.ID == "" || seen[plan.ID] || len(plan.SourceIDs) == 0 || len(plan.SourceIDs) > 8 || plan.Version < 1 || plan.ContextEpoch > epoch || strings.TrimSpace(plan.Content) == "" {
			return fmt.Errorf("invalid persisted personal plan")
		}
		if plan.Status != "active" && plan.Status != "paused" && plan.Status != "completed" && plan.Status != "cancelled" {
			return fmt.Errorf("invalid persisted plan status")
		}
		seen[plan.ID] = true
	}
	return nil
}

func personalPlanContext(snapshot Snapshot, recipient string, material Material) Material {
	if snapshot.OpenProgress == nil {
		return material
	}
	plans := []wiaworld.PersonalPlan{}
	var sources []string
	for _, plan := range snapshot.OpenProgress.Plans {
		if plan.OwnerID == recipient {
			plans = append(plans, plan)
			sources = append(sources, plan.SourceIDs...)
		}
	}
	material = appendRequiredMaterial(material, Section{Name: "current_personal_plans", Text: "你自己的存档当前计划（状态包括 active/paused/completed/cancelled；检查时间只是重估时间）：" + wire.MarshalJSON(plans), Sources: sources})
	material.System += "\nplan_updates 为你自己的计划调整数组，每项 id/content/source_ids/status/review_after_minutes。可修改本人已有计划或使用本人前缀的稳定ID建立计划。来源只能是本次提供的本人知识、当前计划或获准刺激。active 的 review_after_minutes 为1..43200；其他状态填0。没有调整返回[]。计划表达意向；实际非言语行动仍写 action_intent，由行动协调裁定。"
	return material
}

func validatePlanUpdates(snapshot Snapshot, owner string, stageInput StageInput, decision NPCDecision, call *ContextGenerator) error {
	if len(decision.PlanUpdates) == 0 {
		return nil
	}
	if snapshot.OpenProgress == nil || len(decision.PlanUpdates) > 4 {
		return coordinationInvalid("plan_updates_invalid", "plan_updates", "at-most-four-owned-plans-with-progression-enabled")
	}
	known := map[string]bool{}
	for _, id := range stageInput.SourceEventIDs {
		known[id] = true
	}
	for _, perception := range snapshot.Perceptions[owner] {
		known[perception.SourceEventID] = true
	}
	for _, id := range call.providedSources {
		known[id] = true
	}
	seen := map[string]bool{}
	for _, update := range decision.PlanUpdates {
		owned := false
		for _, plan := range snapshot.OpenProgress.Plans {
			if plan.ID == update.ID {
				if plan.OwnerID != owner {
					return coordinationInvalid("plan_owner_invalid", "plan_updates.id", "owned-plan")
				}
				owned = true
			}
		}
		if update.ID == "" || len(update.ID) > 100 || (!owned && !strings.HasPrefix(update.ID, owner+":")) || seen[update.ID] || strings.TrimSpace(update.Content) == "" || !utf8.ValidString(update.Content) || len([]rune(update.Content)) > 1200 || len(update.SourceIDs) == 0 || len(update.SourceIDs) > 8 {
			return coordinationInvalid("plan_update_invalid", "plan_updates", "unique-owned-plan-with-bounded-content-and-sources")
		}
		seen[update.ID] = true
		if update.Status != "active" && update.Status != "paused" && update.Status != "completed" && update.Status != "cancelled" {
			return coordinationInvalid("plan_status_invalid", "plan_updates.status", "active|paused|completed|cancelled")
		}
		if update.Status == "active" && (update.ReviewAfterMinutes < 1 || update.ReviewAfterMinutes > 43200) || update.Status != "active" && update.ReviewAfterMinutes != 0 {
			return coordinationInvalid("plan_time_invalid", "plan_updates.review_after_minutes", "future-review-for-active-plan")
		}
		for _, id := range update.SourceIDs {
			if !known[id] {
				return ErrContextSourceMissing
			}
		}
	}
	return nil
}

func applyPlanUpdates(output *Output, run wiaworld.Run, owner string, decision NPCDecision, stage int) {
	if output.OpenProgress == nil {
		return
	}
	minute, _ := plot.ClockMinute(output.Clock)
	for i, update := range decision.PlanUpdates {
		index := -1
		for j, plan := range output.OpenProgress.Plans {
			if plan.ID == update.ID {
				index = j
				break
			}
		}
		version := int64(1)
		if index >= 0 {
			version = output.OpenProgress.Plans[index].Version + 1
		}
		eventID := fmt.Sprintf("%s:%s:plan:%d:%d", run.RunID, owner, stage, i)
		plan := wiaworld.PersonalPlan{ID: update.ID, OwnerID: owner, Content: update.Content, SourceIDs: slices.Clone(update.SourceIDs), NextCheck: minute + update.ReviewAfterMinutes, LastCheck: minute, Status: update.Status, Version: version, ContextEpoch: run.BaseContextEpoch}
		if index < 0 {
			output.OpenProgress.Plans = append(output.OpenProgress.Plans, plan)
		} else {
			output.OpenProgress.Plans[index] = plan
		}
		event := wiaworld.Event{EventID: eventID, EventType: "npc_plan_updated", ActorID: owner, TargetID: owner, Content: wire.MarshalJSON(plan), RunID: run.RunID, Stage: stage, SceneVersion: output.SceneVersion, SourceType: "personal_plan", CreatedAt: time.Now().UTC()}
		output.Events = append(output.Events, event)
	}
}
