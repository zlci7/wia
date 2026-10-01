package turn

// What a stage tells the model about the people and the scene around the player.
//
// These read the turn's snapshot and produce text: who else is present, what has been
// said so far, and what the world's own records say about what has already happened. They
// are here because they are what the model is told, and because a stage is assembled from
// several of them — having them in one place is what makes it possible to read what a
// model will actually receive.

import (
	"encoding/json"
	"fmt"
	"strings"

	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func DialogueSections(snapshot Snapshot) []Section {
	var groups []Section
	for _, event := range snapshot.Dialogue {
		if len(groups) == 0 || groups[len(groups)-1].Name != "dialogue:"+event.RunID {
			groups = append(groups, Section{Name: "dialogue:" + event.RunID})
		}
		i := len(groups) - 1
		part := snapshot
		part.Dialogue = []wiaworld.Event{event}
		groups[i].Text += "此前已提交对话：" + DialogueContext(part)
		groups[i].Sources = append(groups[i].Sources, event.EventID)
	}
	return groups
}

func SceneViewSources(snapshot Snapshot, recipient string) []string {
	var ids []string
	for _, view := range snapshot.SceneViews {
		if recipient == "" || view.Recipient == recipient {
			for _, id := range view.SourceIDs {
				if !wiaworld.ContainsID(ids, id) {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

// CoordinationScene is the per-recipient scene views as the coordinator sees them, so it
// can keep unchanged places, people and passers-by in the scene it returns.
func CoordinationScene(snapshot Snapshot) string {
	data, _ := json.Marshal(snapshot.SceneViews)
	return string(data)
}

// DialogueContext renders the dialogue already committed this world as lines that carry
// their own provenance, so a model can tell who said what to whom and how privately.
func DialogueContext(snapshot Snapshot) string {
	var result strings.Builder
	for _, event := range snapshot.Dialogue {
		fmt.Fprintf(&result, "[来源=%s；回合=%s；类型=%s；表达者=%s；对象=%s；范围=%s] %s\n", event.EventID, event.RunID, event.EventType, CharacterDisplayName(snapshot.Characters, event.ActorID), event.TargetID, event.SourceType, event.Content)
	}
	return result.String()
}

// IntentVisibilityRule tells the model how to decide whether what the player said was
// private. It is a constant because it is part of the contract, not a setting.
const IntentVisibilityRule = "交谈对象与可听范围分别判断。指定某人、使用引号或试探语气均不单独构成私聊依据。未表达私密意图且没有已成立私密情境时，visibility 为 public。明确耳语、限定听众或有来源的近期私密交谈情境可支持 private；当前明确表达优先。历史发言用于理解省略和指代，不是本轮再次发生的动作。"

// FormatBystanders lists passers-by with their stable identity so a coordinated
// outcome can attribute experience to the one that actually took part.
func FormatBystanders(bystanders []story.Bystander, names []string) string {
	if len(bystanders) == 0 {
		if len(names) == 0 {
			return "（无已记录背景人物）"
		}
		return strings.Join(names, "、")
	}
	parts := make([]string, 0, len(bystanders))
	for _, bystander := range bystanders {
		if bystander.BystanderID != "" {
			parts = append(parts, fmt.Sprintf("%s（%s）", bystander.Name, bystander.BystanderID))
			continue
		}
		parts = append(parts, bystander.Name)
	}
	return strings.Join(parts, "、")
}

// A proposal can reference text already supplied in this coordination request.
// Actor, event kind and exact content must match; otherwise the full text stays here.
func coordinationDecisionContext(decisions map[string]NPCDecision, characters []wiaworld.Character, provided []wiaworld.Event) string {
	var parts []string
	for _, character := range characters {
		decision, exists := decisions[character.EntityID]
		if !exists {
			continue
		}
		speech, action := decision.Speech, decision.ActionIntent
		speechSource, actionSource := "", ""
		for _, event := range provided {
			if event.ActorID != character.EntityID || event.EventID == "" {
				continue
			}
			if speech != "" && event.EventType == "npc_dialogue" && event.Content == speech {
				speechSource, speech = event.EventID, ""
			}
			if action != "" && event.EventType == "npc_action_intent" && event.Content == action {
				actionSource, action = event.EventID, ""
			}
		}
		parts = append(parts, fmt.Sprintf("%s（%s，%s）：speech=%q；speech_source_id=%q；speech_visibility=%s；speech_recipients=%s；action_intent=%q；action_source_id=%q；silent=%t；relationship_proposals=%s", character.Name, character.Role, character.EntityID, speech, speechSource, decision.SpeechVisibility, wire.MarshalJSON(decision.SpeechRecipients), action, actionSource, decision.Silent, wire.MarshalJSON(decision.RelationshipProposals)))
	}
	if len(parts) == 0 {
		return "（暂无）"
	}
	return strings.Join(parts, "\n")
}
