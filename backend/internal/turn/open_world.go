package turn

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// Open worlds share the existing projection, independent decision, and action
// resolution path. A plan review never directly executes a character's intention.
func (s *Service) advanceOpenWorld(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, output *Output) ([]wiaworld.Event, error) {
	if output.OpenProgress == nil {
		return nil, fmt.Errorf("open progression state is missing")
	}
	if err := validateOpenProgress(output.OpenProgress, snapshot.Definition, run.BaseContextEpoch); err != nil {
		return nil, err
	}
	minute, err := plot.ClockMinute(output.Clock)
	if err != nil {
		return nil, err
	}
	working := snapshot
	working.Summary.Clock = output.Clock
	working.Positions, working.States, working.Relationships, working.Items = output.Positions, output.States, output.Relationships, output.Items
	working.SceneViews, working.OpenProgress = output.SceneViews, cloneOpenProgress(output.OpenProgress)
	working.PositionSources = clonePositions(snapshot.PositionSources)
	for _, change := range output.PositionChanges {
		working.PositionSources[change.EntityID] = change.SourceEventID
	}
	var visible []wiaworld.Event
	var materialIDs []string
	checkID, external := "", false
	for _, schedule := range snapshot.Definition.Progression.ExternalSchedules {
		if schedule.AtMinute <= minute && output.OpenProgress.ExternalApplied[schedule.ID] == "" {
			checkID, materialIDs, external = schedule.ID, []string{schedule.MaterialID}, true
			break
		}
	}
	basis := fmt.Sprintf("%x", sha256.Sum256([]byte(wire.MarshalJSON(struct {
		Clock     string
		Positions map[string]string
		Facts     string
		Events    []string
	}{output.Clock, output.Positions, HostMechanicsContext(working), EventIDs(output.Events)}))))
	if checkID == "" {
		for _, development := range snapshot.Definition.Progression.Developments {
			if output.OpenProgress.DevelopmentChecks[development.ID] == basis {
				continue
			}
			relevant := len(development.LocationIDs) == 0 && len(development.EntityIDs) == 0
			for _, location := range output.Positions {
				relevant = relevant || slices.Contains(development.LocationIDs, location)
			}
			for _, entity := range development.EntityIDs {
				relevant = relevant || strings.Contains(wire.MarshalJSON(output.Events), entity)
			}
			if relevant {
				checkID, materialIDs = development.ID, development.MaterialIDs
				break
			}
		}
	}
	woken := []string{}
	if checkID != "" {
		material := Material{System: BehaviorContract + "\n你是持续世界协调器。评估当前矛盾、压力或已到期的外部安排。读取最新权威事实，不将作者初始描述覆盖物品归属、位置、关系或状态。人物意向由本人决定；你只提供真实外部变化与个人可感知的刺激，不代写重要人物已经行动。只返回 JSON。",
			Required: fmt.Sprintf("游戏时间：%s\n本轮已确认记录：%s\n当前位置：%s\n最新能力工作态：%s\n逐人情境：%s\n待评估对象：%s；external_schedule=%t\n输出 status(occurred/deferred/skipped)、content(作者真实结果)、source_ids、projections(逐人recipient/content/scene)、decision_requests、ending(空字符串)。每个人只能获得其实际观察或有效传达的部分；不得为共享信息改变人物位置。decision_requests最多两名已收到本次projection的重要人物。deferred不产生projection或决定请求；不确定时暂缓，不编造完成。", output.Clock, wire.MarshalJSON(output.Events), wire.MarshalJSON(output.Positions), HostMechanicsContext(working), CoordinationScene(working), checkID, external), RequiredSources: append(EventIDs(output.Events), SceneViewSources(working, "")...), Optional: plotEvidenceSections(snapshot.Events)}
		for _, id := range materialIDs {
			m, ok := story.MaterialByID(snapshot.Definition, id)
			if !ok {
				return nil, ErrContextSourceMissing
			}
			material = appendRequiredMaterial(material, materialSection(m, snapshot.Definition.Revision))
		}
		call := s.generator(generator, material, working, run, "plot", "coordinator", 4, "story.open-world.v1")
		var result plotResolution
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_, err := GenerateJSONCheckedMetrics(callCtx, call, material.System, material.Required, &result, structuredTurnOutputTokens, nil, []string{"status", "content", "source_ids", "projections", "decision_requests", "ending"}, func() error { return validateOpenResolution(working, result, call.(*ContextGenerator)) })
		cancel()
		if err != nil {
			return nil, err
		}
		id := run.RunID + ":world:" + checkID
		observed, err := s.publishPlotResolution(ctx, generator, working, run, id, result, output)
		if err != nil {
			return nil, err
		}
		visible = append(visible, observed...)
		woken = slices.Clone(result.DecisionRequests)
		if external && result.Status != "deferred" {
			output.OpenProgress.ExternalApplied[checkID] = id
		}
		if !external {
			output.OpenProgress.DevelopmentChecks[checkID] = basis
		}
	}
	minute, _ = plot.ClockMinute(output.Clock)
	owners := plot.DuePlanOwners(output.OpenProgress.Plans, minute, 2)
	var projections []plotProjection
	for _, owner := range owners {
		if slices.Contains(woken, owner) || len(woken) >= 2 {
			continue
		}
		woken = append(woken, owner)
		projections = append(projections, plotProjection{Recipient: owner, Content: fmt.Sprintf("游戏内已到%s，你自己安排的计划到达检查时间。结合本人当前位置、已获准事实与现有计划重新考虑；检查时间不代表行动成功。", output.Clock)})
	}
	if len(projections) > 0 {
		requests := []string{}
		for _, p := range projections {
			requests = append(requests, p.Recipient)
		}
		result := plotResolution{Status: "occurred", Content: "人物自己的计划到达重估时间。", Projections: projections, DecisionRequests: requests, SourceIDs: []string{run.RunID + ":clock"}}
		working.Positions, working.States, working.Relationships, working.Items = output.Positions, output.States, output.Relationships, output.Items
		working.OpenProgress = cloneOpenProgress(output.OpenProgress)
		observed, err := s.publishPlotResolution(ctx, generator, working, run, run.RunID+":plan-review", result, output)
		if err != nil {
			return nil, err
		}
		visible = append(visible, observed...)
	}
	minute, _ = plot.ClockMinute(output.Clock)
	for i := range output.OpenProgress.Plans {
		plan := &output.OpenProgress.Plans[i]
		if slices.Contains(woken, plan.OwnerID) && plan.Status == "active" && plan.NextCheck <= minute {
			plan.LastCheck, plan.NextCheck = minute, minute+30
			plan.Version++
		}
	}
	return visible, nil
}

