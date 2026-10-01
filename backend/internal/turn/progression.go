package turn

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type eventCandidate struct {
	Condition    string         `json:"condition"`
	Development  string         `json:"development"`
	AfterMinutes int            `json:"after_minutes"`
	Initial      plotResolution `json:"initial"`
}

func (s *Service) advanceGeneratedEvents(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, opportunity *eventOpportunity, output *Output) ([]wiaworld.Event, error) {
	p := snapshot.Definition.EventGeneration
	if p == nil {
		return nil, nil
	}
	state := snapshot.GeneratedEvents
	state.Active = append([]GeneratedEvent{}, state.Active...)
	output.GeneratedEvents = &state
	current, err := plot.ClockMinute(output.Clock)
	if err != nil {
		return nil, err
	}
	// An authored evaluation consumes the round even when it defers without an event.
	if _, due, ok := NextPlotNode(snapshot); ok && current >= due {
		return nil, nil
	}
	if index, due, ok := NextGeneratedEvent(snapshot); ok && current >= due {
		e := state.Active[index]
		base := snapshot
		base.Plot = &plot.Definition{Revision: "generated.v1", Facts: snapshot.Definition.Secret + "\n本事件已成立起点：" + e.Premise, Nodes: []plot.Node{e.Node}}
		base.PlotProgress = plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{}}
		if e.State.Status != "" {
			base.PlotProgress.Nodes[e.Node.ID] = e.State
		}
		authoredProgress := output.PlotProgress
		visible, err := s.advancePlot(ctx, generator, base, run, output)
		if err != nil {
			return nil, err
		}
		next := output.PlotProgress.Nodes[e.Node.ID]
		output.PlotProgress = authoredProgress
		if next.Status == "deferred" {
			state.Active[index].State = next
		} else {
			for i := range output.Events {
				if output.Events[i].EventID == next.EventID {
					output.Events[i].ProjectionParentID = e.StartID
				}
			}
			state.Active = slices.Delete(state.Active, index, index+1)
			state.Completed++
		}
		return visible, nil
	}
	nextTurn := snapshot.Summary.TurnSeq + 1
	if opportunity == nil || len(state.Active) >= p.MaxActive || (state.LastOfferTurn > 0 && nextTurn-state.LastOfferTurn < p.CooldownTurns) {
		return nil, nil
	}
	if (opportunity.Kind != "arrival" && opportunity.Kind != "significant_change") || !slices.Contains(p.Locations, opportunity.Location) {
		return nil, fmt.Errorf("%w: event_opportunity_scope", ErrGenerationFailed)
	}
	var trigger wiaworld.Event
	for _, e := range output.Events {
		suffix, matches := strings.CutPrefix(e.EventID, opportunity.ActionID+":result:")
		n, parseErr := strconv.Atoi(suffix)
		if matches && parseErr == nil && n > 0 && (e.EventType == "player_action_result" || e.EventType == "npc_action_result") && (e.SourceType == "action_succeeded" || e.SourceType == "action_partial") {
			trigger = e
			break
		}
	}
	if trigger.EventID == "" {
		return nil, fmt.Errorf("%w: event_opportunity_result", ErrContextSourceMissing)
	}
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		location := output.Positions[trigger.ActorID]
		if opportunity.Kind == "arrival" {
			location = ""
			for _, change := range output.PositionChanges {
				if change.ActionID == opportunity.ActionID {
					location = change.To
					break
				}
			}
		}
		if location == "" || location != opportunity.Location {
			return nil, fmt.Errorf("%w: event_opportunity_location", ErrGenerationFailed)
		}
	}
	state.LastOfferTurn = nextTurn
	material := Material{
		System:          BehaviorContract + "\n你是开放世界事件协调器。在作者范围内，依据已确认的新情境选择是否发生一个小型外部事件。沿用现有事件优先；没有合适事件时返回空候选。重要NPC的新决定只由本人作出，玩家不自动接受任务。只返回JSON。",
		Required:        fmt.Sprintf("世界规则：%s\n作者事实：%s\n生成范围：%s\n地点目录：%s\n机会：%s\n已确认触发结果：%s\n游戏内时间：%s\n在场人物：%s\n接收者场景：%s\n当前已有事件：%s\n作者剧情：%s\n返回candidates数组，0或1项。每项condition(何时可收束)、development(有干预与不参与时的后续可能及结束条件)、after_minutes(1到120)、initial对象。initial字段status必须occurred，content为本次外部事实，source_ids只能引用触发结果ID，projections按接收者给出content及可选scene，decision_requests只含本次确实得到新刺激且需要本人决定的NPC，ending为空。不得新增重要NPC、地图地点、强迫玩家承诺、覆写作者主线、把计划写成已发生事实。后续发展须可通过等待、参与或拒绝自然结算。只向真实目击或有来源获知者投影，不广播作者秘密。参与者上限=%s加player。背景人物可有符合设定的日常反应。所有数组使用[]，不使用null。", snapshot.Definition.Rules, snapshot.Definition.Secret, p.Scope, wire.MarshalJSON(snapshot.Definition.Locations), wire.MarshalJSON(opportunity), wire.MarshalJSON(trigger), output.Clock, wire.MarshalJSON(output.SceneCharacters), wire.MarshalJSON(output.SceneViews), wire.MarshalJSON(state.Active), wire.MarshalJSON(snapshot.Plot), wire.MarshalJSON(p.Participants)),
		RequiredSources: []string{trigger.EventID}, Optional: plotEvidenceSections(snapshot.Events),
	}
	material.Required += "\n每个 projection 的 recipient 最多出现一次，只能使用上述参与者上限中的重要NPC ID或player。背景人物只出现在事件描述中。"
	material.Required += worldEventSpatialContext(snapshot, *output)
	working := snapshot
	working.Positions, working.States, working.Relationships, working.Items = output.Positions, output.States, output.Relationships, output.Items
	material.Required += "\n最新权威状态、关系、物品及定义（初始材料不覆盖当前事实）：" + HostMechanicsContext(working)
	call := s.generator(generator, material, snapshot, run, "event_generation", "coordinator", 4, "story.events.v3")
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var response struct {
		Candidates []eventCandidate `json:"candidates"`
	}
	validate := func() error {
		if response.Candidates == nil || len(response.Candidates) > 1 {
			return fmt.Errorf("%w: event_candidate_count", ErrGenerationFailed)
		}
		if len(response.Candidates) == 0 {
			return nil
		}
		c := response.Candidates[0]
		if wire.Clean(c.Condition) == "" || wire.Clean(c.Development) == "" || len([]rune(c.Initial.Content+c.Condition+c.Development)) > 4000 || c.AfterMinutes < 1 || c.AfterMinutes > 120 || c.Initial.Status != "occurred" || c.Initial.Ending != "" || len(c.Initial.SourceIDs) != 1 || c.Initial.SourceIDs[0] != trigger.EventID {
			return fmt.Errorf("%w: event_candidate_fields", ErrGenerationFailed)
		}
		base := snapshot
		base.Plot = &plot.Definition{Revision: "generated.v1"}
		node := plot.Node{Audience: append([]string{"player"}, p.Participants...)}
		return validatePlotResolution(base, node, *output, c.Initial)
	}
	_, err = GenerateJSONCheckedMetrics(callCtx, call, material.System, material.Required, &response, structuredTurnOutputTokens, nil, []string{"candidates"}, func() error {
		if validationErr := validate(); validationErr != nil {
			if s.deps.Logger != nil {
				s.deps.Logger.Printf("story event_generation validation failed: world_id=%q run_id=%q boundary=%q", snapshot.Summary.WorldID, run.RunID, validationErr.Error())
			}
			return &GenerationError{Code: "event_candidate_invalid", Field: "candidates", Expected: validationErr.Error(), Cause: validationErr}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(response.Candidates) == 0 {
		return nil, nil
	}
	c := response.Candidates[0]
	key := run.InputID
	if key == "" {
		key = run.RunID
	}
	root := "generated:" + key
	node := plot.Node{ID: key, AtMinute: current + c.AfterMinutes, After: []string{}, Condition: c.Condition, Development: c.Development, Audience: append([]string{"player"}, p.Participants...)}
	visible, err := s.publishPlotResolution(ctx, generator, snapshot, run, root, c.Initial, output)
	if err != nil {
		return nil, err
	}
	for i := range output.Events {
		if output.Events[i].EventID == root {
			output.Events[i].ProjectionParentID = trigger.EventID
		}
	}
	state.Active = append(state.Active, GeneratedEvent{Node: node, StartID: root, TriggerID: trigger.EventID, Location: opportunity.Location, Premise: c.Initial.Content})
	// The author plan is archived with the same transaction, never projected as facts.
	output.Events = append(output.Events, wiaworld.Event{EventID: root + ":plan", EventType: "generated_event_plan", ActorID: "world", Content: wire.MarshalJSON(state.Active[len(state.Active)-1]), RunID: run.RunID, Stage: 4, SceneVersion: output.SceneVersion, SourceType: "author_plan", ProjectionParentID: root, CreatedAt: time.Now().UTC()})
	return visible, nil
}

type plotProjection struct {
	Recipient string `json:"recipient"`
	Content   string `json:"content"`
	Scene     string `json:"scene,omitempty"`
}

// Presence is an effect of this actor's resolved action, not of being selected
// for a decision round. Earlier speech retains its event-time audience.
type plotActionResult struct {
	hostActionResult
	ActorInScene *bool `json:"actor_in_scene,omitempty"`
}

func plotActionPresence(current []string, events []wiaworld.Event, outcomes []plotActionResult) ([]string, error) {
	ids := append([]string{}, current...)
	seen := map[string]bool{}
	for _, o := range outcomes {
		if o.ActorInScene == nil {
			continue
		}
		e, ok := EventByID(events, o.ActionID)
		if !ok || e.EventType != "npc_action_intent" || seen[e.ActorID] || (o.Status != "succeeded" && o.Status != "partial") {
			return nil, fmt.Errorf("%w: invalid plot action presence", ErrGenerationFailed)
		}
		seen[e.ActorID] = true
		if *o.ActorInScene && !slices.Contains(ids, e.ActorID) {
			ids = append(ids, e.ActorID)
		}
		if !*o.ActorInScene {
			ids = slices.DeleteFunc(ids, func(id string) bool { return id == e.ActorID })
		}
	}
	return ids, nil
}

type plotResolution struct {
	Status           string           `json:"status"` // occurred, deferred, skipped
	Content          string           `json:"content"`
	SourceIDs        []string         `json:"source_ids"`
	Projections      []plotProjection `json:"projections"`
	DecisionRequests []string         `json:"decision_requests"`
	Ending           string           `json:"ending"`
}

// A single node is settled per  The clock stops at that node; a subsequent
// input can continue waiting against the newly committed consequences.
func (s *Service) advancePlot(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, output *Output) ([]wiaworld.Event, error) {
	if snapshot.Definition.Progression != nil {
		return s.advanceOpenWorld(ctx, generator, snapshot, run, output)
	}
	if snapshot.Plot == nil {
		return nil, nil
	}
	output.PlotProgress = &plot.Progress{Version: snapshot.PlotProgress.Version, Ending: snapshot.PlotProgress.Ending, Nodes: map[string]plot.NodeState{}}
	for id, state := range snapshot.PlotProgress.Nodes {
		output.PlotProgress.Nodes[id] = state
	}
	node, due, ok := NextPlotNode(snapshot)
	current, err := plot.ClockMinute(output.Clock)
	if err != nil {
		return nil, err
	}
	if !ok || current < due {
		return nil, nil
	}
	material := composePlot(snapshot, run, node, output)
	requirementsMet, requirementEvidence := evaluateFactConditions(snapshot, *output, node.Requirements, "")
	if len(node.Requirements) > 0 {
		onUnmet := node.OnUnmet
		if onUnmet == "" || onUnmet == "defer" {
			onUnmet = "deferred"
		} else {
			onUnmet = "skipped"
		}
		material.Required += fmt.Sprintf("\n程序条件结论：met=%t，evidence=%s。条件成立时依据作者条件继续判断；条件不成立时 status 必须为 %s，source_ids 必须包含全部 evidence。程序条件结论不可被自然语言覆盖。", requirementsMet, wire.MarshalJSON(requirementEvidence), onUnmet)
	}
	call := s.generator(generator, material, snapshot, run, "plot", "coordinator", 4, "story.plot.v4")
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var result plotResolution
	if err = GenerateJSON(callCtx, call, material.System, material.Required, &result, structuredTurnOutputTokens, "status", "content", "source_ids", "projections", "decision_requests", "ending"); err != nil {
		return nil, err
	}
	if err = validatePlotResolution(snapshot, node, *output, result); err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("story plot validation failed: world_id=%q run_id=%q error_code=%q boundary=%q", snapshot.Summary.WorldID, run.RunID, ErrorCode(err), err.Error())
		}
		return nil, err
	}
	if len(node.Requirements) > 0 && !requirementsMet {
		expected := node.OnUnmet
		if expected == "" {
			expected = "deferred"
		} else if expected == "defer" {
			expected = "deferred"
		} else {
			expected = "skipped"
		}
		if result.Status != expected {
			return nil, fmt.Errorf("%w: structured_plot_condition", ErrGenerationFailed)
		}
		if len(result.Projections) > 0 || len(result.DecisionRequests) > 0 {
			return nil, fmt.Errorf("%w: unmet_structured_condition_projection", ErrGenerationFailed)
		}
		for _, id := range requirementEvidence {
			if !slices.Contains(result.SourceIDs, id) {
				return nil, ErrContextSourceMissing
			}
		}
	}
	state := plot.NodeState{Status: result.Status, Content: result.Content, NextCheck: current + 1, Evidence: result.SourceIDs}
	if result.Status != "deferred" {
		state.EventID = "plot:" + snapshot.Plot.Revision + ":" + node.ID
	}
	visible, err := s.publishPlotResolution(ctx, generator, snapshot, run, state.EventID, result, output)
	if err != nil {
		return nil, err
	}
	output.PlotProgress.Nodes[node.ID] = state
	output.PlotProgress.Version++
	if snapshot.Summary.Mode == "guided" && result.Ending != "" {
		output.PlotProgress.Ending = result.Ending
	}
	return visible, nil
}

