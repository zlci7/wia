package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// Opportunities reference resolved actions, never literary additions or reads.
type eventOpportunity struct {
	Kind     string `json:"kind"`
	Location string `json:"location"`
	ActionID string `json:"action_id"`
}

type eventCandidate struct {
	Condition    string         `json:"condition"`
	Development  string         `json:"development"`
	AfterMinutes int            `json:"after_minutes"`
	Initial      plotResolution `json:"initial"`
}

func validateEventPolicy(p *plot.EventGenerationPolicy, def story.Definition) error {
	if p == nil {
		return nil
	}
	if def.Summary.Mode != "open" || wire.Clean(p.Scope) == "" || p.MaxActive < 1 || p.MaxActive > 3 || p.CooldownTurns < 2 || p.CooldownTurns > 20 || len(p.Locations) == 0 {
		return fmt.Errorf("story.json: invalid event_generation policy")
	}
	seen := map[string]bool{}
	for _, id := range p.Locations {
		if seen[id] || !slices.ContainsFunc(def.Locations, func(l story.Location) bool { return l.ID == id }) {
			return fmt.Errorf("story.json: invalid event_generation location")
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, id := range p.Participants {
		if _, ok := turn.CharacterByID(def, id); !ok || seen[id] {
			return fmt.Errorf("story.json: invalid event_generation participant")
		}
		seen[id] = true
	}
	return nil
}

func readGeneratedEvents(ctx context.Context, store *storage.WorldStore, def story.Definition) (turn.GeneratedEventState, error) {
	s := turn.GeneratedEventState{Active: []turn.GeneratedEvent{}}
	raw, err := store.MetaGet(ctx, "generated_events")
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if json.Unmarshal([]byte(raw), &s) != nil || def.EventGeneration == nil || s.LastOfferTurn < 0 || s.Completed < 0 || len(s.Active) > def.EventGeneration.MaxActive {
		return s, ErrStorageUnavailable
	}
	seen := map[string]bool{}
	for _, e := range s.Active {
		if seen[e.Node.ID] || e.StartID == "" || e.TriggerID == "" || e.Premise == "" || !slices.Contains(def.EventGeneration.Locations, e.Location) || e.Node.Terminal || len(e.Node.After) != 0 {
			return s, ErrStorageUnavailable
		}
		seen[e.Node.ID] = true
		if err := plot.ValidateDefinition(plot.Definition{Revision: "generated.v1", Nodes: []plot.Node{e.Node}}); err != nil {
			return s, err
		}
		if e.State.Status != "" && e.State.Status != "deferred" {
			return s, ErrStorageUnavailable
		}
		for _, id := range e.Node.Audience {
			if id != "player" && !slices.Contains(def.EventGeneration.Participants, id) {
				return s, ErrStorageUnavailable
			}
		}
		for _, id := range []string{e.StartID, e.TriggerID} {
			exists, err := store.EventExists(ctx, id)
			if err != nil {
				return s, err
			}
			if !exists {
				return s, ErrContextSourceMissing
			}
		}
	}
	return s, nil
}

func (a *App) advanceGeneratedEvents(ctx context.Context, generator model.TextGenerator, snapshot turn.Snapshot, run wiaworld.Run, opportunity *eventOpportunity, output *turn.Output) ([]wiaworld.Event, error) {
	p := snapshot.Definition.EventGeneration
	if p == nil {
		return nil, nil
	}
	state := snapshot.GeneratedEvents
	state.Active = append([]turn.GeneratedEvent{}, state.Active...)
	output.GeneratedEvents = &state
	current, err := plot.ClockMinute(output.Clock)
	if err != nil {
		return nil, err
	}
	// An authored evaluation consumes the round even when it defers without an event.
	if _, due, ok := turn.NextPlotNode(snapshot); ok && current >= due {
		return nil, nil
	}
	if index, due, ok := turn.NextGeneratedEvent(snapshot); ok && current >= due {
		e := state.Active[index]
		base := snapshot
		base.Plot = &plot.Definition{Revision: "generated.v1", Facts: snapshot.Definition.Secret + "\n本事件已成立起点：" + e.Premise, Nodes: []plot.Node{e.Node}}
		base.PlotProgress = plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{}}
		if e.State.Status != "" {
			base.PlotProgress.Nodes[e.Node.ID] = e.State
		}
		authoredProgress := output.PlotProgress
		visible, err := a.advancePlot(ctx, generator, base, run, output)
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
		return nil, fmt.Errorf("%w: event_opportunity_scope", turn.ErrGenerationFailed)
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
	state.LastOfferTurn = nextTurn
	material := turn.Material{
		System:          turn.BehaviorContract + "\n你是开放世界事件协调器。在作者范围内，依据已确认的新情境选择是否发生一个小型外部事件。沿用现有事件优先；没有合适事件时返回空候选。重要NPC的新决定只由本人作出，玩家不自动接受任务。只返回JSON。",
		Required:        fmt.Sprintf("世界规则：%s\n作者事实：%s\n生成范围：%s\n地点目录：%s\n机会：%s\n已确认触发结果：%s\n游戏内时间：%s\n在场人物：%s\n接收者场景：%s\n当前已有事件：%s\n作者剧情：%s\n返回candidates数组，0或1项。每项condition(何时可收束)、development(有干预与不参与时的后续可能及结束条件)、after_minutes(1到120)、initial对象。initial字段status必须occurred，content为本次外部事实，source_ids只能引用触发结果ID，projections按接收者给出content及可选scene，decision_requests只含本次确实得到新刺激且需要本人决定的NPC，ending为空。不得新增重要NPC、地图地点、强迫玩家承诺、覆写作者主线、把计划写成已发生事实。后续发展须可通过等待、参与或拒绝自然结算。只向真实目击或有来源获知者投影，不广播作者秘密。参与者上限=%s加player。背景人物可有符合设定的日常反应。所有数组使用[]，不使用null。", snapshot.Definition.Rules, snapshot.Definition.Secret, p.Scope, wire.MarshalJSON(snapshot.Definition.Locations), wire.MarshalJSON(opportunity), wire.MarshalJSON(trigger), output.Clock, wire.MarshalJSON(output.SceneCharacters), wire.MarshalJSON(output.SceneViews), wire.MarshalJSON(state.Active), wire.MarshalJSON(snapshot.Plot), wire.MarshalJSON(p.Participants)),
		RequiredSources: []string{trigger.EventID}, Optional: plotEvidenceSections(snapshot.Events),
	}
	call := a.contextGenerator(generator, material, snapshot, run, "event_generation", "coordinator", 4, "story.events.v1")
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var response struct {
		Candidates []eventCandidate `json:"candidates"`
	}
	if err = turn.GenerateJSON(callCtx, call, material.System, material.Required, &response, structuredTurnOutputTokens, "candidates"); err != nil {
		return nil, err
	}
	if response.Candidates == nil || len(response.Candidates) > 1 {
		return nil, fmt.Errorf("%w: event_candidate_count", turn.ErrGenerationFailed)
	}
	if len(response.Candidates) == 0 {
		return nil, nil
	}
	c := response.Candidates[0]
	if wire.Clean(c.Condition) == "" || wire.Clean(c.Development) == "" || len([]rune(c.Initial.Content+c.Condition+c.Development)) > 4000 || c.AfterMinutes < 1 || c.AfterMinutes > 120 || c.Initial.Status != "occurred" || c.Initial.Ending != "" || len(c.Initial.SourceIDs) != 1 || c.Initial.SourceIDs[0] != trigger.EventID {
		return nil, fmt.Errorf("%w: event_candidate_fields", turn.ErrGenerationFailed)
	}
	key := run.InputID
	if key == "" {
		key = run.RunID
	}
	root := "generated:" + key
	node := plot.Node{ID: key, AtMinute: current + c.AfterMinutes, After: []string{}, Condition: c.Condition, Development: c.Development, Audience: append([]string{"player"}, p.Participants...)}
	base := snapshot
	base.Plot = &plot.Definition{Revision: "generated.v1"}
	if err = validatePlotResolution(base, node, *output, c.Initial); err != nil {
		return nil, err
	}
	visible, err := a.publishPlotResolution(ctx, generator, snapshot, run, root, c.Initial, output)
	if err != nil {
		return nil, err
	}
	for i := range output.Events {
		if output.Events[i].EventID == root {
			output.Events[i].ProjectionParentID = trigger.EventID
		}
	}
	state.Active = append(state.Active, turn.GeneratedEvent{Node: node, StartID: root, TriggerID: trigger.EventID, Location: opportunity.Location, Premise: c.Initial.Content})
	// The author plan is archived with the same transaction, never projected as facts.
	output.Events = append(output.Events, wiaworld.Event{EventID: root + ":plan", EventType: "generated_event_plan", ActorID: "world", Content: wire.MarshalJSON(state.Active[len(state.Active)-1]), RunID: run.RunID, Stage: 4, SceneVersion: output.SceneVersion, SourceType: "author_plan", ProjectionParentID: root, CreatedAt: time.Now().UTC()})
	return visible, nil
}
