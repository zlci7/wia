package turn

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

const personalProjectionContract = "\n个人投影合同：每个 outcome.content 只写作者层已确定事实，不供人物直接读取。projections 必须是数组，每项仅含 recipient、content；为 recipients、行动者和 bystanders 中每个不同接收者各写一项本人实际可感知的内容。可因人而异，省略未获知的秘密。行动者也需要自己的投影。recipients 只列实际重要人物和 player，bystanders 只列实际背景人物；没有观察资格的人不能通过 projections 新增。scene_updates 引用 action_id 时仅使用该接收者的投影内容；作者根结果不作为私人知识来源。"

func validateNPCSpeech(snapshot Snapshot, speaker string, decision *NPCDecision) error {
	if decision.SpeechVisibility == "" {
		decision.SpeechVisibility = "public"
	}
	if decision.SpeechVisibility != "public" && decision.SpeechVisibility != "private" {
		return coordinationInvalid("speech_scope_invalid", "speech_visibility", "public|private")
	}
	if decision.Silent || strings.TrimSpace(decision.Speech) == "" {
		decision.SpeechRecipients = nil
		decision.SpeechVisibility = "public"
		return nil
	}
	if decision.SpeechVisibility == "public" {
		if len(decision.SpeechRecipients) > 0 {
			return coordinationInvalid("speech_recipients_invalid", "speech_recipients", "empty-for-public-speech")
		}
		return nil
	}
	if len(decision.SpeechRecipients) == 0 || len(decision.SpeechRecipients) > 4 {
		return coordinationInvalid("speech_recipients_invalid", "speech_recipients", "one-to-four-contactable-recipients-for-private-speech")
	}
	seen := map[string]bool{}
	for _, id := range decision.SpeechRecipients {
		if id == speaker || seen[id] {
			return coordinationInvalid("speech_recipient_invalid", "speech_recipients", "unique-other-contactable-person")
		}
		if id != "player" {
			_, defined := characterByID(snapshot.Characters, id)
			for _, bystander := range snapshot.Definition.BystanderRefs {
				defined = defined || bystander.BystanderID == id
			}
			if !defined {
				return coordinationInvalid("speech_recipient_invalid", "speech_recipients", "defined-person-or-player")
			}
		}
		if snapshot.Definition.Capabilities["spatial"] == 1 {
			if !currentlyCoLocated(snapshot, speaker, id) {
				return coordinationInvalid("speech_recipient_unreachable", "speech_recipients", "same-current-place-as-speaker")
			}
		} else {
			if snapshot.speechRoster != nil && (!slices.Contains(snapshot.speechRoster, speaker) || id != "player" && !slices.Contains(snapshot.speechRoster, id)) {
				return coordinationInvalid("speech_recipient_unreachable", "speech_recipients", "actual-current-contact")
			}
			if _, ok := FindSceneCharacter(snapshot.Characters, speaker); !ok {
				return coordinationInvalid("speech_recipient_unreachable", "speech_recipients", "actual-current-contact")
			}
			if id != "player" {
				_, present := FindSceneCharacter(snapshot.Characters, id)
				for _, bystander := range snapshot.BystanderRefs {
					present = present || bystander.BystanderID == id
				}
				if !present {
					return coordinationInvalid("speech_recipient_unreachable", "speech_recipients", "actual-current-contact")
				}
			}
		}
		seen[id] = true
	}
	return nil
}

func appendNPCSpeech(output *Output, run wiaworld.Run, character wiaworld.Character, decision NPCDecision, participants []wiaworld.Character, sceneVersion int64, stage int) (string, string) {
	rootID := fmt.Sprintf("%s:%s:speech:%d", inputPrefix(run), character.EntityID, stage)
	root := wiaworld.Event{EventID: rootID, EventType: "npc_speech", ActorID: character.EntityID, Content: decision.Speech, RunID: run.RunID, Stage: stage, SceneVersion: sceneVersion, SourceType: "author_speech", CreatedAt: time.Now().UTC()}
	output.Events = append(output.Events, root)
	output.Perceptions = append(output.Perceptions, wiaworld.Perception{RecipientID: character.EntityID, SourceEventID: rootID, SourceType: "own_speech", Content: decision.Speech, Stage: stage, SceneVersion: sceneVersion, CreatedAt: root.CreatedAt})
	private := decision.SpeechVisibility == "private"
	recipients := []string{}
	audience := output.speechAudience
	if audience == nil {
		audience = append(CharacterIDs(participants), "player")
	}
	for _, id := range audience {
		if id != character.EntityID && !slices.Contains(recipients, id) && (!private || slices.Contains(decision.SpeechRecipients, id)) {
			recipients = append(recipients, id)
		}
	}
	for _, recipient := range recipients {
		projection := root
		projection.EventID = rootID + ":projection:" + recipient
		projection.EventType, projection.TargetID, projection.ProjectionParentID = "npc_dialogue", recipient, rootID
		projection.SourceType = "speech_public"
		typeName, scope := "heard_public_reply", "公开"
		if private {
			projection.SourceType = "speech_private"
			typeName, scope = "heard_private_reply", "私下"
		}
		output.Events = append(output.Events, projection)
		output.Perceptions = append(output.Perceptions, wiaworld.Perception{RecipientID: recipient, SourceEventID: projection.EventID, SourceType: typeName, Content: fmt.Sprintf("%s（%s）%s说：%s", character.Name, character.Role, scope, decision.Speech), Stage: stage, SceneVersion: sceneVersion, CreatedAt: root.CreatedAt})
	}
	reply := ""
	if !private {
		reply = fmt.Sprintf("%s（%s）说：%s", character.Name, character.Role, decision.Speech)
	}
	return rootID, reply
}