func (s *Service) publishPlotResolution(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, rootID string, result plotResolution, output *Output) ([]wiaworld.Event, error) {
	var visible []wiaworld.Event
	if result.Status != "deferred" {
		event := wiaworld.Event{EventID: rootID, EventType: "plot_result", ActorID: "world", Content: result.Content, RunID: run.RunID, Stage: 4, SceneVersion: output.SceneVersion, SourceType: "plot_" + result.Status, CreatedAt: time.Now().UTC()}
		output.Events = append(output.Events, event)
		for i, p := range result.Projections {
			// Projection IDs have their own text. Possessing their ID never grants the
			// receiving character access to the author's complete plot result.
			projection := event
			projection.EventID = fmt.Sprintf("%s:projection:%d", rootID, i)
			projection.EventType, projection.TargetID, projection.Content = "plot_perceived", p.Recipient, p.Content
			projection.SourceType = "plot_observed"
			projection.ProjectionParentID = event.EventID
			output.Events = append(output.Events, projection)
			output.Perceptions = append(output.Perceptions, wiaworld.Perception{RecipientID: p.Recipient, SourceEventID: projection.EventID, SourceType: "plot_observed", Content: p.Content, Stage: 4, SceneVersion: output.SceneVersion, CreatedAt: event.CreatedAt})
			if p.Recipient == "player" {
				visible = append(visible, projection)
			}
		}
	}
	sources := plotSceneSources(*output, visible)
	updates := []sceneUpdate{}
	for i, p := range result.Projections {
		id := fmt.Sprintf("%s:projection:%d", rootID, i)
		if wire.Clean(p.Scene) != "" {
			updates = append(updates, sceneUpdate{Content: p.Scene, SourceIDs: []string{"view:" + p.Recipient, id}, Recipients: []string{p.Recipient}})
		}
	}
	if err := applyPlotSceneUpdates(output, sources, updates); err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Printf("story plot scene rejected: world_id=%q run_id=%q constraint=%q", snapshot.Summary.WorldID, run.RunID, err.Error())
		}
		return nil, err
	}
	if len(result.DecisionRequests) > 0 {
		npcVisible, err := s.respondToPlot(ctx, generator, snapshot, run, rootID, result, output)
		if err != nil {
			return nil, err
		}
		visible = append(visible, npcVisible...)
	}
	return visible, nil
}

