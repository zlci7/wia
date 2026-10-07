package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"gameagent/backend/internal/model"
)

var _ model.TextGenerator = (*Provider)(nil)

func (p *Provider) TextReasoningReserve() int {
	name := strings.ToLower(strings.TrimSpace(p.model))
	if strings.HasPrefix(name, "deepseek-v4-") || name == "deepseek-reasoner" {
		return 8192
	}
	return 0
}

func (p *Provider) GenerateText(ctx context.Context, req model.TextRequest) (result model.TextResponse, callErr error) {
	diagnostic := model.TextDiagnostic{Provider: "deepseek", Model: p.model}
	defer func() {
		if callErr != nil {
			if diagnostic.Code == "" {
				diagnostic.Code = model.TextErrorCode(callErr)
			}
			callErr = &model.TextCallError{Diagnostic: diagnostic, Cause: callErr}
		} else {
			result.Diagnostic = diagnostic
		}
	}()
	if err := ctx.Err(); err != nil {
		return model.TextResponse{}, err
	}
	req, err := model.ValidateTextRequest(req)
	if err != nil {
		return model.TextResponse{}, err
	}
	if p.apiKey == "" {
		return model.TextResponse{}, errors.New("deepseek api key is empty")
	}

	messages := make([]map[string]string, 0, 2)
	if req.System != "" {
		messages = append(messages, map[string]string{"role": "system", "content": req.System})
	}
	messages = append(messages, map[string]string{"role": "user", "content": req.Input})
	payload := map[string]any{
		"model":      p.model,
		"messages":   messages,
		"stream":     req.OnDelta != nil,
		"max_tokens": req.TotalOutputTokens(),
	}
	if req.OnDelta != nil {
		payload["stream_options"] = map[string]bool{"include_usage": true}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return model.TextResponse{}, model.ErrInvalidTextRequest
	}
	inputTokens := model.FramedTextInputTokens(req)
	if p.window.ContextTokens > 0 {
		if err := p.window.Check(inputTokens, req.TotalOutputTokens()); err != nil {
			return model.TextResponse{}, err
		}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return model.TextResponse{}, textRequestError(ctx, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return model.TextResponse{}, textRequestError(ctx, err)
	}
	defer httpResp.Body.Close()
	diagnostic.HTTPStatus = httpResp.StatusCode
	diagnostic.RequestID = model.SafeRequestID(httpResp.Header.Get("x-request-id"))
	if err := ctx.Err(); err != nil {
		return model.TextResponse{}, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		diagnostic.Code = "provider_http"
		return model.TextResponse{}, fmt.Errorf("deepseek response failed: status=%d", httpResp.StatusCode)
	}
	if req.OnDelta != nil && strings.Contains(strings.ToLower(httpResp.Header.Get("Content-Type")), "text/event-stream") {
		resp, err := parseStreamingTextResponse(ctx, httpResp.Body, req, &diagnostic)
		if err != nil {
			return model.TextResponse{}, err
		}
		if err := model.ValidateTextResponse(req, resp); err != nil {
			return model.TextResponse{}, err
		}
		return resp, ctx.Err()
	}

	readLimit := int64(req.MaxResponseBytes)
	if readLimit < 1<<63-1 {
		readLimit++
	}
	data, err := io.ReadAll(io.LimitReader(httpResp.Body, readLimit))
	if err != nil {
		return model.TextResponse{}, textRequestError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return model.TextResponse{}, err
	}
	if len(data) > req.MaxResponseBytes {
		return model.TextResponse{}, model.ErrTextResponseTooLarge
	}
	var envelope struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning_content"`
			} `json:"message"`
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
		Status string `json:"status"`
	}
	_ = json.Unmarshal(data, &envelope)
	finish := envelope.Status
	if len(envelope.Choices) > 0 {
		finish = envelope.Choices[0].FinishReason
		diagnostic.ContentChars = utf8.RuneCountInString(envelope.Choices[0].Message.Content)
		diagnostic.ReasoningChars = utf8.RuneCountInString(envelope.Choices[0].Message.Reasoning)
		if req.OnDelta != nil {
			req.OnDelta(model.TextDelta{Reasoning: envelope.Choices[0].Message.Reasoning, Text: envelope.Choices[0].Message.Content})
		}
	}
	diagnostic.SetUsage(envelope.Usage.Input, envelope.Usage.Output, envelope.Usage.Details.Reasoning, envelope.Usage.Hit, envelope.Usage.Miss)
	diagnostic.FinishReason = model.SafeFinishReason(finish)
	if finish == "length" || finish == "incomplete" {
		diagnostic.Code = "output_incomplete"
	}
	resp, err := parseTextResponse(data)
	if err != nil {
		return model.TextResponse{}, err
	}
	if err := model.ValidateTextResponse(req, resp); err != nil {
		if strings.TrimSpace(resp.Text) == "" {
			diagnostic.Code = "empty_response"
		}
		if errors.Is(err, model.ErrInvalidTextResponse) {
			return model.TextResponse{}, fmt.Errorf("%w: response text is empty or invalid UTF-8", err)
		}
		return model.TextResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.TextResponse{}, err
	}
	return resp, nil
}

func parseTextResponse(data []byte) (model.TextResponse, error) {
	var raw struct {
		Error   *json.RawMessage `json:"error"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role         string            `json:"role"`
				Content      string            `json:"content"`
				Refusal      *json.RawMessage  `json:"refusal"`
				ToolCalls    []json.RawMessage `json:"tool_calls"`
				FunctionCall *json.RawMessage  `json:"function_call"`
			} `json:"message"`
		} `json:"choices"`
	}
	if model.ValidateTextJSON(data) != nil {
		return model.TextResponse{}, fmt.Errorf("%w: response body is not strict JSON", model.ErrInvalidTextResponse)
	}
	if json.Unmarshal(data, &raw) != nil {
		return model.TextResponse{}, fmt.Errorf("%w: response body does not match the chat schema", model.ErrInvalidTextResponse)
	}
	if raw.Error != nil {
		return model.TextResponse{}, fmt.Errorf("%w: provider returned an error object", model.ErrInvalidTextResponse)
	}
	if len(raw.Choices) != 1 {
		return model.TextResponse{}, fmt.Errorf("%w: choice count is %d", model.ErrInvalidTextResponse, len(raw.Choices))
	}
	choice := raw.Choices[0]
	if choice.FinishReason != "stop" {
		return model.TextResponse{}, fmt.Errorf("%w: finish_reason is %q", model.ErrInvalidTextResponse, model.SafeFinishReason(choice.FinishReason))
	}
	if choice.Message.Role != "assistant" {
		return model.TextResponse{}, fmt.Errorf("%w: message role is %q", model.ErrInvalidTextResponse, "invalid")
	}
	if choice.Message.Refusal != nil {
		return model.TextResponse{}, fmt.Errorf("%w: response contains a refusal", model.ErrInvalidTextResponse)
	}
	if len(choice.Message.ToolCalls) != 0 || choice.Message.FunctionCall != nil {
		return model.TextResponse{}, fmt.Errorf("%w: response contains tool calls", model.ErrInvalidTextResponse)
	}
	return model.TextResponse{Text: choice.Message.Content}, nil
}

func textRequestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return &model.TextCallError{Diagnostic: model.TextDiagnostic{Code: "network"}, Cause: errors.New("model transport failed")}
}
