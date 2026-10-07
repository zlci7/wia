package model

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// TextDiagnostic contains only bounded transport/validation metadata, never text.
type TextDiagnostic struct {
	Provider        string
	Model           string
	InputKnown      bool
	OutputKnown     bool
	ReasoningKnown  bool
	CacheKnown      bool
	CacheHitTokens  int
	CacheMissTokens int
	Code            string
	HTTPStatus      int
	RequestID       string
	FinishReason    string
	InputTokens     int
	OutputTokens    int
	ReasoningTokens int
	ContentChars    int
	ReasoningChars  int
}

// SetUsage keeps absent or inconsistent provider counters distinct from zero.
func (d *TextDiagnostic) SetUsage(input, output, reasoning, hit, miss *int) {
	if input != nil && *input >= 0 {
		d.InputTokens, d.InputKnown = *input, true
	}
	if output != nil && *output >= 0 {
		d.OutputTokens, d.OutputKnown = *output, true
	}
	if reasoning != nil && *reasoning >= 0 && d.OutputKnown && *reasoning <= d.OutputTokens {
		d.ReasoningTokens, d.ReasoningKnown = *reasoning, true
	}
	if hit != nil && *hit >= 0 && d.InputKnown && *hit <= d.InputTokens && (miss == nil || *miss == d.InputTokens-*hit) {
		d.CacheHitTokens, d.CacheMissTokens, d.CacheKnown = *hit, d.InputTokens-*hit, true
	}
}

type TextCallError struct {
	Diagnostic TextDiagnostic
	Cause      error
}

func (e *TextCallError) Error() string {
	return fmt.Sprintf("model text call: code=%s status=%d", e.Diagnostic.Code, e.Diagnostic.HTTPStatus)
}
func (e *TextCallError) Unwrap() error { return e.Cause }

func TextErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var call *TextCallError
	if errors.As(err, &call) && call.Diagnostic.Code != "" {
		return call.Diagnostic.Code
	}
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &network) && network.Timeout():
		return "timeout"
	case errors.Is(err, ErrTextOutputTooLarge), errors.Is(err, ErrTextResponseTooLarge):
		return "output_limit"
	case errors.Is(err, ErrInvalidTextResponse):
		return "invalid_response"
	case errors.Is(err, ErrTextInputTooLarge):
		return "input_limit"
	case errors.Is(err, ErrInvalidTextRequest):
		return "invalid_request"
	case errors.Is(err, ErrTextReasoningUnsupported):
		return "reasoning_unsupported"
	case errors.Is(err, ErrTextFormatUnsupported):
		return "format_unsupported"
	default:
		return "unknown"
	}
}

// Only provider-generated identifier characters are admitted to diagnostics.
func SafeRequestID(value string) string {
	if len(value) == 0 || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return value
}

func SafeFinishReason(value string) string {
	switch value {
	case "stop", "length", "content_filter", "tool_calls", "completed", "incomplete", "failed", "max_output_tokens":
		return value
	default:
		return "unknown"
	}
}
