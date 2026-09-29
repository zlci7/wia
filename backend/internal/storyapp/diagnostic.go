package storyapp

import (
	"errors"

	"gameagent/backend/internal/turn"
)

// safeTurnErrorCode is the diagnostic code for a failed turn. The application's own
// sentinels are decided here; everything else is the turn's vocabulary, which already
// covers generated JSON, model failures and context capacity.
//
// The two sentinels are checked first because they are returned on their own — nothing
// wraps them together with a model or generation error — so the code a caller sees is
// the same as when this switch held every case itself.
func safeTurnErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrContextSourceMissing):
		return "context_source_missing"
	case errors.Is(err, ErrVersionConflict):
		return "version_conflict"
	}
	return turn.ErrorCode(err)
}
