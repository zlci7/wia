package storyapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gameagent/runtime/internal/model"
	"gameagent/runtime/internal/tokenestimate"
)

type contextSection struct {
	Name    string
	Text    string
	Sources []string
}

type contextMaterial struct {
	RecallSources   []string
	PolicyRevision  string
	RequiredSources []string
	System          string
	Required        string
	Optional        []contextSection // oldest first; a section is an indivisible causal group
}

type ContextScope struct {
	Owner, Game, World, Run, Purpose, Recipient, Template string
	PolicyRevision                                        string
	Attempt, Stage                                        int
	Epoch, SceneVersion                                   int64
}

type ContextBuildReport struct {
	RecallIncluded, RecallExcluded                           int
	SelectedSources                                          []string
	ExcludedSources                                          int
	Failure                                                  string
	Scope                                                    ContextScope
	Sections                                                 []string
	Sources, Excluded, Duplicates, InputTokens, OutputTokens int
	RequiredComplete                                         bool
	WindowKnown                                              bool
	ReasoningRequested, ReasoningReserved, TotalOutputTokens int
}

// ContextComposer has no database, model, or mutable cross-request state.
type ContextComposer struct {
	Scope            ContextScope
	Window           model.WindowLimits
	ReasoningReserve int
}

var ErrContextCapacity = errors.New("story context capacity exceeded")

func (c ContextComposer) Build(material contextMaterial, system string, output int) (model.TextRequest, ContextBuildReport, error) {
	report := ContextBuildReport{Scope: c.Scope, OutputTokens: output, Sections: []string{"required"}}
	report.ReasoningRequested = c.ReasoningReserve
	if c.ReasoningReserve < 0 || output <= 0 || c.ReasoningReserve > int(^uint(0)>>1)-output {
		report.Failure = "invalid_capacity"
		return model.TextRequest{}, report, ErrContextCapacity
	}
	reasoning := c.ReasoningReserve
	limit := 12000
	if c.Window != (model.WindowLimits{}) {
		report.WindowKnown = true
		if err := c.Window.Validate(); err != nil {
			report.Failure = "invalid_model_window"
			return model.TextRequest{}, report, fmt.Errorf("%w: invalid model window", ErrContextCapacity)
		}
		if output > c.Window.OutputTokens || output <= 0 {
			report.Failure = "output_reservation"
			return model.TextRequest{}, report, fmt.Errorf("%w: output reservation exceeds model capacity", ErrContextCapacity)
		}
		reasoning = min(reasoning, c.Window.OutputTokens-output)
		limit = min(limit, c.Window.ContextTokens-output-reasoning)
	}
	report.ReasoningReserved = reasoning
	report.TotalOutputTokens = output + reasoning
	if output <= 0 || limit <= 0 {
		report.Failure = "invalid_capacity"
		return model.TextRequest{}, report, ErrContextCapacity
	}
	req := model.TextRequest{System: system, Input: material.Required, MaxInputTokens: limit, MaxOutputTokens: output, ReasoningReserveTokens: reasoning, MaxResponseBytes: 1 << 20}
	report.InputTokens = framedContextTokens(req)
	if _, err := model.ValidateTextRequest(req); err != nil {
		if !errors.Is(err, model.ErrTextInputTooLarge) {
			report.Failure = "invalid_request"
			return model.TextRequest{}, report, err
		}
		report.Failure = "required_content"
		return model.TextRequest{}, report, fmt.Errorf("%w: required input does not fit", ErrContextCapacity)
	}
	report.RequiredComplete = true
	seen := map[string]bool{}
	var sections []contextSection
	// Keep the latest copy of a duplicate, retaining source identity.
	for i := len(material.Optional) - 1; i >= 0; i-- {
		section := material.Optional[i]
		ids := append([]string{}, section.Sources...)
		sort.Strings(ids)
		keyData, _ := json.Marshal([]any{section.Name, section.Text, ids})
		key := string(keyData)
		if seen[key] {
			report.Duplicates++
			continue
		}
		seen[key] = true
		sections = append(sections, section)
	}
	for left, right := 0, len(sections)-1; left < right; left, right = left+1, right-1 {
		sections[left], sections[right] = sections[right], sections[left]
	}
	for {
		parts := make([]string, 0, len(sections)+1)
		for _, section := range sections {
			parts = append(parts, section.Text)
		}
		parts = append(parts, material.Required)
		if len(material.RecallSources) > 0 {
			selected := map[string]bool{}
			for _, section := range sections {
				for _, id := range section.Sources {
					selected[id] = true
				}
			}
			report.RecallIncluded = 0
			for _, id := range material.RecallSources {
				if selected[id] {
					report.RecallIncluded++
				}
			}
			report.RecallExcluded = len(material.RecallSources) - report.RecallIncluded
			parts = append(parts, fmt.Sprintf("检索预算说明：匹配记录%d条，纳入%d条，因预算排除%d条。未纳入不代表没有历史或事情未发生；只依据已提供材料作判断。", len(material.RecallSources), report.RecallIncluded, report.RecallExcluded))
		}
		req.Input = strings.Join(parts, "\n")
		if _, err := model.ValidateTextRequest(req); err == nil {
			break
		} else if !errors.Is(err, model.ErrTextInputTooLarge) {
			report.Failure = "invalid_request"
			return model.TextRequest{}, report, err
		}
		if len(sections) == 0 {
			report.Failure = "required_content"
			report.RequiredComplete = false
			return model.TextRequest{}, report, ErrContextCapacity
		}
		report.Excluded++
		report.ExcludedSources += len(sections[0].Sources)
		sections = sections[1:]
	}
	for _, id := range material.RequiredSources {
		if !containsID(report.SelectedSources, id) {
			report.SelectedSources = append(report.SelectedSources, id)
		}
	}
	for _, section := range sections {
		report.Sections = append(report.Sections, section.Name)
		for _, id := range section.Sources {
			if !containsID(report.SelectedSources, id) {
				report.SelectedSources = append(report.SelectedSources, id)
			}
		}
	}
	report.Sources = len(report.SelectedSources)
	report.InputTokens = framedContextTokens(req)
	return req, report, nil
}

