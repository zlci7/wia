package turn

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
	basis := ""
	if checkID == "" {
		developments := snapshot.Definition.Progression.Developments
		start := 0
		for i, development := range developments {
			if development.ID == output.OpenProgress.DevelopmentCursor {
				start = (i + 1) % len(developments)
			}
		}
		for offset := range len(developments) {
			development := developments[(start+offset)%len(developments)]
			if !developmentRelevant(working, output.Events, development) {
				continue
			}
			candidateBasis := developmentBasis(working, *output, development)
			if output.OpenProgress.DevelopmentChecks[development.ID] == candidateBasis {
				continue
			}
			checkID, materialIDs, basis = development.ID, development.MaterialIDs, candidateBasis
			break
		}
	}
	woken := []string{}
	if checkID != "" {
		material, err := composeOpenWorld(working, *output, checkID, external, materialIDs)
		if err != nil {
			return nil, err
		}
		call := s.generator(generator, material, working, run, "plot", "coordinator", 4, "story.open-world.v3")
		var result plotResolution
		callCtx, cancel := context.WithTimeout(ctx, GenerationTimeBudget)
		_, err = GenerateJSONCheckedMetrics(callCtx, call, material.System, material.Required, &result, structuredTurnOutputTokens, nil, []string{"status", "content", "source_ids", "projections", "decision_requests", "ending"}, func() error { return validateOpenResolution(working, result, call.(*ContextGenerator)) })
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
			output.OpenProgress.DevelopmentCursor = checkID
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

func composeOpenWorld(snapshot Snapshot, output Output, checkID string, external bool, materialIDs []string) (Material, error) {
	material := Material{System: BehaviorContract + "\n你是持续世界协调器。依据发展材料、当前压力和已确认经历，形成具体、有因果的外部变化与可参与的新情境。环境变化、公开消息、线索的可见后果和到期安排可以推进处境；没有合理变化时暂缓或跳过，不强行制造危机或拉回玩家已拒绝的机会。最新位置、物品、关系和状态优先于开场描述。重要人物的意向与行动由本人决定；你提供其实际可感知的刺激，相关接收者再独立决定回应、拒绝、沉默或延续计划。只返回 JSON。",
		Required: fmt.Sprintf("游戏时间：%s\n本轮已确认记录：%s\n当前位置：%s\n最新能力工作态：%s\n逐人情境：%s\n待评估对象：%s；external_schedule=%t\n输出 status(occurred/deferred/skipped)、content(作者真实结果)、source_ids、projections(逐人recipient/content/scene)、decision_requests、ending(空字符串)。每个人只能获得其实际观察或有效传达的部分；不得为共享信息改变人物位置。decision_requests最多两名已收到本次projection的重要人物。deferred不产生projection或决定请求；不确定时暂缓，不编造完成。", output.Clock, worldProgressionRecords(output.Events), wire.MarshalJSON(output.Positions), HostMechanicsContext(snapshot), CoordinationScene(snapshot), checkID, external), RequiredSources: append(EventIDs(output.Events), SceneViewSources(snapshot, "")...), Optional: plotEvidenceSections(snapshot.Events)}
	for _, id := range materialIDs {
		m, ok := story.MaterialByID(snapshot.Definition, id)
		if !ok {
			return Material{}, ErrContextSourceMissing
		}
		material = appendRequiredMaterial(material, materialSection(m, snapshot.Definition.Revision))
	}
	return material, nil
}

// World evaluation and subsequent action coordination use game time and ordered events.
// Persistence timestamps, unassigned sequence numbers and the shared run ID stay in storage.
func worldProgressionRecords(events []wiaworld.Event) string {
	type record struct {
		EventID      string `json:"event_id"`
		EventType    string `json:"event_type"`
		ActorID      string `json:"actor_id,omitempty"`
		TargetID     string `json:"target_id,omitempty"`
		Content      string `json:"content"`
		Stage        int    `json:"stage"`
		SceneVersion int64  `json:"scene_version"`
		SourceType   string `json:"source_type"`
	}
	records := make([]record, 0, len(events))
	for _, event := range events {
		records = append(records, record{EventID: event.EventID, EventType: event.EventType, ActorID: event.ActorID, TargetID: event.TargetID, Content: event.Content, Stage: event.Stage, SceneVersion: event.SceneVersion, SourceType: event.SourceType})
	}
	return wire.MarshalJSON(records)
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