func projectionSource(action wiaworld.Event, outcome hostActionResult, index int) sceneSource {
	root := fmt.Sprintf("%s:result:%d", outcome.ActionID, index+1)
	ids := append(append([]string{}, outcome.Recipients...), action.ActorID)
	canonical := map[string][]string{}
	for _, id := range ids {
		canonical[id] = []string{root + ":projection:" + id}
	}
	return sceneSource{ID: outcome.ActionID, Content: outcome.Content, Recipients: ids, CanonicalByRecipient: canonical}
}

func speechScope(event wiaworld.Event) string {
	if event.SourceType == "speech_private" || event.SourceType == "player_private" {
		return "private_recipient"
	}
	if event.SourceType == "speech_public" || event.SourceType == "visible_dialogue" || event.SourceType == "player_public" || event.EventType == "npc_dialogue" && event.SourceType == "" {
		return "public_current_scene"
	}
	return ""
}

func actionProjectionText(outcome hostActionResult, recipients map[string]bool) (map[string]string, error) {
	texts := map[string]string{}
	if outcome.Projections == nil {
		return nil, coordinationInvalid("action_projections_required", "outcomes.projections", "explicit-personal-projections")
	}
	for _, projection := range outcome.Projections {
		if !recipients[projection.Recipient] || texts[projection.Recipient] != "" || strings.TrimSpace(projection.Content) == "" {
			return nil, coordinationInvalid("action_projection_invalid", "outcomes.projections", "one-nonempty-text-per-authorized-observer")
		}
		texts[projection.Recipient] = strings.TrimSpace(projection.Content)
	}
	if len(texts) != len(recipients) {
		missing := []string{}
		for id := range recipients {
			if texts[id] == "" {
				missing = append(missing, id)
			}
		}
		slices.Sort(missing)
		return nil, coordinationInvalid("action_projection_missing", "outcomes.projections", "action_id="+outcome.ActionID+"; missing-recipient-ids="+strings.Join(missing, ","))
	}
	return texts, nil
}

func speechContactContext(snapshot Snapshot, speaker string) string {
	type contact struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	contacts := []contact{}
	canContact := func(id string) bool {
		if snapshot.Definition.Capabilities["spatial"] == 1 {
			return currentlyCoLocated(snapshot, speaker, id)
		}
		if snapshot.speechRoster != nil {
			return slices.Contains(snapshot.speechRoster, speaker) && (id == "player" || slices.Contains(snapshot.speechRoster, id))
		}
		if _, ok := FindSceneCharacter(snapshot.Characters, speaker); !ok {
			return false
		}
		return id == "player" || slices.Contains(CharacterIDs(InScene(snapshot.Characters)), id)
	}
	if canContact("player") {
		contacts = append(contacts, contact{ID: "player", Name: snapshot.PlayerName, Role: "主角"})
	}
	for _, person := range snapshot.Characters {
		if person.EntityID != speaker && canContact(person.EntityID) {
			contacts = append(contacts, contact{ID: person.EntityID, Name: person.Name, Role: person.Role})
		}
	}
	for _, person := range snapshot.Definition.BystanderRefs {
		if snapshot.Definition.Capabilities["spatial"] == 1 && canContact(person.BystanderID) {
			contacts = append(contacts, contact{ID: person.BystanderID, Name: person.Name, Role: "背景人物"})
		}
	}
	return wire.MarshalJSON(contacts)
}
