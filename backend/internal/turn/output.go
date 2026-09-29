package turn

import (
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
