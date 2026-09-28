package storyapp

import (
	"context"
	"strings"
	"testing"
)

// The built-in stories must ship an authored voice for every important character:
// without it the narrator has only a one-line profile to infer how someone talks.
func TestBuiltInPacksAuthorACharacterVoice(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	for _, gameID := range []string{"lantern-dusk", "orbital-repair"} {
		pack, ok := a.pack(gameID)
		if !ok {
			t.Fatalf("%s is not loaded", gameID)
		}
		if len(pack.Definition.Characters) == 0 {
			t.Fatalf("%s has no characters", gameID)
		}
		for _, character := range pack.Definition.Characters {
			if len(character.SpeakingExamples) == 0 {
				t.Errorf("%s: %s has no authored dialogue samples", gameID, character.Name)
			}
			if strings.TrimSpace(character.InitialConcerns) == "" {
				t.Errorf("%s: %s has no initial concerns", gameID, character.Name)
			}
			for _, example := range character.SpeakingExamples {
				if strings.TrimSpace(example) == "" {
					t.Errorf("%s: %s has a blank sample", gameID, character.Name)
				}
			}
		}
		// The samples must also reach a world started from this story.
		world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: gameID, ExpectedRevision: pack.Definition.Revision, RequestKey: "voice-" + gameID, Activate: true})
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := a.ReadWorld(ctx, world.WorldID, 1)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Definition.Characters = snapshot.Characters
		a.attachSpeakingExamples(&snapshot.Definition)
		for _, character := range snapshot.Definition.Characters {
			if len(character.SpeakingExamples) == 0 {
				t.Errorf("%s: %s lost its samples in the world", gameID, character.Name)
			}
		}
	}
}
