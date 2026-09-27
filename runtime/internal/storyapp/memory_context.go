package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"gameagent/runtime/internal/model"
)

type SubjectiveState struct {
	Kind    string   `json:"kind"`
	Content string   `json:"content"`
	Sources []string `json:"source_ids"`
}

type MemoryDigest struct {
	Scope    string            `json:"scope"`
	Revision int64             `json:"revision"`
	Epoch    int64             `json:"epoch"`
	Through  int64             `json:"through_seq"`
	Head     int64             `json:"source_head"`
	Content  string            `json:"content"`
	States   []SubjectiveState `json:"states"`
	Sources  []string          `json:"source_ids"`
}

type memoryContext struct {
	Digest  MemoryDigest
	Tail    []MemorySource
	Archive []MemorySource
}

func readDigest(ctx context.Context, db *sql.DB, scope string) (MemoryDigest, error) {
	d := MemoryDigest{Scope: scope, States: []SubjectiveState{}, Sources: []string{}}
	var states, sources string
	err := db.QueryRowContext(ctx, `SELECT revision,epoch,through_seq,source_head,content,states,sources FROM memory_digests WHERE scope=? ORDER BY revision DESC LIMIT 1`, scope).Scan(&d.Revision, &d.Epoch, &d.Through, &d.Head, &d.Content, &states, &sources)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(states), &d.States); err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(sources), &d.Sources)
	return d, err
}

