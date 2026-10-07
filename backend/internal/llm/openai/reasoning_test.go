package openai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gameagent/backend/internal/model"
)

func TestExplicitReasoningFailsBeforeUnsupportedProviderRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	provider := NewProvider("test", "test", WithBaseURL(server.URL))
	_, err := provider.GenerateText(t.Context(), model.TextRequest{Input: "facts", Reasoning: model.ReasoningOff})
	if !errors.Is(err, model.ErrTextReasoningUnsupported) || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
