package storyapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gameagent/runtime/internal/model"
)

func TestReasoningBudgetPreservesVisibleLimitAndWindow(t *testing.T) {
	for _, tc := range []struct {
		name           string
		window         model.WindowLimits
		reserve, input int
	}{
		{"configured", model.WindowLimits{ContextTokens: 131072, OutputTokens: 12800}, 8192, 12000},
		{"bounded", model.WindowLimits{ContextTokens: 12000, OutputTokens: 6000}, 1904, 6000},
		{"unknown", model.WindowLimits{}, 8192, 12000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := ContextComposer{Window: tc.window, ReasoningReserve: 8192}
			req, report, err := c.Build(contextMaterial{Required: "本轮玩家原文"}, "规则", 4096)
			if err != nil || req.MaxOutputTokens != 4096 || req.ReasoningReserveTokens != tc.reserve || req.MaxInputTokens != tc.input || report.TotalOutputTokens != 4096+tc.reserve || report.WindowKnown != (tc.name != "unknown") {
				t.Fatalf("request=%+v report=%+v err=%v", req, report, err)
			}
			if err := model.ValidateTextResponse(req, model.TextResponse{Text: strings.Repeat("中", 5000)}); !errors.Is(err, model.ErrTextOutputTooLarge) {
				t.Fatalf("reasoning expanded visible limit: %v", err)
			}
		})
	}
}

type reasoningCapture struct {
	calls   int
	request model.TextRequest
}

func (g *reasoningCapture) TextReasoningReserve() int { return 8192 }
func (g *reasoningCapture) ModelWindow() model.WindowLimits {
	return model.WindowLimits{ContextTokens: 5000, OutputTokens: 4500}
}
func (g *reasoningCapture) GenerateText(_ context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.calls++
	g.request = r
	return model.TextResponse{Text: "ok"}, nil
}

func TestReasoningRepairRechecksRequiredCapacity(t *testing.T) {
	provider := &reasoningCapture{}
	app := newTestApp(t, &scriptedGenerator{})
	g := app.contextGenerator(provider, contextMaterial{Required: "本轮原文"}, worldSnapshot{}, Run{}, "test", "player", 1, "test.v1")
	if _, err := g.GenerateText(context.Background(), model.TextRequest{System: "规则", MaxOutputTokens: 4096}); err != nil {
		t.Fatal(err)
	}
	if provider.request.ReasoningReserveTokens != 404 {
		t.Fatalf("capability lost: %+v", provider.request)
	}
	_, err := g.GenerateText(context.Background(), model.TextRequest{System: strings.Repeat("修复格式", 500), MaxOutputTokens: 4096})
	if !errors.Is(err, ErrContextCapacity) || provider.calls != 1 {
		t.Fatalf("repair sent oversized request: calls=%d err=%v", provider.calls, err)
	}
}