func composePlot(snapshot Snapshot, run wiaworld.Run, node plot.Node, output *Output) Material {
	snapshot.SceneViews = output.SceneViews
	snapshot.Characters = append([]wiaworld.Character{}, snapshot.Characters...)
	for i := range snapshot.Characters {
		snapshot.Characters[i].InScene = slices.Contains(output.SceneCharacters, snapshot.Characters[i].EntityID)
	}
	material := Material{
		System:          BehaviorContract + "\n你是世界剧情协调器。按当前世界时间、已发生的结果和作者剧情约束处理一个节点。玩家表达是尝试，NPC对白是声称，文学补写不属于事实。不得替重要NPC产生新决定，需要本人决定时在 decision_requests 列出其ID，并先给该人物一个真实且获准的新刺激。先公布外部情境，不提前写成该人物已经选择或完成行动。只返回JSON。",
		Required:        fmt.Sprintf("模式：%s\n游戏内时间：%s\n固定事实：%s\n当前节点：%s\n已提交进度：%s\n人物在场情况：%s\n分接收者场景：%s\n本轮已确认记录：%s\n输出字段：status(occurred/deferred/skipped)、content(作者层真实结果)、source_ids(证据ID数组)、projections(对象数组，每项recipient/content)、decision_requests(字符串数组)、ending(字符串)。证据只能来自提供的事件或 definition:%s:%s；至少一条。条件不足时 deferred、projections=[]、decision_requests=[]、ending=空字符串；skipped 记录确实被干预阻止的发展。projections 仅包含当前节点允许的 audience 中实际观察或经明确来源获知的人物，隐情不随公共迹象广播；场外人物不自动听到场内对白，玩家不自动知道场外结局。ending仅在terminal节点且条件实际成立时填写，拒绝或不参与可以产生相应结果，不伪造玩家同意。无内容的数组使用[]，不得null。", snapshot.Summary.Mode, output.Clock, snapshot.Plot.Facts, wire.MarshalJSON(node), wire.MarshalJSON(snapshot.PlotProgress), wire.MarshalJSON(wiaworld.PublicCharacterViews(snapshot.Characters)), CoordinationScene(snapshot), wire.MarshalJSON(output.Events), snapshot.Plot.Revision, node.ID),
		RequiredSources: EventIDs(output.Events), Optional: plotEvidenceSections(snapshot.Events),
	}
	material.Required += "\n每个 projection 另可含 scene 字符串：只依据此人的旧视图与本次获准感知，写其事件后的完整简明情境；无状态变化可留空。它只交给对应 recipient，作者真相不进入其中，NPC待决定行动保持未执行。程序绑定该人物和投影来源，无须输出另一个场景更新表。"
	material.Required += "\n接收与唤醒合同：每个 recipient 最多出现一次，只选当前节点 audience 中的ID。decision_requests 只选本次 projections 已提供刺激的重要NPC ID，最多一次；player、背景人物、信使等没有独立Agent的角色不放入 decision_requests。没有符合条件的人物时返回[]。"
	material.Required += worldEventSpatialContext(snapshot, *output)
	working := snapshot
	working.Positions, working.States, working.Relationships, working.Items = output.Positions, output.States, output.Relationships, output.Items
	material.Required += "\n最新权威状态、关系、物品及定义（初始材料不覆盖当前事实）：" + HostMechanicsContext(working)
	material.RequiredSources = append(material.RequiredSources, currentFactSources(working)...)
	material.Required += "\n保留场景与已提交节点的来源ID也可以引用，但仅用于这些已提供状态，不据ID猜测未提供的原文。definition引用仅限本次当前节点；其他节点的发生依据使用其event_id，不使用其未来计划。"
	material.RequiredSources = append(material.RequiredSources, SceneViewSources(snapshot, "")...)
	for _, state := range snapshot.PlotProgress.Nodes {
		if state.EventID != "" {
			material.RequiredSources = append(material.RequiredSources, state.EventID)
		}
	}
	return material
}

