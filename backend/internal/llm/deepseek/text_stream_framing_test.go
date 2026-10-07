package deepseek

import (
	"strings"
	"testing"

	"gameagent/backend/internal/model"
)

func TestTextStreamFramingDoesNotConsumeDecodedTextLimit(t *testing.T) {
	wire := ""
	for range 30 {
		wire += "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"想\"}}]}\n\n"
	}
	wire += "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"回应\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	deltas := 0
	req, _ := model.ValidateTextRequest(model.TextRequest{Input: "test", MaxResponseBytes: 256, OnDelta: func(model.TextDelta) { deltas++ }})
	resp, err := parseStreamingTextResponse(t.Context(), strings.NewReader(wire), req, &model.TextDiagnostic{})
	if err != nil || resp.Text != "回应" || deltas != 31 {
		t.Fatalf("SSE framing: response=%+v deltas=%d err=%v", resp, deltas, err)
	}
}
