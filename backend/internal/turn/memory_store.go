package turn

import (
	"context"
	"errors"
	"time"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

var ErrVersionConflict = errors.New("version conflict")

// LoadLongMemory projects every scope's committed memory stream into the snapshot.
func LoadLongMemory(ctx context.Context, store *storage.WorldStore, snapshot *Snapshot) error {
	if err := memory.IndexSources(ctx, store); err != nil {
		return err
	}
	snapshot.LongMemory = map[string]MemoryContext{}
	for _, scope := range memory.ScopeIDs(snapshot.Characters) {
		archive, err := memory.ReadSources(ctx, store, scope, 0)
		if err != nil {
			return err
		}
		d, err := memory.ReadDigestAgainst(ctx, store, scope, archive)
		if err != nil {
			return err
		}
		m := MemoryContext{Digest: d, Archive: archive}
		for _, s := range archive {
			if s.Seq > d.Through {
				m.Tail = append(m.Tail, s)
			}
		}
		snapshot.LongMemory[scope] = m
	}
	return nil
}

// PrepareLongMemory loads memory and publishes bounded standing digests before the
// turn begins. Model calls happen outside the publication transaction.
func (s *Service) PrepareLongMemory(ctx context.Context, store *storage.WorldStore, snapshot *Snapshot, run wiaworld.Run, generator model.TextGenerator) error {
	if err := LoadLongMemory(ctx, store, snapshot); err != nil {
		return err
	}
	snapshot.InputBudgetTokens = inputBudgetTokens(generator)
	for _, scope := range memory.ScopeIDs(snapshot.Characters) {
		for maintenance := 0; maintenance < 3; maintenance++ {
			m := snapshot.LongMemory[scope]
			groups := memory.MemoryGroups(m.Tail)
			if len(groups) <= 4 || (len(groups) <= 8 && model.FramedTextInputTokens(model.TextRequest{System: "memory", Input: memory.MemoryRecordsText(m.Tail)}) <= 6000) {
				break
			}
			prefix := []memory.MemorySource{}
			for _, group := range groups[:len(groups)-4] {
				candidate := append(append([]memory.MemorySource{}, prefix...), group...)
				if model.FramedTextInputTokens(model.TextRequest{System: "memory", Input: memory.MemoryRecordsText(candidate)}) > 6500 && len(prefix) > 0 {
					break
				}
				prefix = candidate
			}
			d, err := s.SummarizeMemory(ctx, generator, *snapshot, run, scope, m.Digest, prefix)
			if err != nil {
				if s.deps.Logger != nil {
					s.deps.Logger.Printf("story memory maintenance: world_id=%q scope=%q success=false error_code=%q", snapshot.Summary.WorldID, scope, ErrorCode(err))
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				break
			}
			d.Head = m.Archive[len(m.Archive)-1].Seq
			if err = PublishDigest(ctx, store, d, m.Digest.Revision, run.BaseContextEpoch); err != nil {
				return err
			}
			m.Digest = d
			m.Tail = nil
			for _, source := range m.Archive {
				if source.Seq > d.Through {
					m.Tail = append(m.Tail, source)
				}
			}
			snapshot.LongMemory[scope] = m
		}
	}
	return nil
}

// SummarizeMemory turns a committed prefix into one scope's standing digest.
func (s *Service) SummarizeMemory(ctx context.Context, g model.TextGenerator, snapshot Snapshot, run wiaworld.Run, scope string, previous memory.MemoryDigest, prefix []memory.MemorySource) (memory.MemoryDigest, error) {
	d := previous
	if len(prefix) == 0 {
		return d, nil
	}
	sources := append([]string{}, previous.Sources...)
	allowed := memory.RetainedStateSources(previous)
	for _, source := range prefix {
		if !wiaworld.ContainsID(allowed, source.ID) {
			allowed = append(allowed, source.ID)
		}
		if !wiaworld.ContainsID(sources, source.ID) {
			sources = append(sources, source.ID)
		}
	}
	material := Material{System: "你整理单一接收者已经提交的经历，不执行故事，不读取其他人物资料。按时间组织回顾，保留关键约定、结果及来源。尝试不等于成功，主观判断不等于事实，玩家文学正文只作玩家经历参考。只返回JSON：content字符串、states数组。states每项仅含kind、content、source_ids；kind为belief/relationship/concern/commitment，source_ids只引用获准来源。保留有效旧状态，已完成关切标明完成而非继续当待办。回顾简洁，通常不超过1000字。", Required: "接收者：" + scope + "\n已有连续回顾：" + memory.DigestContext(previous) + "\n新增连续经历：\n" + memory.MemoryRecordsText(prefix), RequiredSources: allowed}
	material.Required += "\nsource_ids 的完整合法记录ID列表：" + wire.MarshalJSON(allowed) + "\n本次列表仅含保留状态的必要来源与新增记录，完整历史覆盖仍由存档维护。每条状态的 source_ids 只从此列表原样选择。经历中的来源事件字段是溯源元数据，不是此处可填写的个人记录ID。没有可保留状态时 states 返回[]。"
	material.System += MemoryCorrectionRule
	call := NewContextGenerator(s.deps, s.deps.Owner, g, material, snapshot, run, "memory_digest", scope, 0, "story.memory.v3")
	var result struct {
		Content string                   `json:"content"`
		States  []memory.SubjectiveState `json:"states"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := GenerateJSON(callCtx, call, material.System, material.Required, &result, structuredTurnOutputTokens, "content", "states"); err != nil {
		return d, err
	}
	if wire.Clean(result.Content) == "" || result.States == nil {
		s.logMemoryValidation(snapshot.Summary.WorldID, scope, "required_fields")
		return d, ErrGenerationFailed
	}
	for _, state := range result.States {
		if !wiaworld.ContainsID([]string{"belief", "relationship", "concern", "commitment"}, state.Kind) || wire.Clean(state.Content) == "" || len(state.Sources) == 0 {
			s.logMemoryValidation(snapshot.Summary.WorldID, scope, "state_fields")
			return d, ErrGenerationFailed
		}
		for _, id := range state.Sources {
			if !wiaworld.ContainsID(allowed, id) {
				s.logMemoryValidation(snapshot.Summary.WorldID, scope, "state_source")
				return d, ErrGenerationFailed
			}
		}
	}
	return memory.MemoryDigest{Scope: scope, Revision: previous.Revision + 1, Epoch: run.BaseContextEpoch, Through: prefix[len(prefix)-1].Seq, Head: prefix[len(prefix)-1].Seq, Content: result.Content, States: result.States, Sources: sources}, nil
}

func (s *Service) logMemoryValidation(world, scope, boundary string) {
	if s.deps.Logger != nil {
		s.deps.Logger.Printf("story memory validation failed: world_id=%q scope=%q boundary=%s", world, scope, boundary)
	}
}

// PublishDigest records one scope's standing summary only while the world still
// matches the source stream the caller read.
func PublishDigest(ctx context.Context, store *storage.WorldStore, d memory.MemoryDigest, previous, epoch int64) error {
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

func inputBudgetTokens(generator model.TextGenerator) int {
	limit := 12000
	if provider, ok := generator.(model.WindowProvider); ok {
		window := provider.ModelWindow()
		if window != (model.WindowLimits{}) && window.Validate() == nil {
			reserved := 2048
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

func loadCoordinationEvidence(ctx context.Context, store *storage.WorldStore, snapshot *Snapshot, input string) error {
	events, err := store.LoadEvents(ctx, int(snapshot.Summary.EventHead))
	if err != nil {
		return err
	}
	effective := Snapshot{Events: events}
	if err = applySnapshotCorrections(ctx, store, &effective); err != nil {
		return err
	}
	events = effective.Events
	runs := []string{}
	seen := map[string]bool{}
	records := []memory.MemorySource{}
	for _, event := range events {
		if !seen[event.RunID] {
			seen[event.RunID] = true
			runs = append(runs, event.RunID)
		}
		records = append(records, memory.MemorySource{ID: event.EventID, Seq: event.Seq, RunID: event.RunID, Content: event.Content})
	}
	selected := map[string]bool{}
	for i := max(0, len(runs)-4); i < len(runs); i++ {
		selected[runs[i]] = true
	}
	queries := []string{input}
	for _, event := range snapshot.GeneratedEvents.Active {
		queries = append(queries, event.Node.Condition)
		for _, source := range events {
			if source.EventID == event.StartID || source.EventID == event.TriggerID {
				selected[source.RunID] = true
			}
		}
	}
	if snapshot.Plot != nil {
		for _, node := range snapshot.Plot.Nodes {
			queries = append(queries, node.Condition)
		}
	}
	for _, query := range queries {
		for _, source := range memory.SearchMemory(records, query, 5) {
			selected[source.RunID] = true
		}
	}
	snapshot.Events = nil
	for _, event := range events {
		if selected[event.RunID] {
			snapshot.Events = append(snapshot.Events, event)
		}
	}
	return nil
}

func (s *Service) loadInput(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, generator model.TextGenerator, limit int) (Snapshot, error) {
	snapshot, err := LoadInputSnapshot(ctx, store, limit)
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.PrepareLongMemory(ctx, store, &snapshot, run, generator); err != nil {
		return Snapshot{}, err
	}
	if err = loadCoordinationEvidence(ctx, store, &snapshot, run.Input); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}
