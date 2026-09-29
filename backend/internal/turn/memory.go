package turn

// This file owns how much memory one model call may see and how it is fitted into the
// material. Choosing a window against the request's real input budget, shrinking it
// whole-group by whole-group, and appending retrieval results are all decisions about
// this call, not about what a character remembers — that part lives in memorymodel.

import (
	"fmt"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

// MemoryCorrectionRule is the standing rule that a recorded correction outranks the
// older recollection it corrects. The long-memory material and the digest prompt share
// it so both say the same thing.
const MemoryCorrectionRule = "\n记录类型 correction:* 是对本人资料已经生效的纠正，优先于此前关于同一内容的解释、回忆或自己的旧对白。保留曾经说过旧话这一历史，但后续判断使用纠正后的内容；纠正本身不是故事里新发生的对话，也不授予其他人物这些知识。"

// budgetHeadroomTokens covers the labels, digest and framing the projection adds on top
// of the raw records it measures.
const budgetHeadroomTokens = 256

// recentWindowMinTokens is the smallest recent window a request may be reduced to: one
// complete group.
const recentWindowMinTokens = 512

// WithLongMemory fits a scope's committed memory into the material: the standing digest,
// the newest complete groups, and whatever the request's own input budget still allows
// after the base prompt. It never advances the digest watermark.
func WithLongMemory(material Material, snapshot Snapshot, scope, query string) Material {
	m, ok := snapshot.LongMemory[scope]
	if !ok {
		return material
	}
	material.Optional = nil
	material.System += MemoryCorrectionRule
	// The window is chosen against the request's real input budget, not a fixed size:
	// a large base prompt must shrink the window instead of failing the turn.
	baseTokens := FramedContextTokens(model.TextRequest{System: material.System, Input: material.Required})
	budget := snapshot.InputBudgetTokens
	if budget <= 0 {
		budget = 12000
	}
	available := budget - baseTokens - budgetHeadroomTokens
	block, _, _ := selectRecentWindow(m.Tail, available)
	base := material.Required
	material = renderMemoryWindow(material, base, m, scope, block, query)
	// The window is chosen against the known budget first; if the assembled request still
	// does not fit, this material rebuilds itself from its parts with fewer complete
	// groups rather than failing. The newest group always stays and the digest watermark
	// is untouched either way.
	full := material
	material.Bounded = func(inputLimit int) (Material, bool) {
		groups := memorymodel.MemoryGroups(block)
		if len(groups) <= 1 {
			return full, false
		}
		// Reserve room for the system prompt and everything that is not the window.
		baseTokens := FramedContextTokens(model.TextRequest{System: full.System, Input: base})
		budget := inputLimit - baseTokens - budgetHeadroomTokens
		if budget < recentWindowMinTokens {
			budget = recentWindowMinTokens
		}
		kept := groupsWithinBudget(groups, budget)
		if len(kept) >= len(groups) {
			return full, false
		}
		return renderMemoryWindow(full, base, m, scope, memorymodel.FlattenGroups(kept), query), true
	}
	return material
}

// renderMemoryWindow builds the required block from the untouched base text and an
// explicit set of groups. Rendering from the parts keeps the digest header, the source
// list and the declined backlog consistent with what was actually included.
func renderMemoryWindow(material Material, base string, m MemoryContext, scope string, block []memorymodel.MemorySource, query string) Material {
	material.Required = base
	material.RequiredSources = nil
	material.DeclinedSources = nil
	material.Optional = nil
	groups := memorymodel.MemoryGroups(block)
	included := map[string]bool{}
	for _, record := range block {
		included[record.ID] = true
	}
	label := fmt.Sprintf("最近的已发生经历（共%d组）", len(groups))
	declinedCount := len(m.Tail) - len(block)
	if declinedCount > 0 {
		label = fmt.Sprintf("最近的已发生经历（本次提供最近%d组；另有%d条更早经历尚未整理、本次未提供，按需检索，未提供不代表没有发生）", len(groups), declinedCount)
	}
	material.Required = "已提交的连续个人回顾（非世界客观事实）：" + memorymodel.DigestContext(m.Digest) + "\n" + label + "（均已发生，不重演）：\n" + memorymodel.MemoryRecordsText(block) + "\n本轮职责与刺激：\n" + material.Required
	material.RequiredSources = append(material.RequiredSources, memorymodel.RetainedStateSources(m.Digest)...)
	if m.Digest.Revision > 0 {
		material.RequiredSources = append(material.RequiredSources, fmt.Sprintf("digest:%s:%d", scope, m.Digest.Revision))
	}
	for _, record := range block {
		material.RequiredSources = append(material.RequiredSources, record.ID)
	}
	// Groups that did not fit stay retrievable: they remain in the archive and are
	// reported as declined rather than as already retrieved.
	backlog := [][]memorymodel.MemorySource{}
	for _, group := range memorymodel.MemoryGroups(m.Tail) {
		if len(group) > 0 && !included[group[0].ID] {
			backlog = append(backlog, group)
		}
	}
	for i := len(backlog) - 1; i >= 0; i-- {
		section := Section{Name: "memory_recent_backlog", Text: "较早的未整理经历（本次未全部提供，可用检索取回）：\n" + memorymodel.MemoryRecordsText(backlog[i])}
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
	return WithRecall(material, MemoryProjection{Context: m, Supplied: supplied}, query)
}

// MemoryProjection is what this request already supplies, so retrieval does not offer the
// same committed group twice. It is exported only while the material callers are still in
// storyapp; A2 moves them here and takes it back to package-private.
type MemoryProjection struct {
	Context MemoryContext
	// Supplied marks the records this request already provides; anything else the
	// receiver may lawfully recall stays eligible for retrieval.
	Supplied map[string]bool
}

func (p MemoryProjection) alreadySupplied(id string) bool {
	return p.Supplied[id]
}

// WithRecall appends authorized retrieval hits to the material as optional sections, one
// whole committed group per hit, lowest-ranked first when the composer has to drop
// something.
func WithRecall(material Material, projection MemoryProjection, query string) Material {
	m := projection.Context
	hits := memorymodel.SearchMemory(m.Archive, query, 5)
	groups := memorymodel.MemoryGroups(m.Archive)
	var selected []Section
	// Lowest-ranked matches are removed first by the shared budgeter. A hit
	// selects its entire committed group so attempts keep their outcomes.
	for _, s := range hits {
		if projection.alreadySupplied(s.ID) || wiaworld.ContainsID(material.RecallSources, s.ID) {
			continue
		}
		for _, group := range groups {
			contains := false
			overlap := false
			for _, record := range group {
				contains = contains || record.ID == s.ID
				overlap = overlap || projection.alreadySupplied(record.ID) || wiaworld.ContainsID(material.RecallSources, record.ID)
			}
			if !contains || overlap {
				continue
			}
			section := Section{Name: "memory_recall", Text: "检索到的本人旧经历（同一已提交回合）：\n" + memorymodel.MemoryRecordsText(group)}
			for _, record := range group {
				section.Sources = append(section.Sources, record.ID)
				material.RecallSources = append(material.RecallSources, record.ID)
			}
			selected = append(selected, section)
			break
		}
	}
	for i := len(selected) - 1; i >= 0; i-- {
		material.Optional = append(material.Optional, selected[i])
	}
	return material
}

// groupsWithinBudget keeps the newest complete groups that fit, and always keeps the
// newest one so a turn never loses its own most recent context.
func groupsWithinBudget(groups [][]memorymodel.MemorySource, budget int) [][]memorymodel.MemorySource {
	if len(groups) == 0 {
		return nil
	}
	for start := 0; start < len(groups); start++ {
		candidate := groups[start:]
		if FramedContextTokens(model.TextRequest{Input: memorymodel.MemoryRecordsText(memorymodel.FlattenGroups(candidate))}) <= budget || start == len(groups)-1 {
			return candidate
		}
	}
	return groups[len(groups)-1:]
}

// selectRecentWindow keeps the newest complete experience groups that fit the request's
// remaining input budget. It falls back to the size-based rule when no budget is known,
// always keeps the newest group, and never advances the digest watermark: supplying
// fewer groups must not claim they were summarized.
func selectRecentWindow(items []memorymodel.MemorySource, available int) (block []memorymodel.MemorySource, backlog [][]memorymodel.MemorySource, supplied map[string]bool) {
	supplied = map[string]bool{}
	groups := memorymodel.MemoryGroups(items)
	if len(groups) == 0 {
		return nil, nil, supplied
	}
	if available <= 0 {
		block, backlog, supplied = memorymodel.ProjectRecentExperience(items)
		return block, backlog, supplied
	}
	start := len(groups) - memorymodel.TargetRecentGroups
	if start < 0 {
		start = 0
	}
	// Drop older groups while the window does not fit the budget, and drop it entirely
	// when it is larger than the size rule allows.
	for start < len(groups)-1 {
		candidate := groups[start:]
		if FramedContextTokens(model.TextRequest{Input: memorymodel.MemoryRecordsText(memorymodel.FlattenGroups(candidate))}) <= available &&
			len(memorymodel.MemoryRecordsText(memorymodel.FlattenGroups(candidate))) <= memorymodel.RecentWindowChars {
			break
		}
		start++
	}
	block = memorymodel.FlattenGroups(groups[start:])
	for _, record := range block {
		supplied[record.ID] = true
	}
	backlog = groups[:start]
	return block, backlog, supplied
}
