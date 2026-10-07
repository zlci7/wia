package deepseek

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"gameagent/backend/internal/model"
)

func parseStreamingTextResponse(ctx context.Context, reader io.Reader, req model.TextRequest, diagnostic *model.TextDiagnostic) (model.TextResponse, error) {
	// SSE repeats its metadata per token. Bound decoded text independently from
	// transport framing so the non-streamed envelope cap does not cut off thoughts.
	wireLimit := min(int64(req.MaxResponseBytes), (math.MaxInt64-1)/16) * 16
	limited := &io.LimitedReader{R: reader, N: wireLimit + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, min(4096, wireLimit+1)), int(min(wireLimit+1, int64(math.MaxInt))))
	var text strings.Builder
	var data []string
	finished, done := false, false
	decodedBytes := 0
	consume := func() error {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if payload == "[DONE]" {
			done = true
			return nil
		}
		if model.ValidateTextJSON([]byte(payload)) != nil {
			return model.ErrInvalidTextResponse
		}
		var chunk struct {
			Error   *json.RawMessage `json:"error"`
			Choices []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Role         string            `json:"role"`
					Content      string            `json:"content"`
					Reasoning    string            `json:"reasoning_content"`
					Refusal      *json.RawMessage  `json:"refusal"`
					ToolCalls    []json.RawMessage `json:"tool_calls"`
					FunctionCall *json.RawMessage  `json:"function_call"`
				} `json:"delta"`
			} `json:"choices"`
			Usage struct {
				Input   *int `json:"prompt_tokens"`
				Output  *int `json:"completion_tokens"`
				Hit     *int `json:"prompt_cache_hit_tokens"`
				Miss    *int `json:"prompt_cache_miss_tokens"`
				Details struct {
					Reasoning *int `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil || chunk.Error != nil || len(chunk.Choices) > 1 {
			return model.ErrInvalidTextResponse
		}
		diagnostic.SetUsage(chunk.Usage.Input, chunk.Usage.Output, chunk.Usage.Details.Reasoning, chunk.Usage.Hit, chunk.Usage.Miss)
		if len(chunk.Choices) == 0 {
			return nil
		}
		choice := chunk.Choices[0]
		delta := choice.Delta
		if finished || choice.Index != 0 || delta.Role != "" && delta.Role != "assistant" || delta.Refusal != nil || len(delta.ToolCalls) > 0 || delta.FunctionCall != nil {
			return model.ErrInvalidTextResponse
		}
		if !utf8.ValidString(delta.Content) || !utf8.ValidString(delta.Reasoning) {
			return model.ErrInvalidTextResponse
		}
		if len(delta.Content) > req.MaxResponseBytes-decodedBytes || len(delta.Reasoning) > req.MaxResponseBytes-decodedBytes-len(delta.Content) {
			return model.ErrTextResponseTooLarge
		}
		decodedBytes += len(delta.Content) + len(delta.Reasoning)
		text.WriteString(delta.Content)
		diagnostic.ContentChars += utf8.RuneCountInString(delta.Content)
		diagnostic.ReasoningChars += utf8.RuneCountInString(delta.Reasoning)
		if req.OnDelta != nil && (delta.Content != "" || delta.Reasoning != "") {
			req.OnDelta(model.TextDelta{Text: delta.Content, Reasoning: delta.Reasoning})
		}
		if choice.FinishReason != "" {
			finished = true
			diagnostic.FinishReason = model.SafeFinishReason(choice.FinishReason)
			if choice.FinishReason != "stop" {
				if choice.FinishReason == "length" {
					diagnostic.Code = "output_incomplete"
				}
				return model.ErrInvalidTextResponse
			}
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return model.TextResponse{}, err
		}
		if limited.N == 0 {
			return model.TextResponse{}, model.ErrTextResponseTooLarge
		}
		line := scanner.Text()
		if line == "" {
			if err := consume(); err != nil {
				return model.TextResponse{}, err
			}
			if done {
				break
			}
		} else if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	if limited.N == 0 {
		return model.TextResponse{}, model.ErrTextResponseTooLarge
	}
	if err := scanner.Err(); err != nil {
		return model.TextResponse{}, textRequestError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return model.TextResponse{}, err
	}
	if !done || !finished {
		return model.TextResponse{}, fmt.Errorf("%w: unfinished text stream", model.ErrInvalidTextResponse)
	}
	return model.TextResponse{Text: text.String()}, nil
}
