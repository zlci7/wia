package storyapp

import (
	"encoding/json"
	"fmt"
	"strings"

	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// Scene views are recipient-specific, committed state, not narrator prose.
type SceneView struct {
	Recipient string   `json:"recipient"`
	Content   string   `json:"content"`
	SourceIDs []string `json:"source_ids"`
	Version   int64    `json:"version"`
}

type sceneUpdate struct {
	Content    string   `json:"content"`
	SourceIDs  []string `json:"source_ids"`
	Recipients []string `json:"recipients"`
}

type sceneSource struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	Recipients []string `json:"recipients"`
	Canonical  []string `json:"-"`
}

func sceneFor(snapshot worldSnapshot, recipient string) string {
	for _, view := range snapshot.SceneViews {
		if view.Recipient == recipient {
			return view.Content
		}
	}
	return "当前情境以本人获准经历为依据。"
}

func initialSceneViews(snapshot worldSnapshot) []SceneView {
	recipients := []string{"player"}
	for _, c := range snapshot.Characters {
		recipients = append(recipients, c.EntityID)
	}
	var views []SceneView
	for _, id := range recipients {
		view := SceneView{Recipient: id, Version: snapshot.SceneVersion, SourceIDs: []string{}}
		if snapshot.Summary.TurnSeq == 0 {
			view.Content = snapshot.Definition.Scene
			if location := snapshot.Definition.InitialLocations[id]; location != "" {
				for _, loc := range snapshot.Definition.Locations {
					if loc.ID == location {
						view.Content = loc.Name + "：" + loc.Description
					}
				}
			}
			view.SourceIDs = []string{"opening"}
		} else if id == "player" {
			for _, event := range snapshot.Events {
				if event.EventType == "turn_settled" {
					view.Content = event.Content
					view.SourceIDs = []string{event.EventID}
				}
			}
		} else {
			for _, p := range snapshot.Perceptions[id] {
				if strings.HasPrefix(p.SourceType, "action_") {
					view.Content = p.Content
					view.SourceIDs = []string{p.SourceEventID}
				}
			}
		}
		if view.Content == "" {
			view.Content = "当前地点沿用已提交经历；没有足够的获准场景信息，不推定回到开场地点。"
		}
		views = append(views, view)
	}
	return views
}

func sceneSources(snapshot worldSnapshot, run wiaworld.Run, intent turnIntent, events []wiaworld.Event) []sceneSource {
	var sources []sceneSource
	for _, view := range snapshot.SceneViews {
		sources = append(sources, sceneSource{ID: "view:" + view.Recipient, Content: view.Content, Recipients: []string{view.Recipient}, Canonical: view.SourceIDs})
	}
	public := append([]string{"player"}, wiaworld.CharacterIDs(sceneCharacters(snapshot.Characters))...)
	for _, event := range events {
		if event.RunID != run.RunID || event.Stage < 1 || event.Stage > 2 {
			continue
		}
		var recipients []string
		switch event.EventType {
		case "player_attempt":
			recipients = public
			if intent.Visibility == "private" {
				recipients = []string{"player", intent.AddresseeID}
			}
		case "npc_dialogue":
			recipients = public
		default:
			continue
		}
		sources = append(sources, sceneSource{ID: event.EventID, Content: event.Content, Recipients: recipients, Canonical: []string{event.EventID}})
	}
	return sources
}

func sceneSourcePrompt(snapshot worldSnapshot, run wiaworld.Run, intent turnIntent, events []wiaworld.Event) string {
	data, _ := json.Marshal(sceneSources(snapshot, run, intent, events))
	const scope = "\n引用范围：scene_updates.source_ids 仅从本节 id 或本轮 outcomes.action_id 选择。保留旧状态时引用 view:接收者ID；未在本节 id 清单中的旧历史 event_id、视图内部 source_ids、剧情根事件ID及原始输入 ID 均不是本阶段可直接引用的场景来源。等待中断的 interrupt_source_ids 使用另一份历史证据合同，不能复制进 scene_updates.source_ids。"
	return scope + "\n场景来源(JSON)：" + string(data) + "\n场景视图合同：scene_updates 必须是数组，无变化返回 []。每项包含 content（该接收者回合结束时的完整简明情境）、source_ids（依据 ID 数组）、recipients（接收者 ID 数组）。此前视图 view:ID 仅属于该 ID；不同接收者分别更新。玩家表达与对白使用上述来源 ID，新行动结果使用本轮对应 outcome 的 action_id（只能在该 outcome 的 recipients 与行动者范围内）。每位接收者最多一次更新；每个引用都必须允许该接收者读取。人物对白是声称，不当成真相；行动尝试不是成功。无来源不更新，不加入未获知的隐情。scene 只作协调记录，不作为任何人的共享事实；玩家位置或环境有变化时必须通过 player 的 scene_updates 表达。"
}

