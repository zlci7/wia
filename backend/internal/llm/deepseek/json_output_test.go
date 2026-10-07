package deepseek

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gameagent/backend/internal/model"
)

func TestJSONOutputIsExplicitAndKeepsTransportIndependent(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				var format struct{ Type string }
				_ = json.Unmarshal(payload["response_format"], &format)
				if enabled && format.Type != "json_object" || !enabled && payload["response_format"] != nil {
					t.Error("JSON output selection changed")
				}
				var streaming bool
				_ = json.Unmarshal(payload["stream"], &streaming)
				if streaming != stream {
					t.Error("format changed transport")
				}
				_, _ = io.WriteString(w, textSuccessBody)
			}))
			p := NewProvider("test", "test", WithBaseURL(server.URL))
			_, err := p.GenerateText(t.Context(), model.TextRequest{Input: "JSON facts", JSON: enabled, Streaming: &stream})
			server.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