func worldEventSpatialContext(snapshot Snapshot, output Output) string {
	if snapshot.Definition.Capabilities["spatial"] != 1 {
		return ""
	}
	return "\n事件发生时的权威位置(JSON)：" + wire.MarshalJSON(output.Positions) + "\n人物当前位置以此表为准，分接收者场景中的旧文字不改变位置。外部事件的 content、projections 和 scene 保持这些位置；重要NPC的新对白、移动、交付和其他自主行动由本人在 decision_requests 后决定，再由行动协调结算。本阶段只提供外部刺激，不把这些待决定行动写成已发生。接收者须在自己当前位置真实感知或通过明确来源获知，不能为了让其目击而将其移到事件地点。"
}

func plotEvidenceSections(events []wiaworld.Event) []Section {
	var result []Section
	for _, e := range events {
		if e.EventType == "turn_settled" {
			continue
		}
		name := "plot_evidence:" + e.RunID
		if len(result) == 0 || result[len(result)-1].Name != name {
			result = append(result, Section{Name: name})
		}
		group := &result[len(result)-1]
		group.Text += wire.MarshalJSON(e) + "\n"
		group.Sources = append(group.Sources, e.EventID)
	}
	return result
}

func validatePlotResolution(snapshot Snapshot, node plot.Node, output Output, result plotResolution) error {
	if result.Status != "occurred" && result.Status != "deferred" && result.Status != "skipped" {
		return fmt.Errorf("%w: plot_status", ErrGenerationFailed)
	}
	if wire.Clean(result.Content) == "" || len(result.SourceIDs) == 0 || result.Projections == nil || result.DecisionRequests == nil {
		return fmt.Errorf("%w: plot_required_fields", ErrGenerationFailed)
	}
	if result.Ending != "" && (!node.Terminal || result.Status == "deferred") {
		return fmt.Errorf("%w: plot_ending", ErrGenerationFailed)
	}
	if result.Status == "deferred" && (len(result.Projections) > 0 || len(result.DecisionRequests) > 0) {
		return fmt.Errorf("%w: plot_deferred_effects", ErrGenerationFailed)
	}
	known := map[string]bool{"definition:" + snapshot.Plot.Revision + ":" + node.ID: true}
	working := snapshot
	working.Positions, working.States, working.Relationships, working.Items = output.Positions, output.States, output.Relationships, output.Items
	for _, id := range currentFactSources(working) {
		known[id] = true
	}
	_, factEvidence := evaluateFactConditions(snapshot, output, node.Requirements, "")
	for _, id := range factEvidence {
		known[id] = true
	}
	for _, e := range append(append([]wiaworld.Event{}, snapshot.Events...), output.Events...) {
		known[e.EventID] = true
	}
	// Retained views and node results are provided even when their originating
	// event body falls outside the optional history window. Metadata is world-local.
	provided := SceneViewSources(Snapshot{SceneViews: output.SceneViews}, "")
	for _, state := range snapshot.PlotProgress.Nodes {
		provided = append(provided, state.EventID)
	}
	for _, id := range provided {
		if source, ok := snapshot.Sources[id]; ok && source.ID == id {
			known[id] = true
		}
	}
	for _, id := range result.SourceIDs {
		if !known[id] {
			return ErrContextSourceMissing
		}
	}
	seen := map[string]bool{}
	for _, p := range result.Projections {
		if seen[p.Recipient] {
			return fmt.Errorf("%w: plot_projection_audience_duplicate", ErrGenerationFailed)
		}
		if !slices.Contains(node.Audience, p.Recipient) {
			return fmt.Errorf("%w: plot_projection_audience_out_of_scope", ErrGenerationFailed)
		}
		if wire.Clean(p.Content) == "" {
			return fmt.Errorf("%w: plot_projection_content_empty", ErrGenerationFailed)
		}
		if p.Recipient != "player" {
			if _, ok := story.CharacterByID(story.Definition{Characters: snapshot.Characters}, p.Recipient); !ok {
				return fmt.Errorf("%w: plot_unknown_character", ErrGenerationFailed)
			}
		}
		seen[p.Recipient] = true
	}
	wake := map[string]bool{}
	for _, id := range result.DecisionRequests {
		if id == "player" || !seen[id] || wake[id] {
			return fmt.Errorf("%w: plot_decision_recipient", ErrGenerationFailed)
		}
		wake[id] = true
	}
	return nil
}

