package turn

// The context composer: it decides what one model call may see, and how much of it fits.
//
// It has no database, no model and no state that survives a request. Material comes in
// already gathered, this decides what to include within a budget and reports what it did,
// and the caller sends the result. Keeping the two apart is what makes it possible to
// answer "what was this call allowed to see" from a report rather than from the prompt.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func JoinPerceptions(snapshot Snapshot, items []wiaworld.Perception) string {
	var parts []string
	for _, item := range items {
		label := item.SourceType
		if source, ok := snapshot.Sources[item.SourceEventID]; ok && source.Actor != "" {
			label = fmt.Sprintf("%s(%s；来源=%s；序号=%d)", CharacterDisplayName(snapshot.Characters, source.Actor), item.SourceType, source.ID, source.Seq)
		} else if event, ok := EventByID(snapshot.Events, item.SourceEventID); ok && event.ActorID != "" {
			label = fmt.Sprintf("%s(%s)", CharacterDisplayName(snapshot.Characters, event.ActorID), item.SourceType)
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", label, item.Content))
	}
	if len(parts) == 0 {
		return "（暂无）"
	}
	return strings.Join(parts, "\n")
}

type Section struct {
	Name    string
	Text    string
	Sources []string
}

type Material struct {
	RecallSources []string
	// RecallLimited means retrieval ended at its candidate, byte, query or time
	// boundary. Callers must not treat an empty result as proof that no older match
	// exists.
	RecallLimited bool
	// DeclinedSources are authorized records this request deliberately left out of
	// the recent window. They stay retrievable, so they are reported but not treated
	// as already supplied or already retrieved.
	DeclinedSources []string
	PolicyRevision  string
	RequiredSources []string
	System          string
	Required        string
	Optional        []Section // oldest first; a section is an indivisible causal group
	// Bounded rebuilds this material to fit an input budget. It reports changed=false
	// when it is already as small as it can be, so a caller can tell "does not fit"
	// from "was reduced".
	Bounded func(inputLimit int, system string) (Material, bool)
}

type ContextScope struct {
	Owner, Game, World, Run, Purpose, Recipient, Template string
	PolicyRevision                                        string
	Attempt, Stage                                        int
	Epoch, SceneVersion                                   int64
}

type ContextBuildReport struct {
	RecallIncluded, RecallExcluded                           int
	RecallLimited                                            bool
	SelectedSources                                          []string
	ExcludedSources                                          int
	Failure                                                  string
	Scope                                                    ContextScope
	Sections                                                 []string
	Sources, Excluded, Duplicates, InputTokens, OutputTokens int
	RequiredComplete                                         bool
	WindowKnown                                              bool
	// WindowShrunk records that the recent-experience window was reduced to fit the
	// request budget rather than the request failing outright.
	WindowShrunk                                             bool
	ReasoningRequested, ReasoningReserved, TotalOutputTokens int
}

// ContextComposer has no database, model, or mutable cross-request state.
type ContextComposer struct {
	Scope            ContextScope
	Window           model.WindowLimits
	ReasoningReserve int
}

var ErrContextCapacity = errors.New("story context capacity exceeded")

func (c ContextComposer) Build(material Material, system string, output int) (model.TextRequest, ContextBuildReport, error) {
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
	// A material that knows the whole request's input budget can shrink its own recent
	// window before the composer has to give up.
	if material.Bounded != nil {
		if bounded, changed := material.Bounded(limit, system); changed {
			material = bounded
			report.WindowShrunk = true
		}
	}
	report.RecallLimited = material.RecallLimited
	req := model.TextRequest{System: system, Input: material.Required, MaxInputTokens: limit, MaxOutputTokens: output, ReasoningReserveTokens: reasoning, MaxResponseBytes: 1 << 20}
	report.InputTokens = model.FramedTextInputTokens(req)
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
	var sections []Section
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
		req.Input, report.RecallIncluded, report.RecallExcluded = contextInput(material, sections)
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
		if !wiaworld.ContainsID(report.SelectedSources, id) {
			report.SelectedSources = append(report.SelectedSources, id)
		}
	}
	for _, section := range sections {
		report.Sections = append(report.Sections, section.Name)
		for _, id := range section.Sources {
			if !wiaworld.ContainsID(report.SelectedSources, id) {
				report.SelectedSources = append(report.SelectedSources, id)
			}
		}
	}
	report.Sources = len(report.SelectedSources)
	report.InputTokens = model.FramedTextInputTokens(req)
	return req, report, nil
}

// contextInput includes the retrieval notice in both window sizing and final assembly.
func contextInput(material Material, sections []Section) (string, int, int) {
	parts := make([]string, 0, len(sections)+2)
	selected := map[string]bool{}
	for _, section := range sections {
		parts = append(parts, section.Text)
		for _, id := range section.Sources {
			selected[id] = true
		}
	}
	parts = append(parts, material.Required)
	included, excluded := 0, 0
	if len(material.RecallSources) > 0 || len(material.DeclinedSources) > 0 || material.RecallLimited {
		for _, id := range material.RecallSources {
			if selected[id] {
				included++
			}
		}
		excluded = len(material.RecallSources) - included + len(material.DeclinedSources)
		note := fmt.Sprintf("检索预算说明：匹配记录%d条，纳入%d条，因预算排除%d条。未纳入不代表没有历史或事情未发生；只依据已提供材料作判断。", len(material.RecallSources)+len(material.DeclinedSources), included, excluded)
		if material.RecallLimited {
			note += " 本次检索受近期候选窗口或检索预算限制；未命中不代表更早经历不存在。"
		}
		parts = append(parts, note)
	}
	return strings.Join(parts, "\n"), included, excluded
}

func NarrativeSections(messages []wiaworld.Message) []Section {
	var sections []Section
	for _, message := range messages {
		if message.Kind == "narrative" && wire.Clean(message.Content) != "" {
			sections = append(sections, Section{Name: "player_history", Text: "玩家可见历史正文（表现参考，不是本轮事实）：" + message.Content, Sources: []string{message.MessageID}})
		}
	}
	if len(sections) > 8 {
		sections = sections[len(sections)-8:]
	}
	return sections
}

func PersonalSections(snapshot Snapshot, recipient string) []Section {
	groups := map[string]Section{}
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
		if !wiaworld.ContainsID(group.Sources, p.SourceEventID) {
			group.Sources = append(group.Sources, p.SourceEventID)
		}
		group.Text += "此前已提交个人感知：" + JoinPerceptions(snapshot, []wiaworld.Perception{p}) + "\n"
		groups[key] = group
	}
	for _, m := range snapshot.Memories[recipient] {
		key := keyFor(m.SourceEventID)
		group := groups[key]
		group.Name = "personal_history"
		if !wiaworld.ContainsID(group.Sources, m.SourceEventID) {
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
	result := make([]Section, 0, len(ids))
	for _, id := range ids {
		result = append(result, groups[id])
	}
	return result
}

func CoordinationSections(events []wiaworld.Event) []Section {
	var groups [][]wiaworld.Event
	for _, event := range events {
		if event.EventType != "npc_action_result" && event.EventType != "player_action_result" {
			continue
		}
		if len(groups) == 0 || groups[len(groups)-1][0].RunID != event.RunID {
			groups = append(groups, []wiaworld.Event{})
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], event)
	}
	var result []Section
	for _, group := range groups {
		result = append(result, Section{Name: "committed_results", Text: CoordinationContinuity(group), Sources: EventIDs(group)})
	}
	return result
}
