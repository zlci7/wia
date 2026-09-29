package storyapp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func loadLongMemory(ctx context.Context, store *storage.WorldStore, snapshot *turn.Snapshot) error {
	if err := indexMemorySources(ctx, store); err != nil {
		return err
	}
	snapshot.LongMemory = map[string]turn.MemoryContext{}
	for _, scope := range memoryScopeIDs(*snapshot) {
		archive, err := readMemorySources(ctx, store, scope, 0)
		if err != nil {
			return err
		}
		d, err := readDigest(ctx, store, scope)
		if err != nil {
			return err
		}
		m := turn.MemoryContext{Digest: d, Archive: archive}
		for _, s := range archive {
			if s.Seq > d.Through {
				m.Tail = append(m.Tail, s)
			}
		}
		snapshot.LongMemory[scope] = m
	}
	return nil
}

// Maintenance reads only committed prefixes while the owning run protects the
// world from deletion/copy. Publication is version checked outside model calls.
func (a *App) prepareLongMemory(ctx context.Context, store *storage.WorldStore, snapshot *turn.Snapshot, run wiaworld.Run, generator model.TextGenerator) error {
	if err := loadLongMemory(ctx, store, snapshot); err != nil {
		return err
	}
	// The projection must know the request's real input budget, so a small model window
	// shrinks the recent-experience window instead of failing the turn.
	snapshot.InputBudgetTokens = inputBudgetTokens(generator)
	for _, scope := range memoryScopeIDs(*snapshot) {
		for maintenance := 0; maintenance < 3; maintenance++ {
			m := snapshot.LongMemory[scope]
			groups := memoryGroups(m.Tail)
			if len(groups) <= 4 || (len(groups) <= 8 && framedContextTokens(model.TextRequest{System: "memory", Input: memoryRecordsText(m.Tail)}) <= 6000) {
				break
			}
			prefix := []memorymodel.MemorySource{}
			for _, group := range groups[:len(groups)-4] {
				candidate := append(append([]memorymodel.MemorySource{}, prefix...), group...)
				if framedContextTokens(model.TextRequest{System: "memory", Input: memoryRecordsText(candidate)}) > 6500 && len(prefix) > 0 {
					break
				}
				prefix = candidate
			}
			d, err := a.summarizeMemory(ctx, generator, *snapshot, run, scope, m.Digest, prefix)
			if err != nil {
				if a.logger != nil {
					a.logger.Printf("story memory maintenance: world_id=%q scope=%q success=false error_code=%q", snapshot.Summary.WorldID, scope, safeTurnErrorCode(err))
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				break
			}
			d.Head = m.Archive[len(m.Archive)-1].Seq
			if err = publishDigest(ctx, store, d, m.Digest.Revision, run.BaseContextEpoch); err != nil {
				return err
			}
			m.Digest = d
			m.Tail = nil
			for _, s := range m.Archive {
				if s.Seq > d.Through {
					m.Tail = append(m.Tail, s)
				}
			}
			snapshot.LongMemory[scope] = m
		}
	}
	return nil
}

func (a *App) summarizeMemory(ctx context.Context, g model.TextGenerator, snapshot turn.Snapshot, run wiaworld.Run, scope string, previous memorymodel.MemoryDigest, prefix []memorymodel.MemorySource) (memorymodel.MemoryDigest, error) {
	d := previous
	if len(prefix) == 0 {
		return d, nil
	}
	sources := append([]string{}, previous.Sources...)
	allowed := retainedStateSources(previous)
	for _, s := range prefix {
		if !wiaworld.ContainsID(allowed, s.ID) {
			allowed = append(allowed, s.ID)
		}
		if !wiaworld.ContainsID(sources, s.ID) {
			sources = append(sources, s.ID)
		}
	}
	material := contextMaterial{System: "你整理单一接收者已经提交的经历，不执行故事，不读取其他人物资料。按时间组织回顾，保留关键约定、结果及来源。尝试不等于成功，主观判断不等于事实，玩家文学正文只作玩家经历参考。只返回JSON：content字符串、states数组。states每项仅含kind、content、source_ids；kind为belief/relationship/concern/commitment，source_ids只引用获准来源。保留有效旧状态，已完成关切标明完成而非继续当待办。回顾简洁，通常不超过1000字。", Required: "接收者：" + scope + "\n已有连续回顾：" + digestContext(previous) + "\n新增连续经历：\n" + memoryRecordsText(prefix), RequiredSources: allowed}
	material.Required += "\nsource_ids 的完整合法记录ID列表：" + wire.MarshalJSON(allowed) + "\n本次列表仅含保留状态的必要来源与新增记录，完整历史覆盖仍由存档维护。每条状态的 source_ids 只从此列表原样选择。经历中的来源事件字段是溯源元数据，不是此处可填写的个人记录ID。没有可保留状态时 states 返回[]。"
	material.System += memoryCorrectionRule
	call := a.contextGenerator(g, material, snapshot, run, "memory_digest", scope, 0, "story.memory.v3")
	var result struct {
		Content string                        `json:"content"`
		States  []memorymodel.SubjectiveState `json:"states"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := generateJSON(callCtx, call, material.System, material.Required, &result, structuredTurnOutputTokens, "content", "states"); err != nil {
		return d, err
	}
	if wire.Clean(result.Content) == "" || result.States == nil {
		a.logMemoryValidation(snapshot.Summary.WorldID, scope, "required_fields")
		return d, ErrGenerationFailed
	}
	for _, state := range result.States {
		if !wiaworld.ContainsID([]string{"belief", "relationship", "concern", "commitment"}, state.Kind) || wire.Clean(state.Content) == "" || len(state.Sources) == 0 {
			a.logMemoryValidation(snapshot.Summary.WorldID, scope, "state_fields")
			return d, ErrGenerationFailed
		}
		for _, id := range state.Sources {
			if !wiaworld.ContainsID(allowed, id) {
				a.logMemoryValidation(snapshot.Summary.WorldID, scope, "state_source")
				return d, ErrGenerationFailed
			}
		}
	}
	return memorymodel.MemoryDigest{Scope: scope, Revision: previous.Revision + 1, Epoch: run.BaseContextEpoch, Through: prefix[len(prefix)-1].Seq, Head: prefix[len(prefix)-1].Seq, Content: result.Content, States: result.States, Sources: sources}, nil
}

func (a *App) logMemoryValidation(world, scope, boundary string) {
	if a.logger != nil {
		a.logger.Printf("story memory validation failed: world_id=%q scope=%q boundary=%s", world, scope, boundary)
	}
}

// publishDigest records one scope's standing summary, but only if the world still
// matches what the caller read.
//
// The precondition check and the insert are one transaction inside storage, because
// separating them would let another turn commit in between and leave the digest
// describing a source stream that no longer exists in that form. Whether a mismatch
// means a conflict is decided here, not there.
func publishDigest(ctx context.Context, store *storage.WorldStore, d memorymodel.MemoryDigest, previous, epoch int64) error {
	conflict, err := store.CompareAndInsertMemoryDigest(ctx, storage.MemoryDigestWrite{
		Scope:            d.Scope,
		Revision:         d.Revision,
		Epoch:            d.Epoch,
		Through:          d.Through,
		ExpectedHead:     d.Head,
		ExpectedEpoch:    epoch,
		ExpectedRevision: previous,
		Content:          d.Content,
		States:           wire.MarshalJSON(d.States),
		Sources:          wire.MarshalJSON(d.Sources),
		CreatedAt:        wire.NowText(),
	})
	if err != nil {
		return err
	}
	if conflict.Epoch || conflict.Revision != previous || conflict.Head != d.Head {
		return ErrVersionConflict
	}
	return nil
}

// inputBudgetTokens is how many input tokens a request may use with this generator. It
// mirrors the composer's own rule, including the conservative default for unknown
// windows, so the memory projection and the composer agree on the budget.
func inputBudgetTokens(generator model.TextGenerator) int {
	limit := 12000
	if provider, ok := generator.(model.WindowProvider); ok {
		window := provider.ModelWindow()
		if window != (model.WindowLimits{}) && window.Validate() == nil {
			reserved := outputReserveTokens
			if reasoning, ok := generator.(model.TextReasoningProvider); ok {
				reserved += reasoning.TextReasoningReserve()
			}
			if budget := window.ContextTokens - reserved; budget > 0 {
				limit = min(limit, budget)
			}
		}
	}
	return limit
}

func withLongMemory(material contextMaterial, snapshot turn.Snapshot, scope, query string) contextMaterial {
	m, ok := snapshot.LongMemory[scope]
	if !ok {
		return material
	}
	material.Optional = nil
	material.System += memoryCorrectionRule
	// The window is chosen against the request's real input budget, not a fixed size:
	// a large base prompt must shrink the window instead of failing the turn.
	baseTokens := framedContextTokens(model.TextRequest{System: material.System, Input: material.Required})
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
	material.Bounded = func(inputLimit int) (contextMaterial, bool) {
		groups := memoryGroups(block)
		if len(groups) <= 1 {
			return full, false
		}
		// Reserve room for the system prompt and everything that is not the window.
		baseTokens := framedContextTokens(model.TextRequest{System: full.System, Input: base})
		budget := inputLimit - baseTokens - budgetHeadroomTokens
		if budget < recentWindowMinTokens {
			budget = recentWindowMinTokens
		}
		kept := groupsWithinBudget(groups, budget)
		if len(kept) >= len(groups) {
			return full, false
		}
		return renderMemoryWindow(full, base, m, scope, flattenGroups(kept), query), true
	}
	return material
}

// renderMemoryWindow builds the required block from the untouched base text and an
// explicit set of groups. Rendering from the parts keeps the digest header, the source
// list and the declined backlog consistent with what was actually included.
func renderMemoryWindow(material contextMaterial, base string, m turn.MemoryContext, scope string, block []memorymodel.MemorySource, query string) contextMaterial {
	material.Required = base
	material.RequiredSources = nil
	material.DeclinedSources = nil
	material.Optional = nil
	groups := memoryGroups(block)
	included := map[string]bool{}
	for _, record := range block {
		included[record.ID] = true
	}
	label := fmt.Sprintf("最近的已发生经历（共%d组）", len(groups))
	declinedCount := len(m.Tail) - len(block)
	if declinedCount > 0 {
		label = fmt.Sprintf("最近的已发生经历（本次提供最近%d组；另有%d条更早经历尚未整理、本次未提供，按需检索，未提供不代表没有发生）", len(groups), declinedCount)
	}
	material.Required = "已提交的连续个人回顾（非世界客观事实）：" + digestContext(m.Digest) + "\n" + label + "（均已发生，不重演）：\n" + memoryRecordsText(block) + "\n本轮职责与刺激：\n" + material.Required
	material.RequiredSources = append(material.RequiredSources, retainedStateSources(m.Digest)...)
	if m.Digest.Revision > 0 {
		material.RequiredSources = append(material.RequiredSources, fmt.Sprintf("digest:%s:%d", scope, m.Digest.Revision))
	}
	for _, record := range block {
		material.RequiredSources = append(material.RequiredSources, record.ID)
	}
	// Groups that did not fit stay retrievable: they remain in the archive and are
	// reported as declined rather than as already retrieved.
	backlog := [][]memorymodel.MemorySource{}
	for _, group := range memoryGroups(m.Tail) {
		if len(group) > 0 && !included[group[0].ID] {
			backlog = append(backlog, group)
		}
	}
	for i := len(backlog) - 1; i >= 0; i-- {
		section := contextSection{Name: "memory_recent_backlog", Text: "较早的未整理经历（本次未全部提供，可用检索取回）：\n" + memoryRecordsText(backlog[i])}
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
	return withRecall(material, memoryProjection{context: m, supplied: supplied}, query)
}

// groupsWithinBudget keeps the newest complete groups that fit, and always keeps the
// newest one so a turn never loses its own most recent context.
func groupsWithinBudget(groups [][]memorymodel.MemorySource, budget int) [][]memorymodel.MemorySource {
	if len(groups) == 0 {
		return nil
	}
	for start := 0; start < len(groups); start++ {
		candidate := groups[start:]
		if framedContextTokens(model.TextRequest{Input: memoryRecordsText(flattenGroups(candidate))}) <= budget || start == len(groups)-1 {
			return candidate
		}
	}
	return groups[len(groups)-1:]
}

// withLongMemoryWindow renders the required block from an explicit set of groups.
func withLongMemoryWindow(material contextMaterial, m turn.MemoryContext, kept [][]memorymodel.MemorySource, query string) contextMaterial {
	rebuilt := contextMaterial{
		PolicyRevision: material.PolicyRevision,
		System:         material.System,
		Required:       material.Required,
		Optional:       []contextSection{},
	}
	// Strip the previous recent block and its backlog, then render the smaller window.
	if index := strings.Index(rebuilt.Required, "最近的已发生经历"); index >= 0 {
		if end := strings.Index(rebuilt.Required[index:], "\n本轮职责与刺激："); end >= 0 {
			head := rebuilt.Required[:index]
			tail := rebuilt.Required[index+end+len("\n本轮职责与刺激："):]
			rebuilt.Required = head + "本轮职责与刺激：" + tail
		}
	}
	block := flattenGroups(kept)
	supplied := map[string]bool{}
	for _, record := range block {
		supplied[record.ID] = true
	}
	declined := len(m.Tail) - len(block)
	label := fmt.Sprintf("最近的已发生经历（共%d组）", len(kept))
	if declined > 0 {
		label = fmt.Sprintf("最近的已发生经历（本次提供最近%d组；另有%d条更早经历尚未整理、本次未提供，按需检索，未提供不代表没有发生）", len(kept), declined)
	}
	rebuilt.Required = "已提交的连续个人回顾（非世界客观事实）：" + digestContext(m.Digest) + "\n" + label + "（均已发生，不重演）：\n" + memoryRecordsText(block) + "\n" + rebuilt.Required
	rebuilt.RequiredSources = append([]string{}, material.RequiredSources...)
	keptIDs := map[string]bool{}
	for _, record := range block {
		keptIDs[record.ID] = true
	}
	filtered := []string{}
	for _, id := range rebuilt.RequiredSources {
		if keptIDs[id] || !strings.HasPrefix(id, "memory:") {
			filtered = append(filtered, id)
		}
	}
	rebuilt.RequiredSources = filtered
	older := [][]memorymodel.MemorySource{}
	for _, group := range memoryGroups(m.Tail) {
		if len(group) > 0 && !keptIDs[group[0].ID] {
			older = append(older, group)
		}
	}
	for i := len(older) - 1; i >= 0; i-- {
		section := contextSection{Name: "memory_recent_backlog", Text: "较早的未整理经历（本次未全部提供，可用检索取回）：\n" + memoryRecordsText(older[i])}
		for _, record := range older[i] {
			section.Sources = append(section.Sources, record.ID)
			rebuilt.DeclinedSources = append(rebuilt.DeclinedSources, record.ID)
		}
		rebuilt.Optional = append(rebuilt.Optional, section)
	}
	rebuilt = withRecall(rebuilt, memoryProjection{context: m, supplied: supplied}, query)
	// A reduced material stays reducible, so a caller can keep asking for a smaller
	// window until it can no longer shrink.
	rebuilt.Bounded = func(inputLimit int) (contextMaterial, bool) { return rebuilt, false }
	return rebuilt
}

// selectRecentWindow keeps the newest complete experience groups that fit the request's
// remaining input budget. It falls back to the size-based rule when no budget is known,
// always keeps the newest group, and never advances the digest watermark: supplying
// fewer groups must not claim they were summarized.
func selectRecentWindow(items []memorymodel.MemorySource, available int) (block []memorymodel.MemorySource, backlog [][]memorymodel.MemorySource, supplied map[string]bool) {
	supplied = map[string]bool{}
	groups := memoryGroups(items)
	if len(groups) == 0 {
		return nil, nil, supplied
	}
	if available <= 0 {
		block, backlog, supplied = projectRecentExperience(items)
		return block, backlog, supplied
	}
	start := len(groups) - targetRecentGroups
	if start < 0 {
		start = 0
	}
	// Drop older groups while the window does not fit the budget, and drop it entirely
	// when it is larger than the size rule allows.
	for start < len(groups)-1 {
		candidate := groups[start:]
		if framedContextTokens(model.TextRequest{Input: memoryRecordsText(flattenGroups(candidate))}) <= available &&
			len(memoryRecordsText(flattenGroups(candidate))) <= recentWindowChars {
			break
		}
		start++
	}
	block = flattenGroups(groups[start:])
	for _, record := range block {
		supplied[record.ID] = true
	}
	backlog = groups[:start]
	return block, backlog, supplied
}

// budgetHeadroomTokens covers the labels, digest and framing the projection adds on top
// of the raw records it measures.
const (
	budgetHeadroomTokens = 256
	// outputReserveTokens is the generation room a request keeps before it can spend
	// tokens on input; the conservative default mirrors the composer.
	outputReserveTokens = 2048
)

// projectRecentExperience splits the unsummarized tail into the groups supplied to
// this request and the older backlog that stays recall-only. The digest watermark
// is never advanced here: supplying fewer groups must not claim they were summarized.
func projectRecentExperience(items []memorymodel.MemorySource) (block []memorymodel.MemorySource, backlog [][]memorymodel.MemorySource, supplied map[string]bool) {
	supplied = map[string]bool{}
	groups := memoryGroups(items)
	if len(groups) == 0 {
		return nil, nil, supplied
	}
	start := len(groups) - targetRecentGroups
	if start < 0 {
		start = 0
	}
	if len(groups)-start > 1 && len(memoryRecordsText(flattenGroups(groups[start:]))) > recentWindowChars {
		for start < len(groups)-1 && len(memoryRecordsText(flattenGroups(groups[start:]))) > recentWindowChars {
			start++
		}
	}
	block = flattenGroups(groups[start:])
	for _, record := range block {
		supplied[record.ID] = true
	}
	backlog = groups[:start]
	return block, backlog, supplied
}

func flattenGroups(groups [][]memorymodel.MemorySource) []memorymodel.MemorySource {
	out := []memorymodel.MemorySource{}
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

const memoryCorrectionRule = "\n记录类型 correction:* 是对本人资料已经生效的纠正，优先于此前关于同一内容的解释、回忆或自己的旧对白。保留曾经说过旧话这一历史，但后续判断使用纠正后的内容；纠正本身不是故事里新发生的对话，也不授予其他人物这些知识。"

func digestContext(d memorymodel.MemoryDigest) string {
	return wire.MarshalJSON(struct {
		Scope   string                        `json:"scope"`
		Through int64                         `json:"through_seq"`
		Content string                        `json:"content"`
		States  []memorymodel.SubjectiveState `json:"states"`
	}{d.Scope, d.Through, d.Content, d.States})
}

// Recent requests carry a bounded window of committed groups. The newest group
// stays required; older groups inside the window follow as optional material so the
// composer can drop them whole when the complete request does not fit.
const (
	targetRecentGroups = 4
	recentWindowChars  = 8000
	// The smallest recent window a request may be reduced to: one complete group.
	recentWindowMinTokens = 512
)

type memoryProjection struct {
	context turn.MemoryContext
	// supplied marks the records this request already provides; anything else the
	// receiver may lawfully recall stays eligible for retrieval.
	supplied map[string]bool
}

func (p memoryProjection) alreadySupplied(id string) bool {
	return p.supplied[id]
}

func withRecall(material contextMaterial, projection memoryProjection, query string) contextMaterial {
	m := projection.context
	hits := searchMemory(m.Archive, query, 5)
	groups := memoryGroups(m.Archive)
	var selected []contextSection
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
			section := contextSection{Name: "memory_recall", Text: "检索到的本人旧经历（同一已提交回合）：\n" + memoryRecordsText(group)}
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

func retainedStateSources(d memorymodel.MemoryDigest) []string {
	var ids []string
	for _, state := range d.States {
		for _, id := range state.Sources {
			if !wiaworld.ContainsID(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// Search receives one authorized scope, not the world's unprojected events.
func searchMemory(items []memorymodel.MemorySource, query string, limit int) []memorymodel.MemorySource {
	tokens := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		rs := []rune(word)
		if len(rs) < 2 {
			continue
		}
		tokens[word] = true
		for i := 0; i+1 < len(rs); i++ {
			tokens[string(rs[i:i+2])] = true
		}
	}
	type ranked struct {
		s     memorymodel.MemorySource
		score int
	}
	var candidates []ranked
	for _, s := range items {
		score := 0
		text := strings.ToLower(s.Content)
		for token := range tokens {
			if strings.Contains(text, token) {
				score++
			}
		}
		if score > 0 {
			candidates = append(candidates, ranked{s, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].s.Seq > candidates[j].s.Seq
		}
		return candidates[i].score > candidates[j].score
	})
	result := []memorymodel.MemorySource{}
	for i := 0; i < min(limit, len(candidates)); i++ {
		result = append(result, candidates[i].s)
	}
	return result
}