func framedContextTokens(req model.TextRequest) int {
	n, _ := tokenestimate.EstimateStableJSON(map[string]any{"messages": []map[string]string{{"role": "system", "content": req.System}, {"role": "user", "content": req.Input}}})
	return n + 16
}

type contextGenerator struct {
	model.TextGenerator
	composer ContextComposer
	material contextMaterial
	logger   Logger
	calls    int
}

func (a *App) contextGenerator(generator model.TextGenerator, material contextMaterial, snapshot worldSnapshot, run Run, purpose, recipient string, stage int, template string) model.TextGenerator {
	window := model.WindowLimits{}
	if provider, ok := generator.(model.WindowProvider); ok {
		window = provider.ModelWindow()
	}
	reasoning := 0
	if provider, ok := generator.(model.TextReasoningProvider); ok {
		reasoning = provider.TextReasoningReserve()
	}
	return &contextGenerator{TextGenerator: generator, material: material, logger: a.logger, composer: ContextComposer{Scope: ContextScope{Owner: a.userID, Game: snapshot.Summary.GameID, World: snapshot.Summary.WorldID, Run: run.RunID, Attempt: run.Attempt, Stage: stage, Epoch: run.BaseContextEpoch, SceneVersion: snapshot.SceneVersion, Purpose: purpose, Recipient: recipient, Template: template, PolicyRevision: material.PolicyRevision}, Window: window, ReasoningReserve: reasoning}}
}

