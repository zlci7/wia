package turn

import (
	"time"

	wiaworld "gameagent/backend/internal/world"
)

// notePlayerAction gives coordination the complete chosen input to resolve.
func notePlayerAction(output *Output, run wiaworld.Run, intent TurnIntent) {
	// Intent classification selects the interaction; the full input still needs
	// resolution because speech can include movement or other world consequences.
	output.Events = append(output.Events, wiaworld.Event{
		EventID: inputPrefix(run) + ":player-action", EventType: "player_action_intent", ActorID: "player", TargetID: intent.AddresseeID,
		Content: run.Input, RunID: run.RunID, Stage: 2, SceneVersion: output.SceneVersion,
		SourceType: "player_attempt", ProjectionParentID: output.PlayerEventID, CreatedAt: time.Now().UTC(),
	})
}

// recordPlayerExperience gives every character named by the caller a memory of what the
// player did, so the action becomes part of their history rather than only of the
// transcript.
//
// The participants are passed in rather than read off the output: the scene roster is
// what the turn resolved, and a helper that guessed it from accumulated events would
// disagree the moment a stage appended one more.
func recordPlayerExperience(snapshot *Snapshot, output *Output, intent TurnIntent, participantIDs []string, playerEventID string) {
	for _, characterID := range participantIDs {
		kind, memory := playerExperienceMemory(intent, characterID, snapshot)
		sourceID := playerEventID
		if input, ok := output.StageOneInputs[characterID]; ok && len(input.SourceEventIDs) > 0 {
			sourceID = input.SourceEventIDs[0]
		}
		output.Memories = append(output.Memories, wiaworld.Memory{
			RecipientID: characterID, Kind: kind, Content: memory,
			SourceEventID: sourceID, CreatedAt: time.Now().UTC(),
		})
	}
}

// playerExperienceMemory is what one character remembers of the player's action, in
// their own terms: a private aside is only audible to the character it was addressed to,
// and everyone else remembers having seen the two speak without the content.
func playerExperienceMemory(intent TurnIntent, characterID string, snapshot *Snapshot) (string, string) {
	recipient := intent.AddresseeID
	input := intent.Input
	if intent.Private() && characterID != recipient {
		return "observed", "我看见玩家和" + describeRecipient(snapshot, recipient) + "低声交谈，但没有听清内容。"
	}
	switch intent.IntentType {
	case "observe":
		return "observed_player_action", "我看见玩家尝试观察：" + input
	case "act":
		return "observed_player_action", "我看见玩家尝试行动：" + input
	default:
		if intent.Private() {
			return "heard_player", "玩家私下表达：" + input
		}
		return "heard_player", "玩家公开表达：" + input
	}
}

// describeRecipient names the addressee as the character list knows them, so a memory
// reads as a sentence about a person rather than about an identifier.
// describeRecipient names the addressee as the character list knows them, role included,
// so a memory reads as a sentence about a person rather than about an identifier.
//
// An address that names nobody is described as such rather than echoed back: a memory
// saying "玩家和未明确指定具体人物低声交谈" tells the character something true, while one
// built from the raw identifier would put an internal id into their recollection.
func describeRecipient(snapshot *Snapshot, recipient string) string {
	if recipient == "" {
		return "未明确指定具体人物"
	}
	if character, ok := characterByID(snapshot.Characters, recipient); ok {
		return character.Name + "（" + character.Role + "）"
	}
	return "未明确指定具体人物"
}

// characterByID finds a character in the roster the turn runs against.
func characterByID(characters []wiaworld.Character, id string) (wiaworld.Character, bool) {
	for _, character := range characters {
		if character.EntityID == id {
			return character, true
		}
	}
	return wiaworld.Character{}, false
}
