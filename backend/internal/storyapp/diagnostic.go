package storyapp

import (
	"errors"
	"gameagent/backend/internal/model"
)

func safeTurnErrorCode(err error) string {
	var validation *generationJSONError
	if errors.As(err, &validation) {
		return validation.Code
	}
	code := model.TextErrorCode(err)
	if code != "unknown" {
		return code
	}
	switch {
	case errors.Is(err, ErrGenerationFailed):
		return "generated_content_invalid"
	case errors.Is(err, ErrContextCapacity):
		return "context_capacity"
	case errors.Is(err, ErrContextSourceMissing):
		return "context_source_missing"
	case errors.Is(err, ErrVersionConflict):
		return "version_conflict"
	default:
		return "internal_error"
	}
}
