package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"gameagent/backend/internal/model"
)

// Plot content is frozen with each world. Conditions are narrative material,
// while dependencies, time boundaries, publication and audience are code contracts.
type PlotDefinition struct {
	Revision string     `json:"revision"`
	Facts    string     `json:"facts"`
	Nodes    []PlotNode `json:"nodes"`
}

type PlotNode struct {
	ID          string   `json:"id"`
	After       []string `json:"after"`
	AtMinute    int      `json:"at_minute"`
	Condition   string   `json:"condition"`
	Development string   `json:"development"`
	Audience    []string `json:"audience"`
	Terminal    bool     `json:"terminal"`
}

type PlotNodeState struct {
	Status    string   `json:"status"`
	EventID   string   `json:"event_id,omitempty"`
	Content   string   `json:"content,omitempty"`
	NextCheck int      `json:"next_check"`
	Evidence  []string `json:"evidence"`
}

type PlotProgress struct {
	Version int64                    `json:"version"`
	Nodes   map[string]PlotNodeState `json:"nodes"`
	Ending  string                   `json:"ending,omitempty"`
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

func plotActionPresence(current []string, events []Event, outcomes []plotActionResult) ([]string, error) {
	ids := append([]string{}, current...)
	seen := map[string]bool{}
	for _, o := range outcomes {
		if o.ActorInScene == nil {
			continue
		}
		e, ok := eventByID(events, o.ActionID)
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

func clockMinute(clock string) (int, error) {
	var day, hour, minute int
	if n, err := fmt.Sscanf(clock, "第 %d 日 %d:%d", &day, &hour, &minute); err != nil || n != 3 || day < 1 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("%w: invalid world clock", ErrStorageUnavailable)
	}
	return (day-1)*1440 + hour*60 + minute, nil
}

func readPlot(ctx context.Context, db *sql.DB) (*PlotDefinition, PlotProgress, error) {
	var def PlotDefinition
	var state PlotProgress
	raw, err := metaGet(ctx, db, "plot_definition")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, state, nil
	}
	if err != nil {
		return nil, state, err
	}
	if err = json.Unmarshal([]byte(raw), &def); err != nil {
		return nil, state, err
	}
	raw, err = metaGet(ctx, db, "plot_progress")
	if err != nil {
		return nil, state, err
	}
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, state, err
	}
	if err = validatePlot(def, state); err != nil {
		return nil, state, err
	}
	return &def, state, nil
}

func validatePlot(def PlotDefinition, state PlotProgress) error {
	if def.Revision == "" || len(def.Nodes) == 0 || len(def.Nodes) > 32 || state.Version < 1 || state.Nodes == nil {
		return ErrStorageUnavailable
	}
	known := map[string]bool{}
	for _, n := range def.Nodes {
		if n.ID == "" || known[n.ID] || n.AtMinute < 0 || n.Condition == "" || n.Development == "" || n.Audience == nil {
			return ErrStorageUnavailable
		}
		for _, dep := range n.After {
			if !known[dep] {
				return ErrStorageUnavailable
			}
		}
		known[n.ID] = true
	}
	for id, n := range state.Nodes {
		if !known[id] || (n.Status != "occurred" && n.Status != "deferred" && n.Status != "skipped") || (n.Status != "deferred" && n.EventID == "") {
			return ErrStorageUnavailable
		}
	}
	return nil
}

func nextPlotNode(snapshot worldSnapshot) (PlotNode, int, bool) {
	if snapshot.Plot == nil || (snapshot.Summary.Mode == "guided" && snapshot.PlotProgress.Ending != "") {
		return PlotNode{}, 0, false
	}
	var chosen PlotNode
	var due int
	found := false
	for _, node := range snapshot.Plot.Nodes {
		state := snapshot.PlotProgress.Nodes[node.ID]
		if state.Status == "occurred" || state.Status == "skipped" {
			continue
		}
		eligible := true
		for _, dep := range node.After {
			s := snapshot.PlotProgress.Nodes[dep].Status
			if s != "occurred" && s != "skipped" {
				eligible = false
			}
		}
		at := max(node.AtMinute, state.NextCheck)
		if eligible && (!found || at < due) {
			chosen, due, found = node, at, true
		}
	}
	return chosen, due, found
}

