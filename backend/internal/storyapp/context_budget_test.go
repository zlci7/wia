package storyapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"gameagent/backend/internal/model"
)

func TestContextBudgetPreservesRequiredAndDropsWholeOldGroups(t *testing.T) {
	c := ContextComposer{Window: model.WindowLimits{ContextTokens: 500, OutputTokens: 64}}
	m := contextMaterial{Required: "玩家本轮原文与新刺激", RequiredSources: []string{"current"}, Optional: []contextSection{
		{Name: "old", Text: strings.Repeat("旧事实", 400), Sources: []string{"old-intent", "old-result"}},
		{Name: "recent", Text: "最近的对话", Sources: []string{"recent"}},
		{Name: "recent", Text: "最近的对话", Sources: []string{"recent"}},
	}}
	req, report, err := c.Build(m, "规则", 64)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Input, m.Required) || strings.Contains(req.Input, "旧事实") || !strings.Contains(req.Input, "最近的对话") {
		t.Fatal("invalid selection")
	}
	if report.Excluded != 1 || report.ExcludedSources != 2 || report.Duplicates != 1 || !report.RequiredComplete || !report.WindowKnown {
		t.Fatalf("%+v", report)
	}
	if report.InputTokens+64 > 500 || req.MaxInputTokens != 436 {
		t.Fatalf("%+v", report)
	}
	if _, err := model.ValidateTextRequest(req); err != nil {
		t.Fatal(err)
	}
}

func TestContextBudgetRequiredOverflowAndUnknownWindow(t *testing.T) {
	c := ContextComposer{}
	_, report, err := c.Build(contextMaterial{Required: strings.Repeat("不可删除的原话", 8000)}, "规则", 64)
	if !errors.Is(err, ErrContextCapacity) || report.RequiredComplete || report.Failure != "required_content" {
		t.Fatalf("%+v %v", report, err)
	}
	req, report, err := c.Build(contextMaterial{Required: "当前内容"}, "规则", 64)
	if err != nil || report.WindowKnown || req.MaxInputTokens != 12000 {
		t.Fatalf("%+v %v", report, err)
	}
	_, report, err = (ContextComposer{Window: model.WindowLimits{ContextTokens: 500, OutputTokens: 32}}).Build(contextMaterial{Required: "当前内容"}, "规则", 64)
	if !errors.Is(err, ErrContextCapacity) || report.Failure != "output_reservation" {
		t.Fatalf("%+v %v", report, err)
	}
}

type contextCaptureGenerator struct {
	requests []model.TextRequest
	response string
}

func (g *contextCaptureGenerator) GenerateText(_ context.Context, req model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, req)
	return model.TextResponse{Text: g.response}, nil
}

func TestRepairIsRecomposedAndCanFailBeforeSecondProviderRequest(t *testing.T) {
	material := contextMaterial{System: "只输出JSON", Required: "玩家本轮原文"}
	base := framedContextTokens(model.TextRequest{System: material.System, Input: material.Required})
	provider := &contextCaptureGenerator{response: "not json"}
	logger := &recordingLogger{}
	g := &contextGenerator{TextGenerator: provider, material: material, logger: logger, composer: ContextComposer{Window: model.WindowLimits{ContextTokens: base + 64, OutputTokens: 64}}}
	var result map[string]any
	_, err := generateJSONMetrics(context.Background(), g, material.System, material.Required, &result, 64, "answer")
	if !errors.Is(err, ErrContextCapacity) || len(provider.requests) != 1 {
		t.Fatalf("calls=%d err=%v", len(provider.requests), err)
	}
	if !strings.Contains(logger.String(), "success=false") || !strings.Contains(logger.String(), "failure=\"required_content\"") {
		t.Fatal(logger.String())
	}
	if strings.Contains(logger.String(), material.Required) {
		t.Fatal("prompt in logs")
	}
}

func TestPersonalCausalGroupsAreIndivisible(t *testing.T) {
	s := contextFixture()
	s.Sources = map[string]sourceMetadata{"action": {Seq: 1, Kind: "npc_action_intent"}, "action:result:1": {Seq: 2, Kind: "npc_action_result"}}
	s.Memories["npc:innkeeper"] = []Memory{{SourceEventID: "action", Content: "我想递茶"}}
	s.Perceptions["npc:innkeeper"] = []Perception{{SourceEventID: "action:result:1", Content: "茶已经递完", SourceType: "action_succeeded"}}
	groups := personalSections(s, "npc:innkeeper")
	if len(groups) != 1 || len(groups[0].Sources) != 2 || !strings.Contains(groups[0].Text, "茶已经递完") || !strings.Contains(groups[0].Text, "我想递茶") {
		t.Fatalf("%+v", groups)
	}
}

func TestContextComposerParallelWorldsHaveIndependentReports(t *testing.T) {
	var wg sync.WaitGroup
	for _, world := range []string{"A", "B"} {
		for _, recipient := range []string{"player", "npc:innkeeper"} {
			wg.Add(1)
			go func(world, recipient string) {
				defer wg.Done()
				c := ContextComposer{Scope: ContextScope{World: world, Recipient: recipient, Stage: 2}}
				for i := 0; i < 30; i++ {
					req, report, err := c.Build(contextMaterial{Required: world + recipient}, "规则", 64)
					if err != nil || req.Input != world+recipient || report.Scope.World != world || report.Scope.Recipient != recipient {
						t.Errorf("mixed context: %+v %v", report, err)
					}
				}
			}(world, recipient)
		}
	}
	wg.Wait()
}
