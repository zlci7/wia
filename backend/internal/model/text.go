package model

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"gameagent/backend/internal/tokenestimate"
)

const (
	DefaultTextMaxInputTokens   = 32768
	DefaultTextMaxOutputTokens  = 2048
	DefaultTextMaxResponseBytes = 1 << 20
)

var (
	ErrInvalidTextRequest       = errors.New("invalid text request")
	ErrTextInputTooLarge        = errors.New("text input exceeds token limit")
	ErrTextOutputTooLarge       = errors.New("text output exceeds token limit")
	ErrTextResponseTooLarge     = errors.New("text response exceeds byte limit")
	ErrInvalidTextResponse      = errors.New("invalid or incomplete text response")
	ErrTextReasoningUnsupported = errors.New("text reasoning selection is unsupported")
	ErrTextFormatUnsupported    = errors.New("text JSON output selection is unsupported")
)

// ReasoningMode selects generation effort independently of transport. Empty
// preserves the provider default for existing callers.
type ReasoningMode string

const (
	ReasoningDefault ReasoningMode = ""
	ReasoningOff     ReasoningMode = "off"
	ReasoningLow     ReasoningMode = "low"
	ReasoningHigh    ReasoningMode = "high"
)

func (m ReasoningMode) Valid() bool {
	return m == ReasoningDefault || m == ReasoningOff || m == ReasoningLow || m == ReasoningHigh
}

// TextRequest is a tool-free generation request. Zero Max* limits use defaults;
// a zero reasoning reserve allocates no additional tokens. Visible token limits
// use tokenestimate. MaxResponseBytes bounds a non-streamed envelope or total
// decoded streaming text; providers also bound streaming transport framing.
type TextRequest struct {
	System                 string
	Input                  string
	MaxInputTokens         int
	MaxOutputTokens        int
	ReasoningReserveTokens int
	MaxResponseBytes       int
	Streaming              *bool           `json:"streaming,omitempty"`
	Reasoning              ReasoningMode   `json:"reasoning,omitempty"`
	JSON                   bool            `json:"json,omitempty"`
	OnDelta                func(TextDelta) `json:"-"`
}

// TextDelta carries provider-returned text while a response is incomplete.
// Providers invoke OnDelta synchronously before GenerateText returns. Deltas
// are observation only: consumers still validate the final TextResponse.
type TextDelta struct {
	Reasoning string
	Text      string
}

type TextResponse struct {
	Text       string
	Diagnostic TextDiagnostic
}

type TextGenerator interface {
	GenerateText(context.Context, TextRequest) (TextResponse, error)
}

type TextStreamingProvider interface{ SupportsTextStreaming() bool }

// Streams resolves explicit transport selection independently from observation.
func (r TextRequest) Streams() bool {
	if r.Streaming != nil {
		return *r.Streaming
	}
	return r.OnDelta != nil
}

// TextReasoningProvider declares an additional internal-reasoning allowance.
// MaxOutputTokens continues to bound the visible answer.
type TextReasoningProvider interface{ TextReasoningReserve() int }

func (r TextRequest) TotalOutputTokens() int { return r.MaxOutputTokens + r.ReasoningReserveTokens }

// ValidateTextRequest applies defaults and checks the complete framed input.
func ValidateTextRequest(req TextRequest) (TextRequest, error) {
	req = textRequestDefaults(req)
	if req.MaxInputTokens < 0 || req.MaxOutputTokens < 0 || req.MaxResponseBytes < 0 || req.ReasoningReserveTokens < 0 || req.ReasoningReserveTokens > int(^uint(0)>>1)-req.MaxOutputTokens ||
		!req.Reasoning.Valid() || req.Reasoning == ReasoningOff && req.ReasoningReserveTokens != 0 ||
		!utf8.ValidString(req.System) || !utf8.ValidString(req.Input) || strings.TrimSpace(req.Input) == "" {
		return TextRequest{}, ErrInvalidTextRequest
	}

	systemTokens := tokenestimate.EstimateText(req.System)
	if systemTokens > req.MaxInputTokens || tokenestimate.EstimateText(req.Input) > req.MaxInputTokens-systemTokens {
		return TextRequest{}, ErrTextInputTooLarge
	}

	if FramedTextInputTokens(req) > req.MaxInputTokens {
		return TextRequest{}, ErrTextInputTooLarge
	}
	return req, nil
}

// FramedTextInputTokens counts escaped content and native message framing. HTTP
// transport options such as model names and output limits do not consume input.
func FramedTextInputTokens(req TextRequest) int {
	n, _ := tokenestimate.EstimateStableJSON(map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.Input},
		},
	})
	return n + 16
}

// ValidateTextResponse checks text without trimming or truncating its content.
func ValidateTextResponse(req TextRequest, resp TextResponse) error {
	req = textRequestDefaults(req)
	if req.MaxInputTokens < 0 || req.MaxOutputTokens < 0 || req.MaxResponseBytes < 0 {
		return ErrInvalidTextRequest
	}
	if !utf8.ValidString(resp.Text) || strings.TrimSpace(resp.Text) == "" {
		return ErrInvalidTextResponse
	}
	if len(resp.Text) > req.MaxResponseBytes {
		return ErrTextResponseTooLarge
	}
	if tokenestimate.EstimateText(resp.Text) > req.MaxOutputTokens {
		return ErrTextOutputTooLarge
	}
	return nil
}

func textRequestDefaults(req TextRequest) TextRequest {
	if req.MaxInputTokens == 0 {
		req.MaxInputTokens = DefaultTextMaxInputTokens
	}
	if req.MaxOutputTokens == 0 {
		req.MaxOutputTokens = DefaultTextMaxOutputTokens
	}
	if req.MaxResponseBytes == 0 {
		req.MaxResponseBytes = DefaultTextMaxResponseBytes
	}
	return req
}
