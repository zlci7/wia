package deepseek

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gameagent/backend/internal/model"
)

func TestExplicitReasoningPayload(t *testing.T) {
	for _, mode := range []model.ReasoningMode{model.ReasoningDefault, model.ReasoningOff, model.ReasoningLow, model.ReasoningHigh} {
		t.Run(string(mode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				var thinking struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(payload["thinking"], &thinking)
				var effort string
				_ = json.Unmarshal(payload["reasoning_effort"], &effort)
				switch mode {
				case model.ReasoningDefault:
					if payload["thinking"] != nil || payload["reasoning_effort"] != nil {
						t.Error("default changed")
					}
				case model.ReasoningOff:
					if thinking.Type != "disabled" || effort != "" {
						t.Error("thinking was not disabled")
					}
				default:
					if thinking.Type != "enabled" || effort != string(mode) {
						t.Error("explicit effort was lost")
					}
				}
				_, _ = io.WriteString(w, textSuccessBody)
			}))
			defer server.Close()
			p := NewProvider("test", "deepseek-v4-flash", WithBaseURL(server.URL))
			if _, err := p.GenerateText(t.Context(), model.TextRequest{Input: "facts", Reasoning: mode}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
