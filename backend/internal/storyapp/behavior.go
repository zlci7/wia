package storyapp

import (
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

// migrateWritingPreference reads a settings record written before writing preferences
// became a behavior policy, and folds the old free-text preference into the narration
// policy. It stays here rather than in turn because it is about reading an older stored
// record, not about what the model is told: it belongs with the rest of this
// application's storage compatibility until that moves too.
func migrateWritingPreference(settings wiaworld.NarrativeSettings) wiaworld.NarrativeSettings {
	if settings.CustomInstruction != "" && settings.Policies.Narration == "" {
		settings.Policies.Narration = turn.PacingInstruction() + "\n写作偏好：" + settings.CustomInstruction
	}
	settings.CustomInstruction = ""
	return settings
}