func plotTimeLimit(snapshot worldSnapshot) int {
	_, due, ok := nextPlotNode(snapshot)
	if _, eventDue, found := nextGeneratedEvent(snapshot); found && (!ok || eventDue < due) {
		due, ok = eventDue, true
	}
	if !ok {
		return 120
	}
	current, err := clockMinute(snapshot.Summary.Clock)
	if err != nil {
		return 0
	}
	return min(120, max(0, due-current))
}

func plotContext(snapshot worldSnapshot) string {
	if snapshot.Plot == nil && len(snapshot.GeneratedEvents.Active) == 0 {
		return ""
	}
	if snapshot.Plot == nil {
		return fmt.Sprintf("\n世界剧情时间边界：本轮 time_minutes 最大为 %d。开放事件的 node 是未来计划，premise 是已成立起点：%s。当前只裁定本轮行动，不提前展开未来事件。", plotTimeLimit(snapshot), marshalJSON(snapshot.GeneratedEvents.Active))
	}
	return fmt.Sprintf("\n世界剧情时间边界：本轮 time_minutes 最大为 %d。长时间行动或等待先停在下一剧情节点，不宣称剩余等待已经完成；遇到主角关键选择即停下。模式=%s。未来节点由后续剧情协调处理，本次只裁定已经提交的行动，不展开未来剧情。作者固定资料（并非人物共有知识）：%s\n已提交剧情进度：%s\n输出简明状态与结果，不复述输入、来源全文或剧情计划。scene只写简短结束情境；每个outcome用一两句写清结果，scene_updates仅更新确有变化的接收者，每项简明保留其当前状态。", plotTimeLimit(snapshot), snapshot.Summary.Mode, snapshot.Plot.Facts, marshalJSON(snapshot.PlotProgress))
}

// A single node is settled per turn. The clock stops at that node; a subsequent
// input can continue waiting against the newly committed consequences.
func (a *App) advancePlot(ctx context.Context, generator model.TextGenerator, snapshot worldSnapshot, run Run, output *turnOutput) ([]Event, error) {
	if snapshot.Plot == nil {
		return nil, nil
	}
	output.PlotProgress = &PlotProgress{Version: snapshot.PlotProgress.Version, Ending: snapshot.PlotProgress.Ending, Nodes: map[string]PlotNodeState{}}
	for id, state := range snapshot.PlotProgress.Nodes {
		output.PlotProgress.Nodes[id] = state
	}
	node, due, ok := nextPlotNode(snapshot)
	current, err := clockMinute(output.Clock)
	if err != nil {
		return nil, err
	}
	if !ok || current < due {
		return nil, nil
	}
	material := composePlot(snapshot, run, node, output)
	call := a.contextGenerator(generator, material, snapshot, run, "plot", "coordinator", 4, "story.plot.v3")
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var result plotResolution
	if err = generateJSON(callCtx, call, material.System, material.Required, &result, structuredTurnOutputTokens, "status", "content", "source_ids", "projections", "decision_requests", "ending"); err != nil {
		return nil, err
	}
	if err = validatePlotResolution(snapshot, node, *output, result); err != nil {
		if a.logger != nil {
			a.logger.Printf("story plot validation failed: world_id=%q run_id=%q error_code=%q boundary=%q", snapshot.Summary.WorldID, run.RunID, safeTurnErrorCode(err), err.Error())
		}
		return nil, err
	}
	state := PlotNodeState{Status: result.Status, Content: result.Content, NextCheck: current + 1, Evidence: result.SourceIDs}
	if result.Status != "deferred" {
		state.EventID = "plot:" + snapshot.Plot.Revision + ":" + node.ID
	}
	visible, err := a.publishPlotResolution(ctx, generator, snapshot, run, state.EventID, result, output)
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

func (a *App) publishPlotResolution(ctx context.Context, generator model.TextGenerator, snapshot worldSnapshot, run Run, rootID string, result plotResolution, output *turnOutput) ([]Event, error) {
	var visible []Event
	if result.Status != "deferred" {
		event := Event{EventID: rootID, EventType: "plot_result", ActorID: "world", Content: result.Content, RunID: run.RunID, Stage: 4, SceneVersion: output.SceneVersion, SourceType: "plot_" + result.Status, CreatedAt: time.Now().UTC()}
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
			output.Perceptions = append(output.Perceptions, Perception{RecipientID: p.Recipient, SourceEventID: projection.EventID, SourceType: "plot_observed", Content: p.Content, Stage: 4, SceneVersion: output.SceneVersion, CreatedAt: event.CreatedAt})
			if p.Recipient == "player" {
				visible = append(visible, projection)
			}
		}
	}
	sources := plotSceneSources(*output, visible)
	updates := []sceneUpdate{}
	for i, p := range result.Projections {
		id := fmt.Sprintf("%s:projection:%d", rootID, i)
		if cleanText(p.Scene) != "" {
			updates = append(updates, sceneUpdate{Content: p.Scene, SourceIDs: []string{"view:" + p.Recipient, id}, Recipients: []string{p.Recipient}})
		}
	}
	if err := applyPlotSceneUpdates(output, sources, updates); err != nil {
		if a.logger != nil {
			a.logger.Printf("story plot scene rejected: world_id=%q run_id=%q constraint=%q", snapshot.Summary.WorldID, run.RunID, err.Error())
		}
		return nil, err
	}
	if len(result.DecisionRequests) > 0 {
		npcVisible, err := a.respondToPlot(ctx, generator, snapshot, run, rootID, result, output)
		if err != nil {
			return nil, err
		}
		visible = append(visible, npcVisible...)
	}
	return visible, nil
}