func validateOpenResolution(snapshot Snapshot, result plotResolution, call *ContextGenerator) error {
	if (result.Status != "occurred" && result.Status != "deferred" && result.Status != "skipped") || strings.TrimSpace(result.Content) == "" || len(result.SourceIDs) == 0 || len(result.SourceIDs) > 16 || result.Projections == nil || result.DecisionRequests == nil || len(result.DecisionRequests) > 2 || result.Ending != "" {
		return coordinationInvalid("open_world_result_invalid", "status", "bounded-world-result-with-sources-and-no-ending")
	}
	if result.Status == "deferred" && (len(result.Projections) > 0 || len(result.DecisionRequests) > 0) {
		return coordinationInvalid("open_world_deferred_effects", "projections", "empty-when-deferred")
	}
	for _, id := range result.SourceIDs {
		if !slices.Contains(call.providedSources, id) {
			return ErrContextSourceMissing
		}
	}
	seen := map[string]bool{}
	for _, p := range result.Projections {
		_, character := story.CharacterByID(story.Definition{Characters: snapshot.Characters}, p.Recipient)
		if (p.Recipient != "player" && !character) || seen[p.Recipient] || strings.TrimSpace(p.Content) == "" {
			return coordinationInvalid("open_world_projection_invalid", "projections", "unique-defined-recipient-with-content")
		}
		seen[p.Recipient] = true
	}
	wake := map[string]bool{}
	for _, id := range result.DecisionRequests {
		if id == "player" || !seen[id] || wake[id] {
			return coordinationInvalid("open_world_decision_invalid", "decision_requests", "unique-stimulated-important-character")
		}
		wake[id] = true
	}
	return nil
}
