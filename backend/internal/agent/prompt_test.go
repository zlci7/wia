package agent_test

import (
	"strings"
	"testing"

	"gameagent/backend/internal/agent"
)

func TestBuildSystemPromptUsesPromptConfig(t *testing.T) {
	prompt := agent.BuildSystemPrompt(agent.PromptConfig{
		Language:        "Simplified Chinese",
		NPCStyle:        "quiet mountain hermit",
		MaxSpeakChars:   42,
		ToolInstruction: "Use exactly one available tool.",
	})

	for _, want := range []string{
		"Simplified Chinese",
		"quiet mountain hermit",
		"42 characters",
		"Use exactly one available tool.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "speak") {
		t.Fatalf("prompt should not name a concrete tool capability:\n%s", prompt)
	}
}
