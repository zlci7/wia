package world

import (
	"errors"
	"strings"
)

// NarrativeSettings is how a story tells itself: the point of view the prose
// uses, how long and how dense it is, how much of the player's own expression is
// written out, and how forward the characters are. A world stores one of these
// and every generation stage reads it.
//
// The text the model is finally given for these choices is not here. Turning a
// setting into an instruction is the job of the stage that calls the model, so
// this package holds the choice and whether it is legal, and nothing about how it
// is phrased.
type NarrativeSettings struct {
	Perspective       string           `json:"perspective"`
	Length            string           `json:"length"`
	Detail            string           `json:"detail"`
	PlayerElaboration string           `json:"player_elaboration"`
	NPCInitiative     string           `json:"npc_initiative"`
	CustomInstruction string           `json:"custom_instruction,omitempty"`
	Policies          BehaviorPolicies `json:"behavior_policies"`
}

const (
	PerspectiveFirstPerson  = "first_person"
	PerspectiveSecondPerson = "second_person"
	PerspectiveThirdPerson  = "third_person"

	NarrativeLengthConcise  = "concise"
	NarrativeLengthStandard = "standard"
	NarrativeLengthDetailed = "detailed"

	NarrativeDetailRestrained = "restrained"
	NarrativeDetailBalanced   = "balanced"
	NarrativeDetailRich       = "rich"

	PlayerElaborationRestrained = "restrained"
	PlayerElaborationNatural    = "natural"
	PlayerElaborationExpressive = "expressive"

	NPCInitiativeResponsive = "responsive"
	NPCInitiativeContextual = "contextual"
	NPCInitiativeProactive  = "proactive"
)

// BehaviorPolicyVersion names the shape of a stored policy set, and
// MaxBehaviorPolicyChars bounds one policy text.
const (
	BehaviorPolicyVersion  = "behavior.v1"
	MaxBehaviorPolicyChars = 4000
)

// BehaviorPolicies holds the author's stage-specific instruction text. Empty text
// means the stage falls back to its bundled default.
type BehaviorPolicies struct {
	Coordination string `json:"coordination"`
	Narration    string `json:"narration"`
	NPC          string `json:"npc"`
}

// BehaviorPolicyCatalog is what the interface shows for the policy editor.
type BehaviorPolicyCatalog struct {
	BehaviorPolicies
	Version  string `json:"version"`
	MaxChars int    `json:"max_chars"`
}

// ErrInvalidNarrativeSettings reports a settings value the domain does not
// accept. Callers that expose an HTTP contract map it to their own error; the
// domain does not know about HTTP.
var ErrInvalidNarrativeSettings = errors.New("invalid narrative settings")

// DefaultNarrativeSettings is what a world uses until it is told otherwise.
//
// It leaves the policies empty on purpose: an empty policy means the calling
// stage uses its own bundled text, which is where that text belongs.
func DefaultNarrativeSettings() NarrativeSettings {
	return NarrativeSettings{
		Perspective:       PerspectiveSecondPerson,
		Length:            NarrativeLengthStandard,
		Detail:            NarrativeDetailBalanced,
		PlayerElaboration: PlayerElaborationNatural,
		NPCInitiative:     NPCInitiativeContextual,
	}
}

// ValidateNarrativeSettings normalizes a stored or submitted value and rejects
// anything the domain does not accept.
func ValidateNarrativeSettings(settings NarrativeSettings) (NarrativeSettings, error) {
	settings.Perspective = strings.TrimSpace(settings.Perspective)
	settings.Length = strings.TrimSpace(settings.Length)
	settings.Detail = strings.TrimSpace(settings.Detail)
	settings.PlayerElaboration = strings.TrimSpace(settings.PlayerElaboration)
	settings.NPCInitiative = strings.TrimSpace(settings.NPCInitiative)
	settings.CustomInstruction = strings.TrimSpace(strings.ReplaceAll(settings.CustomInstruction, "\x00", ""))
	if settings.Perspective != PerspectiveFirstPerson && settings.Perspective != PerspectiveSecondPerson && settings.Perspective != PerspectiveThirdPerson {
		return NarrativeSettings{}, ErrInvalidNarrativeSettings
	}
	if settings.Length != NarrativeLengthConcise && settings.Length != NarrativeLengthStandard && settings.Length != NarrativeLengthDetailed {
		return NarrativeSettings{}, ErrInvalidNarrativeSettings
	}
	if settings.Detail != NarrativeDetailRestrained && settings.Detail != NarrativeDetailBalanced && settings.Detail != NarrativeDetailRich {
		return NarrativeSettings{}, ErrInvalidNarrativeSettings
	}
	if settings.PlayerElaboration != PlayerElaborationRestrained && settings.PlayerElaboration != PlayerElaborationNatural && settings.PlayerElaboration != PlayerElaborationExpressive {
		return NarrativeSettings{}, ErrInvalidNarrativeSettings
	}
	if settings.NPCInitiative != NPCInitiativeResponsive && settings.NPCInitiative != NPCInitiativeContextual && settings.NPCInitiative != NPCInitiativeProactive {
		return NarrativeSettings{}, ErrInvalidNarrativeSettings
	}
	if len([]rune(settings.CustomInstruction)) > 1000 {
		return NarrativeSettings{}, ErrInvalidNarrativeSettings
	}
	var err error
	settings.Policies, err = ValidateBehaviorPolicies(settings.Policies)
	if err != nil {
		return NarrativeSettings{}, err
	}
	return settings, nil
}

// ValidateBehaviorPolicies normalizes the three policy texts and rejects text
// that is empty of meaning, carries a NUL byte, or exceeds the stored limit.
func ValidateBehaviorPolicies(p BehaviorPolicies) (BehaviorPolicies, error) {
	for _, text := range []*string{&p.NPC, &p.Coordination, &p.Narration} {
		*text = strings.TrimSpace(*text)
		if strings.ContainsRune(*text, '\x00') || len([]rune(*text)) > MaxBehaviorPolicyChars {
			return BehaviorPolicies{}, ErrInvalidNarrativeSettings
		}
	}
	return p, nil
}
