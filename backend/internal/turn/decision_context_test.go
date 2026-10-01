package turn

import (
	"strings"
	"testing"

	wiaworld "gameagent/backend/internal/world"
)

func TestCoordinationProposalReferencesOnlyProvidedMatchingText(t *testing.T) {
	characters := []wiaworld.Character{{EntityID: "npc:a", Name: "甲"}}
	decisions := map[string]NPCDecision{"npc:a": {Speech: "本人私密回复全文", SpeechVisibility: "private", SpeechRecipients: []string{"player"}, ActionIntent: "本人新的行动全文"}}
	wrong := []wiaworld.Event{
		{EventID: "other:speech", EventType: "npc_dialogue", ActorID: "npc:b", Content: decisions["npc:a"].Speech},
		{EventID: "wrong:kind", EventType: "plot_result", ActorID: "npc:a", Content: decisions["npc:a"].ActionIntent},
		{EventID: "wrong:body", EventType: "npc_action_intent", ActorID: "npc:a", Content: "本人旧的行动"},
	}
	fallback := coordinationDecisionContext(decisions, characters, wrong)
	if !strings.Contains(fallback, decisions["npc:a"].Speech) || !strings.Contains(fallback, decisions["npc:a"].ActionIntent) || !strings.Contains(fallback, `speech_source_id=""`) || !strings.Contains(fallback, `action_source_id=""`) {
		t.Fatal("unprovided proposal text was replaced by an unrelated source", fallback)
	}
	provided := append(wrong,
		wiaworld.Event{EventID: "current:speech:projection:player", EventType: "npc_dialogue", ActorID: "npc:a", Content: decisions["npc:a"].Speech},
		wiaworld.Event{EventID: "current:action", EventType: "npc_action_intent", ActorID: "npc:a", Content: decisions["npc:a"].ActionIntent})
	context := coordinationDecisionContext(decisions, characters, provided)
	for _, part := range []string{`speech_source_id="current:speech:projection:player"`, `action_source_id="current:action"`, "speech_visibility=private", `speech_recipients=["player"]`, "silent=false"} {
		if !strings.Contains(context, part) {
			t.Fatal("proposal source or scope missing", context)
		}
	}
	if strings.Contains(context, decisions["npc:a"].Speech) || strings.Contains(context, decisions["npc:a"].ActionIntent) {
		t.Fatal("proposal repeated text already supplied by exact source", context)
	}
}