func applySceneUpdates(snapshot worldSnapshot, run wiaworld.Run, intent turnIntent, output turnOutput, host hostResult) ([]SceneView, error) {
	byID := map[string]sceneSource{}
	for _, source := range sceneSources(snapshot, run, intent, output.Events) {
		byID[source.ID] = source
	}
	for i, outcome := range host.Outcomes {
		action, ok := turn.EventByID(output.Events, outcome.ActionID)
		if !ok || action.RunID != run.RunID || action.Stage < 1 || action.Stage > 2 {
			return nil, ErrGenerationFailed
		}
		id := fmt.Sprintf("%s:result:%d", outcome.ActionID, i+1)
		byID[outcome.ActionID] = sceneSource{ID: outcome.ActionID, Content: outcome.Content, Recipients: append(append([]string{}, outcome.Recipients...), action.ActorID), Canonical: []string{id}}
	}
	return mergeSceneUpdates(snapshot.SceneViews, snapshot.SceneVersion+1, byID, host.SceneUpdates)
}

func mergeSceneUpdates(previous []SceneView, version int64, byID map[string]sceneSource, updates []sceneUpdate) ([]SceneView, error) {
	views := append([]SceneView{}, previous...)
	seen := map[string]bool{}
	for _, update := range updates {
		if wire.Clean(update.Content) == "" || len(update.SourceIDs) == 0 || len(update.Recipients) == 0 {
			return nil, fmt.Errorf("%w: incomplete scene update", ErrGenerationFailed)
		}
		for _, recipient := range update.Recipients {
			index := -1
			for i, v := range views {
				if v.Recipient == recipient {
					index = i
					break
				}
			}
			if index < 0 || seen[recipient] {
				return nil, fmt.Errorf("%w: invalid scene recipient", ErrGenerationFailed)
			}
			seen[recipient] = true
			var canonical []string
			for _, id := range update.SourceIDs {
				source, ok := byID[id]
				if !ok || !containsID(source.Recipients, recipient) {
					return nil, fmt.Errorf("%w: scene source is unavailable to recipient", ErrGenerationFailed)
				}
				for _, sid := range source.Canonical {
					if !containsID(canonical, sid) {
						canonical = append(canonical, sid)
					}
				}
			}
			views[index] = SceneView{Recipient: recipient, Content: wire.Clean(update.Content), SourceIDs: canonical, Version: version}
		}
	}
	return views, nil
}

// Later stages read only committed views and the projections already granted in
// this workspace. An author's plot result is never a scene source for a player.
func plotSceneSources(output turnOutput, visible []wiaworld.Event) map[string]sceneSource {
	sources := map[string]sceneSource{}
	for _, view := range output.SceneViews {
		sources["view:"+view.Recipient] = sceneSource{ID: "view:" + view.Recipient, Content: view.Content, Recipients: []string{view.Recipient}, Canonical: view.SourceIDs}
	}
	for _, p := range output.Perceptions {
		if p.Stage < 4 {
			continue
		}
		s := sources[p.SourceEventID]
		s.ID, s.Content, s.Canonical = p.SourceEventID, p.Content, []string{p.SourceEventID}
		if !containsID(s.Recipients, p.RecipientID) {
			s.Recipients = append(s.Recipients, p.RecipientID)
		}
		sources[p.SourceEventID] = s
	}
	for _, e := range visible {
		s := sources[e.EventID]
		s.ID, s.Content, s.Canonical = e.EventID, e.Content, []string{e.EventID}
		if !containsID(s.Recipients, "player") {
			s.Recipients = append(s.Recipients, "player")
		}
		sources[e.EventID] = s
	}
	return sources
}

func applyPlotSceneUpdates(output *turnOutput, sources map[string]sceneSource, updates []sceneUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	views, err := mergeSceneUpdates(output.SceneViews, output.SceneVersion+1, sources, updates)
	if err != nil {
		return err
	}
	output.SceneVersion++
	output.SceneViews = views
	output.Scene = sceneFor(worldSnapshot{SceneViews: views}, "player")
	return nil
}

func containsID(ids []string, id string) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}

func validateSceneViews(snapshot worldSnapshot) error {
	valid := map[string]bool{"player": true}
	for _, c := range snapshot.Characters {
		valid[c.EntityID] = true
	}
	seen := map[string]bool{}
	for _, view := range snapshot.SceneViews {
		if !valid[view.Recipient] || seen[view.Recipient] || wire.Clean(view.Content) == "" || view.Version > snapshot.SceneVersion || view.Version < 1 {
			return fmt.Errorf("%w: invalid stored scene view", ErrContextSourceMissing)
		}
		seen[view.Recipient] = true
	}
	if len(seen) != len(valid) {
		return fmt.Errorf("%w: incomplete stored scene views", ErrContextSourceMissing)
	}
	return nil
}
