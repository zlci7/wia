package model

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// TextDiagnostic contains only bounded transport/validation metadata, never text.
type TextDiagnostic struct {
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
