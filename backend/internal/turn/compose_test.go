package turn

import (
	"errors"
	"testing"

	wiaworld "gameagent/backend/internal/world"
)

func TestNarrativeUsesPlainTextContract(t *testing.T) {
	plain := "雨声沿着窗棂滑下，客栈里的人都抬起头。"
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"plain text", plain, plain},
		{"legacy wrapper", `{"narrative":"旧格式正文"}`, "旧格式正文"},
		{"markdown fence", "```text\n" + plain + "\n```", plain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseNarrativeText(tc.text)
			if err != nil || got != tc.want {
				t.Fatalf("parseNarrativeText(%q) = %q, %v", tc.text, got, err)
			}
		})
	}
	for _, text := range []string{"", `{"narrative":`, `{"other":"field"}`} {
		if _, err := parseNarrativeText(text); !errors.Is(err, ErrGenerationFailed) {
			t.Fatalf("invalid narrative %q error = %v", text, err)
		}
	}
}

// narrativeFixtureCharacters is the smallest roster the projection needs: one character
// whose speech has to keep its public scope.
func narrativeFixtureCharacters() []wiaworld.Character {
	return []wiaworld.Character{{EntityID: "npc:innkeeper", Name: "沈岚", Role: "客栈老板"}}
}

func TestNarrativeEventsPreservePublicNPCSpeechScope(t *testing.T) {
	events := narrativeEvents([]wiaworld.Event{{EventType: "npc_dialogue", ActorID: "npc:innkeeper", Content: "公开回答"}}, narrativeFixtureCharacters(), "旅人", wiaworld.NarrativeSettings{})
	if len(events) != 1 || events[0].SpeechScope != "public_current_scene" {
		t.Fatalf("%+v", events)
	}
}

func TestNarrativeEventsPreservePlayerAudibility(t *testing.T) {
	for _, test := range []struct{ source, scope string }{{"player_public", "public_current_scene"}, {"player_private", "private_recipient"}} {
		events := narrativeEvents([]wiaworld.Event{{EventType: "player_attempt", ActorID: "player", SourceType: test.source, Content: "你明白的"}}, nil, "旅人", wiaworld.NarrativeSettings{})
		if len(events) != 1 || events[0].SpeechScope != test.scope {
			t.Fatalf("%+v", events)
		}
	}
}
