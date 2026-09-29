package storyapp

import (
	"context"
	"time"

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
			groups := memorymodel.MemoryGroups(m.Tail)
			if len(groups) <= 4 || (len(groups) <= 8 && turn.FramedContextTokens(model.TextRequest{System: "memory", Input: memorymodel.MemoryRecordsText(m.Tail)}) <= 6000) {
				break
			}
			prefix := []memorymodel.MemorySource{}
			for _, group := range groups[:len(groups)-4] {
				candidate := append(append([]memorymodel.MemorySource{}, prefix...), group...)
				if turn.FramedContextTokens(model.TextRequest{System: "memory", Input: memorymodel.MemoryRecordsText(candidate)}) > 6500 && len(prefix) > 0 {
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
	allowed := memorymodel.RetainedStateSources(previous)
	for _, s := range prefix {
		if !wiaworld.ContainsID(allowed, s.ID) {
			allowed = append(allowed, s.ID)
		}
		if !wiaworld.ContainsID(sources, s.ID) {
			sources = append(sources, s.ID)
		}
	}
	material := turn.Material{System: "你整理单一接收者已经提交的经历，不执行故事，不读取其他人物资料。按时间组织回顾，保留关键约定、结果及来源。尝试不等于成功，主观判断不等于事实，玩家文学正文只作玩家经历参考。只返回JSON：content字符串、states数组。states每项仅含kind、content、source_ids；kind为belief/relationship/concern/commitment，source_ids只引用获准来源。保留有效旧状态，已完成关切标明完成而非继续当待办。回顾简洁，通常不超过1000字。", Required: "接收者：" + scope + "\n已有连续回顾：" + memorymodel.DigestContext(previous) + "\n新增连续经历：\n" + memorymodel.MemoryRecordsText(prefix), RequiredSources: allowed}
	material.Required += "\nsource_ids 的完整合法记录ID列表：" + wire.MarshalJSON(allowed) + "\n本次列表仅含保留状态的必要来源与新增记录，完整历史覆盖仍由存档维护。每条状态的 source_ids 只从此列表原样选择。经历中的来源事件字段是溯源元数据，不是此处可填写的个人记录ID。没有可保留状态时 states 返回[]。"
	material.System += turn.MemoryCorrectionRule
	call := a.contextGenerator(g, material, snapshot, run, "memory_digest", scope, 0, "story.memory.v3")
	var result struct {
		Content string                        `json:"content"`
		States  []memorymodel.SubjectiveState `json:"states"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := turn.GenerateJSON(callCtx, call, material.System, material.Required, &result, turn.StructuredTurnOutputTokens, "content", "states"); err != nil {
		return d, err
	}
	if wire.Clean(result.Content) == "" || result.States == nil {
		a.logMemoryValidation(snapshot.Summary.WorldID, scope, "required_fields")
		return d, turn.ErrGenerationFailed
	}
	for _, state := range result.States {
		if !wiaworld.ContainsID([]string{"belief", "relationship", "concern", "commitment"}, state.Kind) || wire.Clean(state.Content) == "" || len(state.Sources) == 0 {
			a.logMemoryValidation(snapshot.Summary.WorldID, scope, "state_fields")
			return d, turn.ErrGenerationFailed
		}
		for _, id := range state.Sources {
			if !wiaworld.ContainsID(allowed, id) {
				a.logMemoryValidation(snapshot.Summary.WorldID, scope, "state_source")
				return d, turn.ErrGenerationFailed
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

// outputReserveTokens is the generation room a request keeps before it can spend
// tokens on input; the conservative default mirrors the composer.
const outputReserveTokens = 2048