func (s *Service) respondToPlot(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, rootID string, resolution plotResolution, output *Output) ([]wiaworld.Event, error) {
	base := snapshot
	base.speechRoster = append([]string{}, output.SceneCharacters...)
	base.Summary.Clock = output.Clock
	base.SceneViews, base.SceneVersion = output.SceneViews, output.SceneVersion
	base.Positions = clonePositions(output.Positions)
	base.States = cloneStates(output.States)
	base.Relationships = slices.Clone(output.Relationships)
	base.Items = cloneItems(output.Items)
	base.OpenProgress = cloneOpenProgress(output.OpenProgress)
	base.AppliedRelationshipSources = map[string]bool{}
	for key, applied := range snapshot.AppliedRelationshipSources {
		base.AppliedRelationshipSources[key] = applied
	}
	for _, change := range output.RelationshipChanges {
		key := change.ProposalSourceID + "\x00" + change.After.SubjectID + "\x00" + change.After.TargetID + "\x00" + change.After.RelationType
		base.AppliedRelationshipSources[key] = true
	}
	base.Characters = append([]wiaworld.Character{}, snapshot.Characters...)
	base.Perceptions = map[string][]wiaworld.Perception{}
	base.Sources = map[string]SourceMetadata{}
	for id, source := range snapshot.Sources {
		base.Sources[id] = source
	}
	for _, e := range output.Events {
		base.Sources[e.EventID] = SourceMetadata{ID: e.EventID, Actor: e.ActorID, Kind: e.EventType, RunID: e.RunID, Stage: e.Stage, SceneVersion: e.SceneVersion}
	}
	for id, items := range snapshot.Perceptions {
		base.Perceptions[id] = append([]wiaworld.Perception{}, items...)
	}
	mergePerceptions(&base, output.Perceptions)
	for _, e := range output.Events {
		if e.EventType == "npc_dialogue" {
			mergePerceptions(&base, []wiaworld.Perception{{RecipientID: e.ActorID, SourceEventID: e.EventID, SourceType: "own_speech", Content: e.Content, Stage: e.Stage, SceneVersion: e.SceneVersion}})
		}
	}
	inputs := map[string]StageInput{}
	for i, p := range resolution.Projections {
		if slices.Contains(resolution.DecisionRequests, p.Recipient) {
			inputs[p.Recipient] = StageInput{NewStimulus: p.Content, SourceEventIDs: []string{fmt.Sprintf("%s:projection:%d", rootID, i)}}
		}
	}
	// Inputs, not global presence, select this bounded response round. Characters
	// outside the player's scene receive only their own projection and history.
	for i := range base.Characters {
		base.Characters[i].InScene = inputs[base.Characters[i].EntityID].NewStimulus != ""
	}
	decisions := map[string]NPCDecision{}
	if err := s.decideNPCs(ctx, generator, base, definitionFor(&base), run, "", "world_event", inputs, nil, decisions, 5); err != nil {
		return nil, err
	}
	extra := Output{Clock: output.Clock, OpenProgress: cloneOpenProgress(output.OpenProgress), SceneVersion: output.SceneVersion, Positions: clonePositions(base.Positions), States: cloneStates(base.States), StateChanges: slices.Clone(output.StateChanges), Relationships: slices.Clone(base.Relationships), RelationshipChanges: slices.Clone(output.RelationshipChanges), Items: cloneItems(base.Items), ItemTransfers: slices.Clone(output.ItemTransfers), Decisions: decisions}
	allowed := map[string][]string{}
	var visible []wiaworld.Event
	for _, c := range snapshot.Characters {
		d, ok := decisions[c.EntityID]
		if !ok {
			continue
		}
		observers := []wiaworld.Character{c}
		audience := []string{c.EntityID}
		if snapshot.Definition.Capabilities["spatial"] == 1 {
			observers = nil
			audience = nil
			for _, other := range snapshot.Characters {
				if currentlyCoLocated(base, c.EntityID, other.EntityID) {
					observers = append(observers, other)
					audience = append(audience, other.EntityID)
				}
			}
			if currentlyCoLocated(base, c.EntityID, "player") {
				audience = append(audience, "player")
			}
		} else if slices.Contains(output.SceneCharacters, c.EntityID) {
			observers = nil
			for _, other := range snapshot.Characters {
				if slices.Contains(output.SceneCharacters, other.EntityID) {
					observers = append(observers, other)
				}
			}
			audience = append([]string{"player"}, wiaworld.CharacterIDs(observers)...)
		}
		start := len(extra.Events)
		if snapshot.Definition.Capabilities["spatial"] == 1 {
			for _, bystander := range snapshot.Definition.BystanderRefs {
				if currentlyCoLocated(base, c.EntityID, bystander.BystanderID) {
					audience = append(audience, bystander.BystanderID)
				}
			}
		}
		extra.speechAudience = audience
		appendNPCDecisionOutput(&extra, run, c, d, observers, inputs[c.EntityID].SourceEventIDs[0], output.SceneVersion, 5)

		for _, e := range extra.Events[start:] {
			if e.EventType == "npc_dialogue" && e.TargetID == "player" && slices.Contains(audience, "player") {
				visible = append(visible, e)
			}
			if e.EventType == "npc_action_intent" {
				allowed[e.EventID] = audience
			}
		}
	}
	needsCoordination := len(allowed) > 0
	for _, d := range decisions {
		needsCoordination = needsCoordination || len(d.RelationshipProposals) > 0
	}
	if needsCoordination {
		remainingMinutes := 120
		if snapshot.Definition.Progression != nil {
			remainingMinutes = max(0, 120-output.elapsedMinutes)
		}
		material := composePlotActions(base, *output, extra, rootID, allowed, decisions, remainingMinutes)
		call := s.generator(generator, material, base, run, "plot_actions", "coordinator", 6, "story.plot-actions.v10")
		var resolved struct {
			TimeMinutes         int                  `json:"time_minutes,omitempty"`
			Outcomes            []plotActionResult   `json:"outcomes"`
			SceneUpdates        []sceneUpdate        `json:"scene_updates"`
			StateEffects        []stateEffect        `json:"state_effects,omitempty"`
			RelationshipEffects []relationshipEffect `json:"relationship_effects,omitempty"`
			ItemTransfers       []itemTransferEffect `json:"item_transfers,omitempty"`
			Movements           []movementResult     `json:"movements,omitempty"`
		}
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		required := []string{"outcomes", "scene_updates"}
		if snapshot.Definition.Progression != nil {
			required = append(required, "time_minutes")
		}
		required = append(required, coordinationCapabilityFields(base)...)
		err := GenerateJSON(callCtx, call, material.System, material.Required, &resolved, structuredTurnOutputTokens, required...)
		cancel()
		if err != nil {
			return nil, err
		}
		if snapshot.Definition.Progression != nil && (resolved.TimeMinutes < 0 || resolved.TimeMinutes > remainingMinutes || len(resolved.Movements) > 0 && resolved.TimeMinutes == 0) {
			return nil, coordinationInvalid("world_action_time_invalid", "time_minutes", "bounded-actual-time-with-positive-movement-cost")
		}
		if snapshot.Definition.Capabilities["spatial"] == 1 {
			for _, outcome := range resolved.Outcomes {
				if outcome.ActorInScene != nil {
					return nil, coordinationInvalid("spatial_plot_presence_unsupported", "outcomes.actor_in_scene", "omitted-when-authoritative-movements-are-enabled")
				}
			}
		}
		outcomes := make([]hostActionResult, 0, len(resolved.Outcomes))
		for _, o := range resolved.Outcomes {
			if snapshot.Definition.Progression != nil && o.Projections == nil {
				return nil, coordinationInvalid("action_projections_required", "outcomes.projections", "explicit-personal-projections")
			}
			outcomes = append(outcomes, o.hostActionResult)
		}
		finalCharacters := append([]string{}, output.SceneCharacters...)
		movementHost := hostResult{Outcomes: outcomes, Movements: resolved.Movements, SceneUpdates: resolved.SceneUpdates}
		if snapshot.Definition.Capabilities["spatial"] == 1 {
			positions, changes, derived, moveErr := applyMovements(base, extra.Events, movementHost)
			if moveErr != nil {
				return nil, moveErr
			}
			if err = validateMovementSceneUpdates(movementHost); err != nil {
				return nil, err
			}
			if err = validateSpatialOutcomeAudiences(base, positions, extra.Events, movementHost); err != nil {
				return nil, err
			}
			extra.Positions, extra.PositionChanges = positions, changes
			finalCharacters = derived
		} else {
			finalCharacters, err = plotActionPresence(output.SceneCharacters, extra.Events, resolved.Outcomes)
			if err != nil {
				return nil, err
			}
		}
		if err = validateSceneCharacters(finalCharacters, snapshot.Characters); err != nil {
			return nil, err
		}
		for _, outcome := range resolved.Outcomes {
			if snapshot.Definition.Capabilities["spatial"] == 1 {
				continue
			}
			for _, id := range outcome.Recipients {
				if !slices.Contains(allowed[outcome.ActionID], id) {
					if s.deps.Logger != nil {
						s.deps.Logger.Printf("story plot_actions validation failed: run_id=%q boundary=outcome_audience", run.RunID)
					}
					return nil, ErrGenerationFailed
				}
			}
		}
		results, err := appendHostOutcomes(&extra, run, snapshot.Characters, snapshot.Definition.BystanderRefs, outcomes)
		if err != nil {
			if s.deps.Logger != nil {
				s.deps.Logger.Printf("story plot_actions validation failed: run_id=%q boundary=action_correspondence", run.RunID)
			}
			return nil, err
		}
		for i := range results {
			results[i].Stage = 6
		}
		visible = append(visible, results...)
		for i := range extra.Events {
			if extra.Events[i].EventType == "npc_action_result" || extra.Events[i].EventType == "action_perceived" {
				extra.Events[i].Stage = 6
			}
		}
		for i := range extra.Perceptions {
			if extra.Perceptions[i].Stage == 3 {
				extra.Perceptions[i].Stage = 6
			}
		}
		if err = applyMechanicEffects(base, &extra, hostResult{Outcomes: outcomes, Movements: resolved.Movements, StateEffects: resolved.StateEffects, RelationshipEffects: resolved.RelationshipEffects, ItemTransfers: resolved.ItemTransfers}); err != nil {
			return nil, err
		}
		sources := plotSceneSources(*output, nil)
		for i, outcome := range resolved.Outcomes {
			action, _ := EventByID(extra.Events, outcome.ActionID)
			sources[outcome.ActionID] = projectionSource(action, outcome.hostActionResult, i)
		}
		version := output.SceneVersion
		if err := applyPlotSceneUpdates(output, sources, resolved.SceneUpdates); err != nil {
			if s.deps.Logger != nil {
				s.deps.Logger.Printf("story plot_actions validation failed: run_id=%q boundary=scene_sources detail=%q", run.RunID, err.Error())
			}
			return nil, err
		}
		if !slices.Equal(output.SceneCharacters, finalCharacters) && output.SceneVersion == version {
			output.SceneVersion++
		}
		output.SceneCharacters = finalCharacters
		if snapshot.Definition.Progression != nil && resolved.TimeMinutes > 0 {
			output.elapsedMinutes += resolved.TimeMinutes
			output.Clock = AdvanceClock(output.Clock, resolved.TimeMinutes)
			elapsed := wiaworld.Event{EventID: rootID + ":action-clock", EventType: "time_advanced", ActorID: "world", Content: fmt.Sprintf("随后实际经过%d分钟，当前游戏时间为%s。", resolved.TimeMinutes, output.Clock), RunID: run.RunID, Stage: 6, SceneVersion: output.SceneVersion, SourceType: "world_clock", CreatedAt: time.Now().UTC()}
			extra.Events = append(extra.Events, elapsed)
			visible = append(visible, elapsed)
		}
	}
	output.Events = append(output.Events, extra.Events...)
	output.OpenProgress = extra.OpenProgress
	output.Perceptions = append(output.Perceptions, extra.Perceptions...)
	output.Memories = append(output.Memories, extra.Memories...)
	output.States, output.Relationships, output.Items = extra.States, extra.Relationships, extra.Items
	output.StateChanges = extra.StateChanges
	output.RelationshipChanges = extra.RelationshipChanges
	output.ItemTransfers = extra.ItemTransfers
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		output.Positions = clonePositions(extra.Positions)
		output.PositionChanges = append(output.PositionChanges, extra.PositionChanges...)
	}
	return visible, nil
}

