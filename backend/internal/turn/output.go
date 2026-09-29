package turn

import (
	"time"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	wiaworld "gameagent/backend/internal/world"
)

// TurnIntent is what the player is trying to do this turn, and who it addresses.
//
// Visibility is a property of the player's action rather than of the world: speaking
// privately to one character is a different act from speaking aloud, and every later
// stage reads it to decide what each character was in a position to perceive.
type TurnIntent struct {
	WaitMinutes int    `json:"wait_minutes,omitempty"`
	IntentType  string `json:"intent_type"`
	AddresseeID string `json:"addressee_id"`
	Visibility  string `json:"visibility"`
	// Input is the player's own words for this turn, carried with the intent because
	// every later stage that records what the player did needs them.
	Input string `json:"-"`
}

// Private reports whether the player addressed one character rather than the room.
func (i TurnIntent) Private() bool { return i.Visibility == "private" }

// Output is what one turn produced, before it is committed.
//
// It accumulates rather than being replaced: the character stages append their events
// and perceptions, coordination appends outcomes, narration fills in the text. The two
// unexported fields carry stage state between those steps and are deliberately not part
// of any wire form — they are how one stage hands work to the next inside a turn, and
// nothing outside a turn has business reading them.
type Output struct {
	GeneratedEvents *GeneratedEventState
	Narrative       string
	Clock           string
	Scene           string
	// SceneLocation is the identifier behind Scene, so presence can be decided without
	// comparing human-readable text.
	SceneLocation   string
	SceneVersion    int64
	SceneCharacters []string
	SceneViews      []SceneView
	PlotProgress    *plot.Progress
	Events          []wiaworld.Event
	Perceptions     []wiaworld.Perception
	Memories        []wiaworld.Memory

	// PublicReplies is the public speech that has been resolved so far, carried from the
	// character stages to coordination.
	PublicReplies []string `json:"-"`
	// Decisions is what each character decided, carried the same way.
	Decisions map[string]NPCDecision `json:"-"`
	// VisibleEvents is what the player can perceive of this turn so far, carried from
	// scene resolution to narration, which renders it as the player's projection.
	//
	// It is a handoff between stages rather than a result: the stages write to the output
	// instead of returning values the next stage would have to receive, because a stage
	// boundary is exactly where such a value goes missing. It is not part of the committed
	// turn — what gets committed is Events.
	VisibleEvents []wiaworld.Event `json:"-"`
	// PerceptText is what each participating character was told it perceived, and
	// StageOneInputs is the per-character input the character stage runs on.
	PerceptText    map[string]string     `json:"-"`
	StageOneInputs map[string]StageInput `json:"-"`
	// PlayerEventID names the event that records the player's own attempt, so the
	// memories recorded at the end of the turn can point at what they are memories of.
	PlayerEventID string `json:"-"`
}

// OpenOutput opens a turn's output: it records the player's attempt as the first event
// and works out what each character in the scene is in a position to perceive.
//
// A private address is only audible to the character it was aimed at; everyone else is
// told that the two spoke quietly, and explicitly not to guess at what was said. That
// rule is here rather than in a stage because it decides the perceptions the rest of the
// turn builds on.
func OpenOutput(snapshot *Snapshot, intent TurnIntent, run wiaworld.Run) Output {
	participants := InScene(snapshot.Characters)
	recipient := intent.AddresseeID
	private := intent.Private()

	now := time.Now().UTC()
	playerEventID := run.RunID + ":input"
	output := Output{
		Clock: snapshot.Summary.Clock, Scene: snapshot.Summary.Scene, SceneVersion: snapshot.SceneVersion,
		SceneCharacters: CharacterIDs(participants),
		Events: []wiaworld.Event{{
			EventID: playerEventID, EventType: "player_attempt", ActorID: "player", TargetID: recipient,
			Content: run.Input, RunID: run.RunID, Stage: 1, SceneVersion: snapshot.SceneVersion,
			SourceType: "player_" + intent.Visibility, CreatedAt: now,
		}},
		Memories: []wiaworld.Memory{}, Perceptions: []wiaworld.Perception{},
		PerceptText: map[string]string{}, StageOneInputs: map[string]StageInput{},
		PlayerEventID: playerEventID,
	}
	for _, character := range participants {
		if private && character.EntityID != recipient {
			output.PerceptText[character.EntityID] = "你看见玩家与" + CharacterDisplayName(snapshot.Characters, recipient) + "低声交谈，但听不清内容。不要猜测原话内容。"
		} else {
			output.PerceptText[character.EntityID] = run.Input
		}
		output.Perceptions = append(output.Perceptions, wiaworld.Perception{
			RecipientID: character.EntityID, SourceEventID: playerEventID,
			SourceType: SourceTypeFor(private, character.EntityID, recipient, intent.IntentType),
			Content:    output.PerceptText[character.EntityID], Stage: 1, SceneVersion: snapshot.SceneVersion, CreatedAt: now,
		})
		output.StageOneInputs[character.EntityID] = StageInput{
			PlayerPerception: output.PerceptText[character.EntityID], SourceEventIDs: []string{playerEventID},
		}
	}
	return output
}

// InScene is the cast present in the scene, which is who a turn gives a chance to act
// and to perceive. The distinction from the full cast matters: presence decides both who
// may act and who perceives what the player did.
func InScene(characters []wiaworld.Character) []wiaworld.Character {
	out := make([]wiaworld.Character, 0, len(characters))
	for _, character := range characters {
		if character.InScene {
			out = append(out, character)
		}
	}
	return out
}

// CharacterIDs lists identifiers in order, without deciding anything about them.
func CharacterIDs(items []wiaworld.Character) []string {
	out := make([]string, 0, len(items))
	for _, character := range items {
		out = append(out, character.EntityID)
	}
	return out
}

// SpeakingExamples supplies a character's authored dialogue samples. A turn needs them
// to keep an existing save's characters speaking the way they were written, without
// reading the currently installed story.
type SpeakingExamples interface {
	CharacterSpeakingExamples(entityID string) ([]string, bool)
}

// Definition is the definition a turn runs against: the roster comes from the world,
// while dialogue samples come from the definition this world froze when it started,
// never from the currently installed story. A later revision must not change how an
// existing save's characters speak, and a world started before samples existed keeps
// none rather than silently adopting a newer template.
func Definition(snapshot *Snapshot, examples SpeakingExamples) story.Definition {
	def := snapshot.Definition
	def.Characters = snapshot.Characters
	if examples == nil {
		return def
	}
	for index := range def.Characters {
		if samples, ok := examples.CharacterSpeakingExamples(def.Characters[index].EntityID); ok {
			def.Characters[index].SpeakingExamples = samples
		}
	}
	return def
}

// SceneCharacters is the identifiers of the characters present in the scene. The
// distinction from the full cast matters: presence decides who a turn gives a chance to
// act and who perceives what the player did.
func SceneCharacters(characters []wiaworld.Character) []string {
	out := make([]string, 0, len(characters))
	for _, character := range characters {
		if character.InScene {
			out = append(out, character.EntityID)
		}
	}
	return out
}
