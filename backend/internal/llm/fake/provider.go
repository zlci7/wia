package fake

import (
	"context"

	"gameagent/backend/internal/model"
)

var _ model.Provider = (*Provider)(nil)

type Provider struct{}

func NewProvider() Provider {
	return Provider{}
}

// Generate 返回一个固定 ToolCall，用于验证 Runtime 到 Adapter 的闭环。
func (Provider) Generate(ctx context.Context, req model.Request) (model.Response, error) {
	for _, tool := range req.Tools {
		if tool.Name == "emote" {
			return newToolCall("emote", map[string]any{
				"emote": "happy",
			})
		}
	}

	return newToolCall("speak", map[string]any{
		"text": "Hello from GameAgent Runtime by zlc",
	})
}

func newToolCall(name string, arguments map[string]any) (model.Response, error) {
	return model.Response{
		Decision: model.ModelDecision{
			ToolCalls: []model.ToolCall{
				{
					ID:        "fake_call_1",
					Name:      name,
					Arguments: arguments,
				},
			},
			Control: model.ControlDirective{Kind: model.ControlSettle},
		},
	}, nil
}