func composePlot(snapshot worldSnapshot, run Run, node PlotNode, output *turnOutput) contextMaterial {
	snapshot.SceneViews = output.SceneViews
	snapshot.Characters = append([]Character{}, snapshot.Characters...)
	for i := range snapshot.Characters {
		snapshot.Characters[i].InScene = slices.Contains(output.SceneCharacters, snapshot.Characters[i].EntityID)
	}
	material := contextMaterial{
		System:          behaviorContract + "\n你是世界剧情协调器。按当前世界时间、已发生的结果和作者剧情约束处理一个节点。玩家表达是尝试，NPC对白是声称，文学补写不属于事实。不得替重要NPC产生新决定，需要本人决定时在 decision_requests 列出其ID，并先给该人物一个真实且获准的新刺激。先公布外部情境，不提前写成该人物已经选择或完成行动。只返回JSON。",
		Required:        fmt.Sprintf("模式：%s\n游戏内时间：%s\n固定事实：%s\n当前节点：%s\n已提交进度：%s\n人物在场情况：%s\n分接收者场景：%s\n本轮已确认记录：%s\n输出字段：status(occurred/deferred/skipped)、content(作者层真实结果)、source_ids(证据ID数组)、projections(对象数组，每项recipient/content)、decision_requests(字符串数组)、ending(字符串)。证据只能来自提供的事件或 definition:%s:%s；至少一条。条件不足时 deferred、projections=[]、decision_requests=[]、ending=空字符串；skipped 记录确实被干预阻止的发展。projections 仅包含当前节点允许的 audience 中实际观察或经明确来源获知的人物，隐情不随公共迹象广播；场外人物不自动听到场内对白，玩家不自动知道场外结局。ending仅在terminal节点且条件实际成立时填写，拒绝或不参与可以产生相应结果，不伪造玩家同意。无内容的数组使用[]，不得null。", snapshot.Summary.Mode, output.Clock, snapshot.Plot.Facts, marshalJSON(node), marshalJSON(snapshot.PlotProgress), marshalJSON(PublicCharacterViews(snapshot.Characters)), coordinationScene(snapshot), marshalJSON(output.Events), snapshot.Plot.Revision, node.ID),
		RequiredSources: eventIDs(output.Events), Optional: plotEvidenceSections(snapshot.Events),
	}
	material.Required += "\n每个 projection 另可含 scene 字符串：只依据此人的旧视图与本次获准感知，写其事件后的完整简明情境；无状态变化可留空。它只交给对应 recipient，作者真相不进入其中，NPC待决定行动保持未执行。程序绑定该人物和投影来源，无须输出另一个场景更新表。"
	material.Required += "\n接收与唤醒合同：每个 recipient 最多出现一次，只选当前节点 audience 中的ID。decision_requests 只选本次 projections 已提供刺激的重要NPC ID，最多一次；player、背景人物、信使等没有独立Agent的角色不放入 decision_requests。没有符合条件的人物时返回[]。"
	material.Required += "\n保留场景与已提交节点的来源ID也可以引用，但仅用于这些已提供状态，不据ID猜测未提供的原文。definition引用仅限本次当前节点；其他节点的发生依据使用其event_id，不使用其未来计划。"
	material.RequiredSources = append(material.RequiredSources, sceneViewSources(snapshot, "")...)
	for _, state := range snapshot.PlotProgress.Nodes {
		if state.EventID != "" {
			material.RequiredSources = append(material.RequiredSources, state.EventID)
		}
	}
	return material
}

