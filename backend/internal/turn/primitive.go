package turn

import (
	"encoding/json"
	"fmt"
	"strings"

	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type NPCDecision struct {
	RecallQuery           string                 `json:"recall_query,omitempty"`
	Speech                string                 `json:"speech"`
	ActionIntent          string                 `json:"action_intent"`
	ActionTargetID        string                 `json:"action_target_id,omitempty"`
	Silent                bool                   `json:"silent"`
	Memory                string                 `json:"memory"`
	RelationshipProposals []relationshipProposal `json:"relationship_proposals,omitempty"`
}

type relationshipProposal struct {
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
	Delta        int    `json:"delta"`
	SourceID     string `json:"source_id"`
}

type StageInput struct {
	PlayerPerception string
	NewStimulus      string
	SourceEventIDs   []string
}

func RenderVisibleProjection(events []wiaworld.Event, characters []wiaworld.Character) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		if event.EventType == "player_attempt" {
			continue
		}
		content := wire.Clean(event.Content)
		if content == "" {
			continue
		}
		if event.EventType == "npc_dialogue" {
			parts = append(parts, fmt.Sprintf("%s说：%s", CharacterDisplayName(characters, event.ActorID), content))
		} else {
			parts = append(parts, content)
		}
	}
	if len(parts) == 0 {
		return "没有人立刻回答，也没有发生玩家可见的新结果。"
	}
	return strings.Join(parts, "\n")
}

func VisibleTurnEvents(events, visibleOutcomes []wiaworld.Event, playerNarrativeInput string) []wiaworld.Event {
	result := make([]wiaworld.Event, 0, len(events)+len(visibleOutcomes))
	for _, event := range events {
		if event.EventType == "player_attempt" || event.EventType == "npc_dialogue" {
			if event.EventType == "player_attempt" {
				event.Content = playerNarrativeInput
			}
			result = append(result, event)
		}
	}
	return append(result, visibleOutcomes...)
}

func EventIDs(events []wiaworld.Event) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		if event.EventID != "" {
			result = append(result, event.EventID)
		}
	}
	return result
}

func PublicReplyStageInputs(perceptions []wiaworld.Perception, playerPerceptions map[string]string, sourceStage int) map[string]StageInput {
	texts := make(map[string][]string)
	sources := make(map[string][]string)
	for _, perception := range perceptions {
		if perception.Stage != sourceStage || perception.SourceType != "heard_public_reply" {
			continue
		}
		texts[perception.RecipientID] = append(texts[perception.RecipientID], perception.Content)
		sources[perception.RecipientID] = append(sources[perception.RecipientID], perception.SourceEventID)
	}
	result := make(map[string]StageInput, len(texts))
	for recipientID, items := range texts {
		result[recipientID] = StageInput{
			PlayerPerception: playerPerceptions[recipientID],
			NewStimulus:      strings.Join(items, "\n"),
			SourceEventIDs:   append([]string(nil), sources[recipientID]...),
		}
	}
	return result
}

func MergeNPCDecision(previous, current NPCDecision) NPCDecision {
	if current.Speech == "" {
		current.Speech = previous.Speech
	}
	if current.ActionIntent == "" {
		current.ActionIntent = previous.ActionIntent
		current.ActionTargetID = previous.ActionTargetID
	}
	if current.Memory == "" {
		current.Memory = previous.Memory
	}
	if len(previous.RelationshipProposals) > 0 {
		current.RelationshipProposals = append(append([]relationshipProposal(nil), previous.RelationshipProposals...), current.RelationshipProposals...)
	}
	current.Silent = current.Speech == ""
	return current
}

func AdvanceClock(clock string, minutes int) string {
	if minutes == 0 {
		return clock
	}
	var day, hour, minute int
	if _, err := fmt.Sscanf(clock, "第 %d 日 %d:%d", &day, &hour, &minute); err != nil || day < 1 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return clock
	}
	total := (day-1)*24*60 + hour*60 + minute + minutes
	if total < 0 {
		return clock
	}
	return fmt.Sprintf("第 %d 日 %02d:%02d", total/(24*60)+1, (total/60)%24, total%60)
}

func SourceTypeFor(private bool, id, recipient, intentType string) string {
	if private && id != recipient {
		return "observed_private_conversation"
	}
	if intentType == "speak" {
		if private {
			return "direct_private_message"
		}
		return "direct_hearing"
	}
	return "observed_player_action"
}

func FormatSelfDecision(decision NPCDecision) string {
	return fmt.Sprintf("已进入本轮交谈的本人公开对白：%q\n本人尚未执行、待场景协调的行动提案：%q\n是否保持沉默：%t\n本人有向关系变化提案：%s\n本轮暂存主观判断（不是执行结果）：%q", decision.Speech, decision.ActionIntent, decision.Silent, wire.MarshalJSON(decision.RelationshipProposals), decision.Memory)
}

func EventByID(events []wiaworld.Event, id string) (wiaworld.Event, bool) {
	for _, event := range events {
		if event.EventID == id {
			return event, true
		}
	}
	return wiaworld.Event{}, false
}

func CharacterDisplayName(characters []wiaworld.Character, id string) string {
	for _, character := range characters {
		if character.EntityID == id {
			return fmt.Sprintf("%s（%s）", character.Name, character.Role)
		}
	}
	if id == "player" {
		return "玩家"
	}
	return id
}

func PublicCharacterContext(characters []wiaworld.Character, sceneCharacterIDs []string) string {
	inScene := make(map[string]bool, len(sceneCharacterIDs))
	for _, id := range sceneCharacterIDs {
		inScene[id] = true
	}
	type publicFact struct {
		EntityID string `json:"entity_id"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		InScene  bool   `json:"in_scene"`
	}
	facts := make([]publicFact, 0, len(characters))
	for _, character := range characters {
		facts = append(facts, publicFact{EntityID: character.EntityID, Name: character.Name, Role: character.Role, InScene: inScene[character.EntityID]})
	}
	data, _ := json.Marshal(facts)
	return string(data)
}

func CoordinationContinuity(events []wiaworld.Event) string {
	completed := make([]wiaworld.Event, 0)
	for _, event := range events {
		if event.EventType == "npc_action_result" || event.EventType == "player_action_result" {
			completed = append(completed, event)
		}
	}
	if len(completed) > 12 {
		completed = completed[len(completed)-12:]
	}
	data, _ := json.Marshal(completed)
	return "连续状态规则：scene 是本轮结束后的简明状态快照，写人物位置、物件状态及仍成立的环境，不是动作回放或对白记录。此前已提交行动结果是连续状态的依据；按时间顺序接续，较新的明确结果覆盖同一事项的旧状态。上一场景中取碗、斟茶、递物等进行式描述，若对应结果已经完成，应写为完成后的状态，不能再次执行。输入与已完成动作重叠时，结合当前状态承接新意图。本轮未提出或未确认的新行动不能借 scene 补出；没有动作变化时沿用完成状态，不把人物移回原位置。\n此前已提交行动结果（仅作状态依据，不属于本轮待裁定行动）：" + string(data) + "\n"
}

func AvailableCharacterIDs(items []wiaworld.Character) string {
	var ids []string
	for _, character := range items {
		ids = append(ids, fmt.Sprintf("%s=%s（%s）", character.EntityID, character.Name, character.Role))
	}
	return strings.Join(ids, "、")
}

func NormalizeSceneCharacters(ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = wire.Clean(id)
		if id != "player" {
			result = append(result, id)
		}
	}
	return result
}
