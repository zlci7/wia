package deepseek

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gameagent/backend/internal/model"
)

func TestExplicitTextTransportKeepsObservationIndependent(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, observe := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/observe=%t", streaming, observe), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if string(payload["stream"]) != fmt.Sprint(streaming) {
						t.Errorf("transport selection ignored: %s", payload["stream"])
					}
					if (payload["stream_options"] != nil) != streaming {
						t.Error("usage streaming option mismatches transport")
					}
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"实际推理\",\"content\":\"完整回应\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						fmt.Fprint(w, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","reasoning_content":"实际推理","content":"完整回应"}}]}`)
					}
				}))
				defer server.Close()
				var observed string
				request := model.TextRequest{Input: "test", Streaming: &streaming}
				if observe {
					request.OnDelta = func(delta model.TextDelta) { observed += delta.Reasoning }
				}
				response, err := NewProvider("test", "test", WithBaseURL(server.URL)).GenerateText(t.Context(), request)
				if err != nil || response.Text != "完整回应" {
					t.Fatalf("response: %+v %v", response, err)
				}
				if observe && observed != "实际推理" || !observe && observed != "" {
					t.Fatalf("observer: %q", observed)
				}
			})
		}
	}
}
