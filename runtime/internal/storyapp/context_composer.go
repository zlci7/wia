package storyapp

import (
	"context"
	"sort"
	"strings"

	"gameagent/runtime/internal/model"
)

type contextSection struct {
	Name    string
	Text    string
	Sources []string
}

type contextMaterial struct {
	System   string
	Required string
	Optional []contextSection // oldest first; a section is an indivisible causal group
}

type ContextScope struct {
	Owner, Game, World, Run, Purpose, Recipient, Template string
	Attempt, Stage                                        int
	Epoch, SceneVersion                                   int64
}

type ContextBuildReport struct {
	Scope                                                    ContextScope
	Sections                                                 []string
	Sources, Excluded, Duplicates, InputTokens, OutputTokens int
	RequiredComplete                                         bool
	WindowKnown                                              bool
}

// ContextComposer has no database, model, or mutable cross-request state.
type ContextComposer struct {
	Scope  ContextScope
	Window model.WindowLimits
}

func (c ContextComposer) Build(material contextMaterial, system string, output int) (model.TextRequest, ContextBuildReport, error) {
	report := ContextBuildReport{Scope: c.Scope, OutputTokens: output, Sections: []string{"required"}, RequiredComplete: true}
	var parts []string
	for _, section := range material.Optional {
		parts = append(parts, section.Text)
		report.Sections = append(report.Sections, section.Name)
		report.Sources += len(section.Sources)
	}
	parts = append(parts, material.Required)
	req := model.TextRequest{System: system, Input: strings.Join(parts, "\n"), MaxInputTokens: 12000, MaxOutputTokens: output, MaxResponseBytes: 1 << 20}
	return req, report, nil
}

type contextGenerator struct {
	model.TextGenerator
	composer ContextComposer
	material contextMaterial
	logger   Logger
}

func (a *App) contextGenerator(generator model.TextGenerator, material contextMaterial, snapshot worldSnapshot, run Run, purpose, recipient string, stage int, template string) model.TextGenerator {
	window := model.WindowLimits{}
	if provider, ok := generator.(model.WindowProvider); ok {
		window = provider.ModelWindow()
	}
	return &contextGenerator{TextGenerator: generator, material: material, logger: a.logger, composer: ContextComposer{Scope: ContextScope{Owner: a.userID, Game: snapshot.Summary.GameID, World: snapshot.Summary.WorldID, Run: run.RunID, Attempt: run.Attempt, Stage: stage, Epoch: run.BaseContextEpoch, SceneVersion: snapshot.SceneVersion, Purpose: purpose, Recipient: recipient, Template: template}, Window: window}}
}

func (g *contextGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	req, report, err := g.composer.Build(g.material, request.System, request.MaxOutputTokens)
	if g.logger != nil {
		s := report.Scope
		g.logger.Printf("story context built: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d epoch=%d scene_version=%d template=%q sections=%q sources=%d excluded=%d duplicates=%d input_tokens=%d output_tokens=%d required_complete=%t window_known=%t success=%t", s.World, s.Run, s.Attempt, s.Purpose, s.Recipient, s.Stage, s.Epoch, s.SceneVersion, s.Template, strings.Join(report.Sections, ","), report.Sources, report.Excluded, report.Duplicates, report.InputTokens, report.OutputTokens, report.RequiredComplete, report.WindowKnown, err == nil)
	}
	if err != nil {
		return model.TextResponse{}, err
	}
	return g.TextGenerator.GenerateText(ctx, req)
}

func dialogueSections(snapshot worldSnapshot) []contextSection {
	var groups []contextSection
	for _, event := range snapshot.Dialogue {
		if len(groups) == 0 || groups[len(groups)-1].Name != "dialogue:"+event.RunID {
			groups = append(groups, contextSection{Name: "dialogue:" + event.RunID})
		}
		i := len(groups) - 1
		part := snapshot
		part.Dialogue = []Event{event}
		groups[i].Text += "此前已提交对话：" + dialogueContext(part)
		groups[i].Sources = append(groups[i].Sources, event.EventID)
	}
	return groups
}

func narrativeSections(messages []Message) []contextSection {
	var sections []contextSection
	for _, message := range messages {
		if message.Kind == "narrative" && cleanText(message.Content) != "" {
			sections = append(sections, contextSection{Name: "player_history", Text: "玩家可见历史正文（表现参考，不是本轮事实）：" + message.Content, Sources: []string{message.MessageID}})
		}
	}
	if len(sections) > 8 {
		sections = sections[len(sections)-8:]
	}
	return sections
}

func personalSections(snapshot worldSnapshot, recipient string) []contextSection {
	groups := map[string]contextSection{}
	for _, p := range snapshot.Perceptions[recipient] {
		group := groups[p.SourceEventID]
		group.Name = "personal_history"
		group.Sources = []string{p.SourceEventID}
		group.Text += "此前已提交个人感知：" + joinPerceptions(snapshot, []Perception{p}) + "\n"
		groups[p.SourceEventID] = group
	}
	for _, m := range snapshot.Memories[recipient] {
		group := groups[m.SourceEventID]
		group.Name = "personal_history"
		group.Sources = []string{m.SourceEventID}
		group.Text += "本人主观记忆（不是世界事实）：" + m.Kind + "：" + m.Content + "\n"
		groups[m.SourceEventID] = group
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := snapshot.Sources[ids[i]], snapshot.Sources[ids[j]]
		if a.Seq == b.Seq {
			return ids[i] < ids[j]
		}
		return a.Seq < b.Seq
	})
	result := make([]contextSection, 0, len(ids))
	for _, id := range ids {
		result = append(result, groups[id])
	}
	return result
}