func composePlotActions(snapshot Snapshot, output Output, extra Output, rootID string, allowed map[string][]string, decisions map[string]NPCDecision, remainingMinutes int) Material {
	audienceContract := "recipients只能取对应允许集合；行动者自动获知。"
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		audienceContract = "recipients按本行动执行前同场和有效移动后的到达同场确定；行动者自动获知。"
	}
	material := Material{System: BehaviorContract + "\n你是世界剧情行动协调器。只裁定人物本人提交的新行动，不代作新的NPC或玩家选择。",
		Required: fmt.Sprintf("本次世界刺激的作者结果见已确认记录中的来源：%s；各人物获知的部分见对应plot_perceived记录。\n本轮此前已确认结果：%s\n待处理NPC记录：%s\n返回JSON对象。outcomes数组项含action_id/status/content/recipients/bystanders/projections；scene_updates遵守下方场景来源合同，其他启用字段遵守下方能力与时间合同。每个npc_action_intent一一对应，status只能succeeded/failed/partial/not_executed。已经完成的相同行动可not_executed，不把计划当成功。%s场外行动不广播。", rootID, worldProgressionRecords(output.Events), worldProgressionRecords(extra.Events), audienceContract), RequiredSources: append(EventIDs(output.Events), EventIDs(extra.Events)...)}
	material.Required += personalProjectionContract
	material.Required += plotActionSceneContract(output, allowed, snapshot.Definition.Capabilities["spatial"] == 1)
	material.Required += "\n当前实际在场人物：" + wire.MarshalJSON(output.SceneCharacters)
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		material.Required += "\n上方每个行动的允许集合描述执行前的同场人物。移动结果的 recipients 还可以包含该行动者实际抵达终点时的同场人物；根据当前位置和本轮有效 movements 推导，不能把其他行动者的移动或整轮最终名单当成本行动的见证资格。"
		material.Required += "\n本世界的位置只由结构化移动改变。movements 必须是数组；实际移动只含 entity_id、from、to、route、action_id，route 是完整有向地点 ID 序列，action_id 必须是该人物本人的成功或部分成功行动。每个移动者必须有以 action_id 为来源并交给本人的 scene_update。没有移动返回[]。outcome 必须省略 actor_in_scene；程序从权威位置派生玩家同场名单和感知资格。"
	} else {
		material.Required += "\n每个outcome可附actor_in_scene布尔值，仅当本行动成功或部分成功地改变行动者本人进场/离场时填写；无位置变化省略。入场结果可以让player及最终在场人物感知，入场前的场外对白、经历仍限原范围。离场不改变此前对白的听众。scene_updates同步描述对应接收者可见的进场或离场结果。"
	}
	material.Required += coordinationCapabilityContext(snapshot, decisions)
	if snapshot.Definition.Progression != nil {
		material.Required += fmt.Sprintf("\n本轮剩余游戏时间预算：%d分钟。time_minutes为本批新行动实际经过的分钟数，范围0..%d；移动需要正数耗时，依据路线与方式判断。预算不足时not_executed或partial，只提交已经完成的路线段，不将计划写成瞬间抵达。没有新行动或移动可以为0。", remainingMinutes, remainingMinutes)
	}
	return material
}

func plotActionSceneContract(output Output, allowed map[string][]string, spatial bool) string {
	sources := map[string]sceneSource{}
	for _, v := range output.SceneViews {
		id := "view:" + v.Recipient
		sources[id] = sceneSource{ID: id, Content: v.Content, Recipients: []string{v.Recipient}}
	}
	label := "\n本轮行动引用及接收者上限（以最终outcome实际范围为准）："
	if spatial {
		label = "\n本轮行动引用及执行前同场人物（移动后的到达见证人按实际位置推导）："
	}
	return "\nscene_updates 为数组，每人至多一项，每项只含content字符串、source_ids字符串数组、recipients字符串数组。content是此人行动后的完整简明情境。可引用的旧情境仅限下面目录的id，目录中每个view只属于它自己的recipient；其他view不能联合引用，也不能引用作者节点ID、目录外历史event_id或自行构造result ID。新结果仅以本次outcomes的action_id为引用，接收者必须实际列在该outcome.recipients中或为行动者。分别为每个人组织自己的更新，无变化返回[]。\n旧情境来源目录：" + wire.MarshalJSON(sources) + label + wire.MarshalJSON(allowed)
}