func plotEvidenceSections(events []Event) []contextSection {
	var result []contextSection
	for _, e := range events {
		if e.EventType == "turn_settled" {
			continue
		}
		name := "plot_evidence:" + e.RunID
		if len(result) == 0 || result[len(result)-1].Name != name {
			result = append(result, contextSection{Name: name})
		}
		group := &result[len(result)-1]
		group.Text += marshalJSON(e) + "\n"
		group.Sources = append(group.Sources, e.EventID)
	}
	return result
}

func validatePlotResolution(snapshot worldSnapshot, node PlotNode, output turnOutput, result plotResolution) error {
	if result.Status != "occurred" && result.Status != "deferred" && result.Status != "skipped" {
		return fmt.Errorf("%w: plot_status", ErrGenerationFailed)
	}
	if cleanText(result.Content) == "" || len(result.SourceIDs) == 0 || result.Projections == nil || result.DecisionRequests == nil {
		return fmt.Errorf("%w: plot_required_fields", ErrGenerationFailed)
	}
	if result.Ending != "" && (!node.Terminal || result.Status == "deferred") {
		return fmt.Errorf("%w: plot_ending", ErrGenerationFailed)
	}
	if result.Status == "deferred" && (len(result.Projections) > 0 || len(result.DecisionRequests) > 0) {
		return fmt.Errorf("%w: plot_deferred_effects", ErrGenerationFailed)
	}
	known := map[string]bool{"definition:" + snapshot.Plot.Revision + ":" + node.ID: true}
	for _, e := range append(append([]Event{}, snapshot.Events...), output.Events...) {
		known[e.EventID] = true
	}
	// Retained views and node results are provided even when their originating
	// event body falls outside the optional history window. Metadata is world-local.
	provided := sceneViewSources(worldSnapshot{SceneViews: output.SceneViews}, "")
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
		if seen[p.Recipient] || !slices.Contains(node.Audience, p.Recipient) || cleanText(p.Content) == "" {
			return fmt.Errorf("%w: plot_projection_audience", ErrGenerationFailed)
		}
		if p.Recipient != "player" {
			if _, ok := characterByID(gameDefinition{Characters: snapshot.Characters}, p.Recipient); !ok {
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

func (a *App) respondToPlot(ctx context.Context, generator model.TextGenerator, snapshot worldSnapshot, run Run, rootID string, resolution plotResolution, output *turnOutput) ([]Event, error) {
	base := snapshot
	base.Summary.Clock = output.Clock
	base.SceneViews, base.SceneVersion = output.SceneViews, output.SceneVersion
	base.Characters = append([]Character{}, snapshot.Characters...)
	base.Perceptions = map[string][]Perception{}
	base.Sources = map[string]sourceMetadata{}
	for id, source := range snapshot.Sources {
		base.Sources[id] = source
	}
	for _, e := range output.Events {
		base.Sources[e.EventID] = sourceMetadata{ID: e.EventID, Actor: e.ActorID, Kind: e.EventType, RunID: e.RunID, Stage: e.Stage, SceneVersion: e.SceneVersion}
	}
	for id, items := range snapshot.Perceptions {
		base.Perceptions[id] = append([]Perception{}, items...)
	}
	for _, p := range output.Perceptions {
		base.Perceptions[p.RecipientID] = append(base.Perceptions[p.RecipientID], p)
	}
	for _, e := range output.Events {
		if e.EventType == "npc_dialogue" {
			base.Perceptions[e.ActorID] = append(base.Perceptions[e.ActorID], Perception{RecipientID: e.ActorID, SourceEventID: e.EventID, SourceType: "own_speech", Content: e.Content, Stage: e.Stage, SceneVersion: e.SceneVersion})
		}
	}
	inputs := map[string]npcStageInput{}
	for i, p := range resolution.Projections {
		if slices.Contains(resolution.DecisionRequests, p.Recipient) {
			inputs[p.Recipient] = npcStageInput{NewStimulus: p.Content, SourceEventIDs: []string{fmt.Sprintf("%s:projection:%d", rootID, i)}}
		}
	}
	// Inputs, not global presence, select this bounded response round. Characters
	// outside the player's scene receive only their own projection and history.
	for i := range base.Characters {
		base.Characters[i].InScene = inputs[base.Characters[i].EntityID].NewStimulus != ""
	}
	decisions := map[string]npcDecision{}
	if err := a.decideNPCs(ctx, generator, base, gameDefinition{Characters: snapshot.Characters}, run, "", "world_event", inputs, nil, decisions, 5); err != nil {
		return nil, err
	}
	extra := turnOutput{SceneVersion: output.SceneVersion}
	allowed := map[string][]string{}
	var visible []Event
	for _, c := range snapshot.Characters {
		d, ok := decisions[c.EntityID]
		if !ok {
			continue
		}
		observers := []Character{c}
		audience := []string{c.EntityID}
		if slices.Contains(output.SceneCharacters, c.EntityID) {
			observers = nil
			for _, other := range snapshot.Characters {
				if slices.Contains(output.SceneCharacters, other.EntityID) {
					observers = append(observers, other)
				}
			}
			audience = append([]string{"player"}, characterIDs(observers)...)
		}
		start := len(extra.Events)
		appendNPCDecisionOutput(&extra, run, c, d, observers, inputs[c.EntityID].SourceEventIDs[0], output.SceneVersion, 5)
		if !slices.Contains(audience, "player") {
			for i := start; i < len(extra.Events); i++ {
				if extra.Events[i].EventType == "npc_dialogue" {
					extra.Events[i].SourceType = "offscene_dialogue"
					extra.Events[i].TargetID = c.EntityID
				}
			}
		}
		for _, e := range extra.Events[start:] {
			if e.EventType == "npc_dialogue" && slices.Contains(audience, "player") {
				visible = append(visible, e)
			}
			if e.EventType == "npc_action_intent" {
				allowed[e.EventID] = audience
			}
		}
	}
	if len(allowed) > 0 {
		material := contextMaterial{System: behaviorContract + "\n你是世界剧情行动协调器。只裁定人物本人提交的新行动，不代作新的NPC或玩家选择。",
			Required: fmt.Sprintf("当前节点结果(作者资料)：%s\n本轮此前已确认结果：%s\n待处理NPC记录：%s\n每个行动允许的接收者：%s\n仅返回JSON字段outcomes，数组项含action_id/status/content/recipients。每个npc_action_intent一一对应，status只能succeeded/failed/partial/not_executed。已经完成的相同行动可not_executed，不把计划当成功。recipients只能取对应允许集合；行动者自动获知。场外行动不广播。", marshalJSON(resolution), marshalJSON(output.Events), marshalJSON(extra.Events), marshalJSON(allowed)), RequiredSources: append(eventIDs(output.Events), eventIDs(extra.Events)...)}
		material.Required += plotActionSceneContract(*output, allowed)
		material.Required += "\n当前实际在场人物：" + marshalJSON(output.SceneCharacters) + "\n每个outcome可附actor_in_scene布尔值，仅当本行动成功或部分成功地改变行动者本人进场/离场时填写；无位置变化省略。入场结果可以让player及最终在场人物感知，入场前的场外对白、经历仍限原范围。离场不改变此前对白的听众。scene_updates同步描述对应接收者可见的进场或离场结果。"
		call := a.contextGenerator(generator, material, base, run, "plot_actions", "coordinator", 6, "story.plot-actions.v4")
		var resolved struct {
			Outcomes     []plotActionResult `json:"outcomes"`
			SceneUpdates []sceneUpdate      `json:"scene_updates"`
		}
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := generateJSON(callCtx, call, material.System, material.Required, &resolved, structuredTurnOutputTokens, "outcomes", "scene_updates")
		cancel()
		if err != nil {
			return nil, err
		}
		finalCharacters, err := plotActionPresence(output.SceneCharacters, extra.Events, resolved.Outcomes)
		if err != nil {
			return nil, err
		}
		if err = validateSceneCharacters(finalCharacters, snapshot.Characters); err != nil {
			return nil, err
		}
		outcomes := make([]hostActionResult, 0, len(resolved.Outcomes))
		for _, o := range resolved.Outcomes {
			outcomes = append(outcomes, o.hostActionResult)
			// Only the arrival result may reach the destination audience.
			if o.ActorInScene != nil && *o.ActorInScene {
				allowed[o.ActionID] = append(append(allowed[o.ActionID], "player"), finalCharacters...)
			}
		}
		for _, outcome := range resolved.Outcomes {
			for _, id := range outcome.Recipients {
				if !slices.Contains(allowed[outcome.ActionID], id) {
					if a.logger != nil {
						a.logger.Printf("story plot_actions validation failed: run_id=%q boundary=outcome_audience", run.RunID)
					}
					return nil, ErrGenerationFailed
				}
			}
		}
		results, err := appendHostOutcomes(&extra, run, snapshot.Characters, snapshot.Definition.BystanderRefs, outcomes)
		if err != nil {
			if a.logger != nil {
				a.logger.Printf("story plot_actions validation failed: run_id=%q boundary=action_correspondence", run.RunID)
			}
			return nil, err
		}
		for i := range results {
			results[i].Stage = 6
		}
		visible = append(visible, results...)
		for i := range extra.Events {
			if extra.Events[i].EventType == "npc_action_result" {
				extra.Events[i].Stage = 6
			}
		}
		for i := range extra.Perceptions {
			if extra.Perceptions[i].Stage == 3 {
				extra.Perceptions[i].Stage = 6
			}
		}
		sources := plotSceneSources(*output, nil)
		for i, outcome := range resolved.Outcomes {
			action, _ := eventByID(extra.Events, outcome.ActionID)
			sources[outcome.ActionID] = sceneSource{ID: outcome.ActionID, Content: outcome.Content, Recipients: append(append([]string{}, outcome.Recipients...), action.ActorID), Canonical: []string{fmt.Sprintf("%s:result:%d", outcome.ActionID, i+1)}}
		}
		version := output.SceneVersion
		if err := applyPlotSceneUpdates(output, sources, resolved.SceneUpdates); err != nil {
			if a.logger != nil {
				a.logger.Printf("story plot_actions validation failed: run_id=%q boundary=scene_sources detail=%q", run.RunID, err.Error())
			}
			return nil, err
		}
		if !slices.Equal(output.SceneCharacters, finalCharacters) && output.SceneVersion == version {
			output.SceneVersion++
		}
		output.SceneCharacters = finalCharacters
	}
	output.Events = append(output.Events, extra.Events...)
	output.Perceptions = append(output.Perceptions, extra.Perceptions...)
	output.Memories = append(output.Memories, extra.Memories...)
	return visible, nil
}

func plotActionSceneContract(output turnOutput, allowed map[string][]string) string {
	sources := map[string]sceneSource{}
	for _, v := range output.SceneViews {
		id := "view:" + v.Recipient
		sources[id] = sceneSource{ID: id, Content: v.Content, Recipients: []string{v.Recipient}}
	}
	return "\nscene_updates 为数组，每人至多一项，每项只含content字符串、source_ids字符串数组、recipients字符串数组。content是此人行动后的完整简明情境。可引用的旧情境仅限下面目录的id，目录中每个view只属于它自己的recipient；其他view不能联合引用，也不能引用作者节点ID、目录外历史event_id或自行构造result ID。新结果仅以本次outcomes的action_id为引用，接收者必须实际列在该outcome.recipients中或为行动者。分别为每个人组织自己的更新，无变化返回[]。\n旧情境来源目录：" + marshalJSON(sources) + "\n本轮行动引用及接收者上限（以最终outcome实际范围为准）：" + marshalJSON(allowed)
}