func (g *contextGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	req, report, err := g.composer.Build(g.material, request.System, request.MaxOutputTokens)
	if g.logger != nil {
		s := report.Scope
		g.logger.Printf("story context recall: world_id=%q run_id=%q purpose=%q recipient=%q included=%d excluded=%d", s.World, s.Run, s.Purpose, s.Recipient, report.RecallIncluded, report.RecallExcluded)
		g.logger.Printf("story context output budget: world_id=%q run_id=%q purpose=%q recipient=%q visible_tokens=%d reasoning_requested=%d reasoning_reserved=%d total_output_tokens=%d", s.World, s.Run, s.Purpose, s.Recipient, report.OutputTokens, report.ReasoningRequested, report.ReasoningReserved, report.TotalOutputTokens)
		g.logger.Printf("story context built: owner_id=%q game_id=%q world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d epoch=%d scene_version=%d template=%q policy_revision=%q sections=%q sources=%d excluded=%d duplicates=%d input_tokens=%d output_tokens=%d required_complete=%t window_known=%t success=%t failure=%q excluded_sources=%d selected_source_ids=%q", s.Owner, s.Game, s.World, s.Run, s.Attempt, s.Purpose, s.Recipient, s.Stage, s.Epoch, s.SceneVersion, s.Template, s.PolicyRevision, strings.Join(report.Sections, ","), report.Sources, report.Excluded, report.Duplicates, report.InputTokens, report.OutputTokens, report.RequiredComplete, report.WindowKnown, err == nil, report.Failure, report.ExcludedSources, strings.Join(report.SelectedSources, ","))
	}
	if err != nil {
		return model.TextResponse{}, err
	}
	g.calls++
	scope := g.composer.Scope
	started := time.Now()
	if g.logger != nil {
		g.logger.Printf("story model call started: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d call=%d", scope.World, scope.Run, scope.Attempt, scope.Purpose, scope.Recipient, scope.Stage, g.calls)
	}
	response, callErr := g.TextGenerator.GenerateText(ctx, req)
	diagnostic := response.Diagnostic
	var failure *model.TextCallError
	if errors.As(callErr, &failure) {
		diagnostic = failure.Diagnostic
	}
	if g.logger != nil {
		g.logger.Printf("story model call finished: world_id=%q run_id=%q attempt=%d purpose=%q recipient=%q stage=%d call=%d elapsed_ms=%d success=%t error_code=%q http_status=%d provider_request_id=%q finish_reason=%q provider_input_tokens=%d provider_output_tokens=%d reasoning_tokens=%d content_chars=%d reasoning_chars=%d", scope.World, scope.Run, scope.Attempt, scope.Purpose, scope.Recipient, scope.Stage, g.calls, time.Since(started).Milliseconds(), callErr == nil, model.TextErrorCode(callErr), diagnostic.HTTPStatus, model.SafeRequestID(diagnostic.RequestID), model.SafeFinishReason(diagnostic.FinishReason), diagnostic.InputTokens, diagnostic.OutputTokens, diagnostic.ReasoningTokens, diagnostic.ContentChars, diagnostic.ReasoningChars)
	}
	return response, callErr
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
	sequence := map[string]int64{}
	keyFor := func(id string) string {
		key := id
		source := snapshot.Sources[id]
		if source.Kind == "npc_action_result" {
			if at := strings.LastIndex(id, ":result:"); at >= 0 {
				key = id[:at]
			}
		}
		if source.Seq > sequence[key] {
			sequence[key] = source.Seq
		}
		return key
	}
	for _, p := range snapshot.Perceptions[recipient] {
		key := keyFor(p.SourceEventID)
		group := groups[key]
		group.Name = "personal_history"
		if !containsID(group.Sources, p.SourceEventID) {
			group.Sources = append(group.Sources, p.SourceEventID)
		}
		group.Text += "此前已提交个人感知：" + joinPerceptions(snapshot, []Perception{p}) + "\n"
		groups[key] = group
	}
	for _, m := range snapshot.Memories[recipient] {
		key := keyFor(m.SourceEventID)
		group := groups[key]
		group.Name = "personal_history"
		if !containsID(group.Sources, m.SourceEventID) {
			group.Sources = append(group.Sources, m.SourceEventID)
		}
		group.Text += "本人主观记忆（不是世界事实）：" + m.Kind + "：" + m.Content + "\n"
		groups[key] = group
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := sequence[ids[i]], sequence[ids[j]]
		if a == b {
			return ids[i] < ids[j]
		}
		return a < b
	})
	result := make([]contextSection, 0, len(ids))
	for _, id := range ids {
		result = append(result, groups[id])
	}
	return result
}

func sceneViewSources(snapshot worldSnapshot, recipient string) []string {
	var ids []string
	for _, view := range snapshot.SceneViews {
		if recipient == "" || view.Recipient == recipient {
			for _, id := range view.SourceIDs {
				if !containsID(ids, id) {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

func coordinationSections(events []Event) []contextSection {
	var groups [][]Event
	for _, event := range events {
		if event.EventType != "npc_action_result" && event.EventType != "player_action_result" {
			continue
		}
		if len(groups) == 0 || groups[len(groups)-1][0].RunID != event.RunID {
			groups = append(groups, []Event{})
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], event)
	}
	var result []contextSection
	for _, group := range groups {
		result = append(result, contextSection{Name: "committed_results", Text: coordinationContinuity(group), Sources: eventIDs(group)})
	}
	return result
}
