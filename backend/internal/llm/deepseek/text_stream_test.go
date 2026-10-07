package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/model"
)

func TestTextStreamReportsReasoningBeforeFinalResponse(t *testing.T) {
	first := make(chan model.TextDelta, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream  bool `json:"stream"`
			Options struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || !request.Stream || !request.Options.IncludeUsage {
			t.Error("stream and usage not requested")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"模型返回的思考\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"完整回应\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":8,\"completion_tokens_details\":{\"reasoning_tokens\":5},\"prompt_cache_hit_tokens\":2,\"prompt_cache_miss_tokens\":10}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result := make(chan model.TextResponse, 1)
	failed := make(chan error, 1)
	go func() {
		resp, err := NewProvider("test", "test", WithBaseURL(server.URL)).GenerateText(ctx, model.TextRequest{Input: "test", OnDelta: func(delta model.TextDelta) {
			if delta.Reasoning != "" {
				first <- delta
			}
		}})
		result <- resp
		failed <- err
	}()
	select {
	case delta := <-first:
		if delta.Reasoning != "模型返回的思考" || delta.Text != "" {
			t.Fatal("reasoning was mixed with final text")
		}
	case <-ctx.Done():
		t.Fatal("reasoning not delivered during generation")
	}
	close(release)
	resp := <-result
	if err := <-failed; err != nil {
		t.Fatal(err)
	}
	if resp.Text != "完整回应" || resp.Diagnostic.ReasoningTokens != 5 || resp.Diagnostic.InputTokens != 12 || resp.Diagnostic.FinishReason != "stop" {
		t.Fatalf("final response: %+v", resp)
	}
}

func TestTextStreamRejectsIncompleteAndOversizedResponses(t *testing.T) {
	for _, test := range []struct {
		name, wire string
		limit      int
		want       error
	}{
		{"dropped", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n", 1024, model.ErrInvalidTextResponse},
		{"done-without-finish", "data: [DONE]\n\n", 1024, model.ErrInvalidTextResponse},
		{"tool", "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{}]}}]}\n\n", 1024, model.ErrInvalidTextResponse},
		{"wire-limit", ":" + strings.Repeat("x", 3000) + "\n\n", 128, model.ErrTextResponseTooLarge},
		{"decoded-limit", "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"" + strings.Repeat("x", 130) + "\"}}]}\n\n", 128, model.ErrTextResponseTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, test.wire)
			}))
			defer server.Close()
			_, err := NewProvider("test", "test", WithBaseURL(server.URL)).GenerateText(t.Context(), model.TextRequest{Input: "test", MaxResponseBytes: test.limit, OnDelta: func(model.TextDelta) {}})
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want=%v", err, test.want)
			}
		})
	}
}