func loadLongMemory(ctx context.Context, store *worldStore, snapshot *worldSnapshot) error {
	if err := indexMemorySources(ctx, store); err != nil {
		return err
	}
	snapshot.LongMemory = map[string]memoryContext{}
	for _, scope := range memoryScopeIDs(*snapshot) {
		archive, err := readMemorySources(ctx, store.db, scope, 0)
		if err != nil {
			return err
		}
		d, err := readDigest(ctx, store.db, scope)
		if err != nil {
			return err
		}
		m := memoryContext{Digest: d, Archive: archive}
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
func (a *App) prepareLongMemory(ctx context.Context, store *worldStore, snapshot *worldSnapshot, run Run, generator model.TextGenerator) error {
	if err := loadLongMemory(ctx, store, snapshot); err != nil {
		return err
	}
	for _, scope := range memoryScopeIDs(*snapshot) {
		for maintenance := 0; maintenance < 3; maintenance++ {
			m := snapshot.LongMemory[scope]
			groups := memoryGroups(m.Tail)
			if len(groups) <= 4 || (len(groups) <= 8 && framedContextTokens(model.TextRequest{System: "memory", Input: memoryRecordsText(m.Tail)}) <= 6000) {
				break
			}
			prefix := []MemorySource{}
			for _, group := range groups[:len(groups)-4] {
				candidate := append(append([]MemorySource{}, prefix...), group...)
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

func (a *App) summarizeMemory(ctx context.Context, g model.TextGenerator, snapshot worldSnapshot, run Run, scope string, previous MemoryDigest, prefix []MemorySource) (MemoryDigest, error) {
	d := previous
	if len(prefix) == 0 {
		return d, nil
	}
	sources := append([]string{}, previous.Sources...)
	for _, s := range prefix {
		if !containsID(sources, s.ID) {
			sources = append(sources, s.ID)
		}
	}
	material := contextMaterial{System: "你整理单一接收者已经提交的经历，不执行故事，不读取其他人物资料。按时间组织回顾，保留关键约定、结果及来源。尝试不等于成功，主观判断不等于事实，玩家文学正文只作玩家经历参考。只返回JSON：content字符串、states数组。states每项仅含kind、content、source_ids；kind为belief/relationship/concern/commitment，source_ids只引用获准来源。保留有效旧状态，已完成关切标明完成而非继续当待办。回顾简洁，通常不超过1000字。", Required: "接收者：" + scope + "\n已有连续回顾：" + digestContext(previous) + "\n新增连续经历：\n" + memoryRecordsText(prefix), RequiredSources: sources}
	material.Required += "\nsource_ids 的完整合法记录ID列表：" + marshalJSON(sources) + "\n每条状态的 source_ids 只从此列表原样选择。经历中的来源事件字段是溯源元数据，不是此处可填写的个人记录ID。没有可保留状态时 states 返回[]。"
	material.System += memoryCorrectionRule
	call := a.contextGenerator(g, material, snapshot, run, "memory_digest", scope, 0, "story.memory.v2")
	var result struct {
		Content string            `json:"content"`
		States  []SubjectiveState `json:"states"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := generateJSON(callCtx, call, material.System, material.Required, &result, structuredTurnOutputTokens, "content", "states"); err != nil {
		return d, err
	}
	if cleanText(result.Content) == "" || result.States == nil {
		a.logMemoryValidation(snapshot.Summary.WorldID, scope, "required_fields")
		return d, ErrGenerationFailed
	}
	for _, state := range result.States {
		if !containsID([]string{"belief", "relationship", "concern", "commitment"}, state.Kind) || cleanText(state.Content) == "" || len(state.Sources) == 0 {
			a.logMemoryValidation(snapshot.Summary.WorldID, scope, "state_fields")
			return d, ErrGenerationFailed
		}
		for _, id := range state.Sources {
			if !containsID(sources, id) {
				a.logMemoryValidation(snapshot.Summary.WorldID, scope, "state_source")
				return d, ErrGenerationFailed
			}
		}
	}
	return MemoryDigest{Scope: scope, Revision: previous.Revision + 1, Epoch: run.BaseContextEpoch, Through: prefix[len(prefix)-1].Seq, Head: prefix[len(prefix)-1].Seq, Content: result.Content, States: result.States, Sources: sources}, nil
}

func (a *App) logMemoryValidation(world, scope, boundary string) {
	if a.logger != nil {
		a.logger.Printf("story memory validation failed: world_id=%q scope=%q boundary=%s", world, scope, boundary)
	}
}

func publishDigest(ctx context.Context, store *worldStore, d MemoryDigest, previous, epoch int64) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actual, err := metaGetTx(ctx, tx, "context_epoch")
	if err != nil {
		return err
	}
	if actual != fmt.Sprint(epoch) {
		return ErrVersionConflict
	}
	var revision, head int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0) FROM memory_digests WHERE scope=?`, d.Scope).Scan(&revision); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM memory_sources WHERE scope=?`, d.Scope).Scan(&head); err != nil {
		return err
	}
	if revision != previous || head != d.Head || head < d.Through {
		return ErrVersionConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_digests(scope,revision,epoch,through_seq,source_head,content,states,sources,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, d.Scope, d.Revision, d.Epoch, d.Through, head, d.Content, marshalJSON(d.States), marshalJSON(d.Sources), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func withLongMemory(material contextMaterial, snapshot worldSnapshot, scope, query string) contextMaterial {
	m, ok := snapshot.LongMemory[scope]
	if !ok {
		return material
	}
	material.Optional = nil
	material.System += memoryCorrectionRule
	material.Required = "已提交的连续个人回顾（非世界客观事实）：" + digestContext(m.Digest) + "\n完整近期经历（均已发生，不重演）：\n" + memoryRecordsText(m.Tail) + "\n本轮职责与刺激：\n" + material.Required
	material.RequiredSources = append(material.RequiredSources, m.Digest.Sources...)
	for _, s := range m.Tail {
		material.RequiredSources = append(material.RequiredSources, s.ID)
	}
	material = withRecall(material, m, query)
	return material
}

const memoryCorrectionRule = "\n记录类型 correction:* 是对本人资料已经生效的纠正，优先于此前关于同一内容的解释、回忆或自己的旧对白。保留曾经说过旧话这一历史，但后续判断使用纠正后的内容；纠正本身不是故事里新发生的对话，也不授予其他人物这些知识。"

func digestContext(d MemoryDigest) string {
	return marshalJSON(struct {
		Scope   string            `json:"scope"`
		Through int64             `json:"through_seq"`
		Content string            `json:"content"`
		States  []SubjectiveState `json:"states"`
	}{d.Scope, d.Through, d.Content, d.States})
}

func withRecall(material contextMaterial, m memoryContext, query string) contextMaterial {
	hits := searchMemory(m.Archive, query, 5)
	tail := map[string]bool{}
	for _, s := range m.Tail {
		tail[s.ID] = true
	}
	for _, s := range hits {
		if !tail[s.ID] && !containsID(material.RecallSources, s.ID) {
			material.Required += "\n检索到的本人旧经历：" + memoryRecordsText([]MemorySource{s})
			material.RequiredSources = append(material.RequiredSources, s.ID)
			material.RecallSources = append(material.RecallSources, s.ID)
		}
	}
	return material
}

// Search receives one authorized scope, not the world's unprojected events.
func searchMemory(items []MemorySource, query string, limit int) []MemorySource {
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
		s     MemorySource
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
	result := []MemorySource{}
	for i := 0; i < min(limit, len(candidates)); i++ {
		result = append(result, candidates[i].s)
	}
	return result
}
