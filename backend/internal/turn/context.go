package turn

import (
	"fmt"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/wire"
)

// SceneFor is the scene text one recipient last received.
//
// It reads the per-recipient views rather than the world scene, because a character who
// was somewhere else has a different answer to "where are you" than the turn does. A
// recipient with no view of its own is told to rely on what it is permitted to have
// experienced, rather than being handed the world's scene — which would tell them about
// a place they were not.
func SceneFor(snapshot Snapshot, recipient string) string {
	for _, view := range snapshot.SceneViews {
		if view.Recipient == recipient {
			return view.Content
		}
	}
	return "当前情境以本人获准经历为依据。"
}

func NextGeneratedEvent(s Snapshot) (int, int, bool) {
	index, due := -1, 0
	for i, e := range s.GeneratedEvents.Active {
		at := max(e.Node.AtMinute, e.State.NextCheck)
		if index < 0 || at < due {
			index, due = i, at
		}
	}
	return index, due, index >= 0
}

func EventOpportunityContract(s Snapshot) string {
	p := s.Definition.EventGeneration
	if p == nil {
		return ""
	}
	return "\n开放事件机会：本轮确已抵达另一个地点或发生显著场景变化时，可额外返回 event_opportunity 对象，字段 kind(arrival/significant_change)、location(下列允许地点ID)、action_id(本轮造成变化且结果为succeeded或partial的outcome.action_id)。单纯交谈、读表、重复观察、未成功移动和文学补写不构成机会；无机会省略此字段。它只申请一次受限的外部情节生成，不替玩家接受任务。允许地点：" + wire.MarshalJSON(p.Locations)
}

func NextPlotNode(snapshot Snapshot) (plot.Node, int, bool) {
	if snapshot.Plot == nil || (snapshot.Summary.Mode == "guided" && snapshot.PlotProgress.Ending != "") {
		return plot.Node{}, 0, false
	}
	var chosen plot.Node
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

func PlotTimeLimit(snapshot Snapshot) int {
	_, due, ok := NextPlotNode(snapshot)
	if _, eventDue, found := NextGeneratedEvent(snapshot); found && (!ok || eventDue < due) {
		due, ok = eventDue, true
	}
	if !ok {
		return max(0, 120-snapshot.elapsedMinutes)
	}
	current, err := plot.ClockMinute(snapshot.Summary.Clock)
	if err != nil {
		return 0
	}
	return min(max(0, 120-snapshot.elapsedMinutes), max(0, due-current))
}

func PlotContext(snapshot Snapshot) string {
	if snapshot.Definition.Progression != nil {
		return "\n游戏时间合同：本轮最多经过120分钟；按行动实际耗时或明确等待推进。持续发展和计划检查由本轮行动结算后的世界阶段处理。当前阶段只裁定已经提交的行动，不将人物的计划检查时间写成预定成功时间。"
	}
	if snapshot.Plot == nil && len(snapshot.GeneratedEvents.Active) == 0 {
		return ""
	}
	if snapshot.Plot == nil {
		return fmt.Sprintf("\n世界剧情时间边界：本轮 time_minutes 最大为 %d。开放事件的 node 是未来计划，premise 是已成立起点：%s。当前只裁定本轮行动，不提前展开未来事件。", PlotTimeLimit(snapshot), wire.MarshalJSON(snapshot.GeneratedEvents.Active))
	}
	return fmt.Sprintf("\n世界剧情时间边界：本轮 time_minutes 最大为 %d。长时间行动或等待先停在下一剧情节点，不宣称剩余等待已经完成；遇到主角关键选择即停下。模式=%s。未来节点由后续剧情协调处理，本次只裁定已经提交的行动，不展开未来剧情。作者固定资料（并非人物共有知识）：%s\n已提交剧情进度：%s\n输出简明状态与结果，不复述输入、来源全文或剧情计划。scene只写简短结束情境；每个outcome用一两句写清结果，scene_updates仅更新确有变化的接收者，每项简明保留其当前状态。", PlotTimeLimit(snapshot), snapshot.Summary.Mode, snapshot.Plot.Facts, wire.MarshalJSON(snapshot.PlotProgress))
}

// The prompt context of a turn: the fragments a stage adds to tell a model what it needs
// to know about the story line and the events the world opened by itself.
//
// These read the turn's snapshot and nothing else. They are here rather than beside the
// stages that call them because a stage is assembled from several of these, and having
// them in one place is what makes it possible to see what a model is actually told.
