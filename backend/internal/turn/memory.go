package turn

// This file owns how much memory one model call may see and how it is fitted into the
// material. Choosing a window against the request's real input budget, shrinking it
// whole-group by whole-group, and appending retrieval results are all decisions about
// this call, not about what a character remembers — that part lives in memory.

import (
	"fmt"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
)

// MemoryCorrectionRule is the standing rule that a recorded correction outranks the
// older recollection it corrects. The long-memory material and the digest prompt share
// it so both say the same thing.
const MemoryCorrectionRule = "\n记录类型 correction:* 是对本人资料已经生效的纠正，优先于此前关于同一内容的解释、回忆或自己的旧对白。保留曾经说过旧话这一历史，但后续判断使用纠正后的内容；纠正本身不是故事里新发生的对话，也不授予其他人物这些知识。"

// withLongMemory fits a scope's committed memory into the material: the standing digest,
// the newest complete groups, and whatever the request's own input budget still allows
// after the base prompt. It never advances the digest watermark.
func withLongMemory(material Material, snapshot Snapshot, scope, query string) Material {
	m, ok := snapshot.LongMemory[scope]
	if !ok {
		return material
	}
	material.Optional = nil
	material.System += MemoryCorrectionRule
	base := material
	base.RequiredSources = append([]string(nil), material.RequiredSources...)
	budget := snapshot.InputBudgetTokens
	if budget <= 0 {
		budget = 12000
	}
	block, _, _ := memory.ProjectRecentExperience(m.Tail)
	groups := memory.MemoryGroups(block)
	// Measure the full digest, framing and retrieval notice. The actual system prompt
	// can grow when the generation contract or repair instruction is appended.
	fit := func(inputLimit int, system string, start int) (Material, int) {
		for {
			candidate := renderMemoryWindow(base, m, scope, memory.FlattenGroups(groups[start:]), query)
			input, _, _ := contextInput(candidate, nil)
			if model.FramedTextInputTokens(model.TextRequest{System: system, Input: input}) <= inputLimit || len(groups)-start <= 1 {
				return candidate, start
			}
			start++
		}
	}
	material, start := fit(budget, base.System, 0)
	material.Bounded = func(inputLimit int, system string) (Material, bool) {
		bounded, keptStart := fit(inputLimit, system, start)
		return bounded, keptStart > start
	}
	return material
}

// renderMemoryWindow builds the required block from the untouched base text and an
// explicit set of groups. Rendering from the parts keeps the digest header, the source
// list and the declined backlog consistent with what was actually included.
func renderMemoryWindow(material Material, m MemoryContext, scope string, block []memory.MemorySource, query string) Material {
	material.RequiredSources = append([]string(nil), material.RequiredSources...)
	material.DeclinedSources = nil
	material.RecallSources = nil
	material.RecallLimited = false
	material.Optional = nil
	groups := memory.MemoryGroups(block)
	included := map[string]bool{}
	for _, record := range block {
		included[record.ID] = true
	}
	label := fmt.Sprintf("最近的已发生经历（共%d组）", len(groups))
	declinedCount := len(m.Tail) - len(block)
	if declinedCount > 0 {
		label = fmt.Sprintf("最近的已发生经历（本次提供最近%d组；另有%d条更早经历尚未整理、本次未提供，按需检索，未提供不代表没有发生）", len(groups), declinedCount)
	}
	material.Required = "已提交的连续个人回顾（非世界客观事实）：" + memory.DigestContext(m.Digest) + "\n" + label + "（均已发生，不重演）：\n" + memory.MemoryRecordsText(block) + "\n本轮职责与刺激：\n" + material.Required
	material.RequiredSources = append(material.RequiredSources, memory.RetainedStateSources(m.Digest)...)
	if m.Digest.Revision > 0 {
		material.RequiredSources = append(material.RequiredSources, fmt.Sprintf("digest:%s:%d", scope, m.Digest.Revision))
	}
	for _, record := range block {
		material.RequiredSources = append(material.RequiredSources, record.ID)
	}
	// Groups that did not fit stay retrievable: they remain in the archive and are
	// reported as declined rather than as already retrieved.
	backlog := [][]memory.MemorySource{}
	for _, group := range memory.MemoryGroups(m.Tail) {
		if len(group) > 0 && !included[group[0].ID] {
			backlog = append(backlog, group)
		}
	}
	for i := len(backlog) - 1; i >= 0; i-- {
		section := Section{Name: "memory_recent_backlog", Text: "较早的未整理经历（本次未全部提供，可用检索取回）：\n" + memory.MemoryRecordsText(backlog[i])}
		for _, record := range backlog[i] {
			section.Sources = append(section.Sources, record.ID)
			material.DeclinedSources = append(material.DeclinedSources, record.ID)
		}
		material.Optional = append(material.Optional, section)
	}
	supplied := map[string]bool{}
	for _, record := range block {
		supplied[record.ID] = true
	}
	return withRecall(material, memoryProjection{Context: m, Supplied: supplied}, query)
}

// memoryProjection is what this request already supplies, so retrieval does not offer the
// same committed group twice.
type memoryProjection struct {
	Context MemoryContext
	// Supplied marks the records this request already provides; anything else the
	// receiver may lawfully recall stays eligible for retrieval.
	Supplied map[string]bool
}

// withRecall appends authorized retrieval hits to the material as optional sections, one
// whole committed group per hit, lowest-ranked first when the composer has to drop
// something.
func withRecall(material Material, projection memoryProjection, query string) Material {
	m := projection.Context
	excluded := make(map[string]bool, len(projection.Supplied)+len(material.RecallSources))
	for id := range projection.Supplied {
		excluded[id] = true
	}
	for _, id := range material.RecallSources {
		excluded[id] = true
	}
	search := memory.SearchMemoryGroups(m.Archive, query, excluded, 5)
	material.RecallLimited = material.RecallLimited || search.Limited
	var selected []Section
	// Lowest-ranked matches are removed first by the shared budgeter. Search returns
	// complete committed groups so attempts keep their outcomes.
	for _, group := range search.Groups {
		section := Section{Name: "memory_recall", Text: "检索到的本人旧经历（同一已提交回合）：\n" + memory.MemoryRecordsText(group)}
		for _, record := range group {
			section.Sources = append(section.Sources, record.ID)
			material.RecallSources = append(material.RecallSources, record.ID)
		}
		selected = append(selected, section)
	}
	for i := len(selected) - 1; i >= 0; i-- {
		material.Optional = append(material.Optional, selected[i])
	}
	return material
}
