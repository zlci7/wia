package storyapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gameagent/runtime/internal/model"
)

type digestGenerator struct {
	requests []model.TextRequest
	fail     bool
}

type recallProbe struct {
	requests []model.TextRequest
	always   bool
}

func (g *recallProbe) GenerateText(_ context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, r)
	if len(g.requests) == 1 || g.always {
		return model.TextResponse{Text: `{"speech":"","action_intent":"","silent":true,"memory":"","recall_query":"铜钥匙"}`}, nil
	}
	return model.TextResponse{Text: `{"speech":"我记得这件事。","action_intent":"","silent":false,"memory":"重新想起约定。"}`}, nil
}

func TestNPCRecallRoundTripAndStageFiveMemory(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.CreateWorld(ctx, "检索", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	s.LongMemory = map[string]memoryContext{
		"npc:innkeeper": {Archive: []MemorySource{{ID: "personal-old", Seq: 1, Content: "铜钥匙须在柜台归还"}}},
		"npc:mercenary": {Archive: []MemorySource{{ID: "other-secret", Seq: 1, Content: "他人的铜钥匙秘密"}}},
	}
	s.Perceptions["npc:innkeeper"] = []Perception{{SourceEventID: "current-done", Content: "此前已完成添茶，不是新提案。", SourceType: "action_result"}}
	s.Sources["current-done"] = sourceMetadata{ID: "current-done", Actor: "npc:innkeeper", Kind: "npc_action_result"}
	input := map[string]npcStageInput{"npc:innkeeper": {NewStimulus: "新的铃声"}}
	g := &recallProbe{}
	decisions := map[string]npcDecision{}
	if err = a.decideNPCs(ctx, g, s, lanternDefinition(), Run{RunID: "probe", BaseContextEpoch: w.ContextEpoch}, "npc:innkeeper", "speak", input, nil, decisions, 5); err != nil {
		t.Fatal(err)
	}
	if len(g.requests) != 2 || strings.Contains(g.requests[0].Input, "柜台归还") || !strings.Contains(g.requests[1].Input, "柜台归还") || strings.Contains(g.requests[1].Input, "他人的铜钥匙秘密") {
		t.Fatal("retrieval scope or execution incorrect")
	}
	if !strings.Contains(g.requests[1].Input, "此前已完成添茶") {
		t.Fatal("stage five lost current results")
	}
	g = &recallProbe{always: true}
	if err = a.decideNPCs(ctx, g, s, lanternDefinition(), Run{RunID: "bounded", BaseContextEpoch: w.ContextEpoch}, "npc:innkeeper", "speak", input, nil, map[string]npcDecision{}, 5); err == nil || len(g.requests) != 3 {
		t.Fatal("unbounded recall", err, len(g.requests))
	}
}

func (g *digestGenerator) GenerateText(_ context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.requests = append(g.requests, r)
	if g.fail {
		return model.TextResponse{}, errors.New("unavailable")
	}
	return model.TextResponse{Text: `{"content":"我答应在码头归还铜钥匙。","states":[]}`}, nil
}

func TestMemoryScopeContinuityAndCompaction(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.CreateWorld(ctx, "memory", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	for i := 1; i <= 12; i++ {
		run := fmt.Sprintf("history-%02d", i)
		if _, err = store.db.Exec(`INSERT INTO runs(run_id,request_key,request_hash,input,addressee_id,attempt,status,created_at,updated_at) VALUES(?,?,?,'','',1,'completed','now','now')`, run, run, run); err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.Exec(`INSERT INTO events VALUES(?,?,?,?,?,?,?,?,?,?,?)`, i+100, "e"+run, "player_attempt", "player", "npc:innkeeper", "秘密铜钥匙", run, 1, 1, "player_private", "now"); err != nil {
			t.Fatal(err)
		}
		for _, scope := range []string{"npc:innkeeper", "npc:mercenary"} {
			content := "看见一次交谈"
			if scope == "npc:innkeeper" {
				content = "承诺在码头归还铜钥匙"
			}
			if _, err = store.db.Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES(?,?,'player_private',?,1,1,'now')`, scope, "e"+run, content); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot, err := loadWorldSnapshot(ctx, store, 40)
	if err != nil {
		t.Fatal(err)
	}
	g := &digestGenerator{}
	if err = a.prepareLongMemory(ctx, store, &snapshot, Run{BaseContextEpoch: w.ContextEpoch}, g); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"npc:innkeeper", "npc:mercenary"} {
		m := snapshot.LongMemory[scope]
		if len(m.Archive) != 12 || len(m.Tail) != 4 || m.Digest.Through != 8 {
			t.Fatalf("scope=%s archive=%d tail=%d through=%d", scope, len(m.Archive), len(m.Tail), m.Digest.Through)
		}
		for i, s := range m.Archive {
			if s.Seq != int64(i+1) {
				t.Fatal("personal sequence gap")
			}
		}
	}
	if len(g.requests) != 2 {
		t.Fatalf("calls=%d", len(g.requests))
	}
	for _, r := range g.requests {
		if !strings.Contains(r.Input, "source_ids 的完整合法记录ID列表：") {
			t.Fatal("record and event ID domains are ambiguous")
		}
		if strings.Contains(r.Input, "接收者：npc:mercenary") && strings.Contains(r.Input, "铜钥匙") {
			t.Fatal("private event leaked into bystander digest request")
		}
	}
	if err = indexMemorySources(ctx, store); err != nil {
		t.Fatal(err)
	}
	items, _ := readMemorySources(ctx, store.db, "npc:innkeeper", 0)
	if len(items) != 12 {
		t.Fatal("index not idempotent")
	}
	hits := searchMemory(snapshot.LongMemory["npc:mercenary"].Archive, "铜钥匙", 5)
	if len(hits) != 0 {
		t.Fatal("cross-scope search")
	}
	material := withLongMemory(contextMaterial{System: "NPC", Required: "本轮", Optional: []contextSection{{Text: "old"}}}, snapshot, "npc:innkeeper", "铜钥匙")
	if len(material.Optional) != 0 || !strings.Contains(material.Required, "perception:23") {
		t.Fatal("recent tail missing")
	}
	stale := snapshot.LongMemory["npc:innkeeper"].Digest
	stale.Revision++
	if err = publishDigest(ctx, store, stale, 0, w.ContextEpoch); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("late revision published", err)
	}
	if _, err = store.db.Exec(`UPDATE meta SET value='2' WHERE key='context_epoch'`); err != nil {
		t.Fatal(err)
	}
	if err = publishDigest(ctx, store, stale, 1, w.ContextEpoch); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("late epoch published", err)
	}
}

func TestMemoryOptionalFailureKeepsCompleteTail(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.CreateWorld(ctx, "保留历史", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, w.WorldID)
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	s, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	snapshot, err := loadWorldSnapshot(ctx, s, 40)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.prepareLongMemory(ctx, s, &snapshot, Run{BaseContextEpoch: w.ContextEpoch}, &digestGenerator{fail: true}); err != nil {
		t.Fatal(err)
	}
	m := snapshot.LongMemory["npc:innkeeper"]
	if m.Digest.Through != 0 || len(m.Tail) != 10 || len(m.Archive) != 10 {
		t.Fatal("optional failure discarded history", m)
	}
	for _, bad := range []string{`{"content":"回顾","states":[{"kind":"commitment","content":"还钥匙","source_ids":["fixture-history-01:input"]}]}`, `{"content":"回顾","states":[{"kind":"commitment","content":"还钥匙","source_ids":["npc:mercenary"]}]}`} {
		if _, err = a.summarizeMemory(ctx, fixedJSONGenerator{text: bad}, snapshot, Run{BaseContextEpoch: w.ContextEpoch}, "npc:innkeeper", m.Digest, m.Archive[:6]); err == nil {
			t.Fatal("unauthorized source accepted")
		}
	}
	valid := `{"content":"回顾","states":[{"kind":"commitment","content":"还钥匙","source_ids":["perception:1"]}]}`
	if _, err = a.summarizeMemory(ctx, fixedJSONGenerator{text: valid}, snapshot, Run{BaseContextEpoch: w.ContextEpoch}, "npc:innkeeper", m.Digest, m.Archive[:6]); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryTailIsRequiredAndSearchBounded(t *testing.T) {
	var archive []MemorySource
	for i := 1; i <= 20; i++ {
		archive = append(archive, MemorySource{ID: fmt.Sprint(i), Seq: int64(i), RunID: fmt.Sprint(i / 2), Content: "旧码头的铜钥匙"})
	}
	if got := searchMemory(archive, "铜钥匙", 5); len(got) != 5 || got[0].Seq != 20 {
		t.Fatal(got)
	}
	s := worldSnapshot{LongMemory: map[string]memoryContext{"player": {Tail: []MemorySource{{Content: strings.Repeat("完整经历", 15000)}}}}}
	m := withLongMemory(contextMaterial{System: "test", Required: "current"}, s, "player", "")
	if _, _, err := (ContextComposer{}).Build(m, m.System, 100); !errors.Is(err, ErrContextCapacity) {
		t.Fatal("silently truncated recent history", err)
	}
}

func TestMemoryPaginationUsesMessageOrderAndExcludesFailedRuns(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.CreateWorld(ctx, "历史分页", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	s, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 251; i++ {
		if _, err = s.db.Exec(`INSERT INTO messages VALUES(?,?,'narrative',?,'','now')`, i, fmt.Sprintf("reverse-%03d", 300-i), fmt.Sprintf("历史%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`INSERT INTO runs(run_id,request_key,request_hash,input,addressee_id,attempt,status,created_at,updated_at) VALUES('failed','failed','failed','失败原话','',1,'failed','now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO messages VALUES(252,'failed-input','player','失败原话','failed','now')`); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	before := int64(0)
	count := 0
	for page := 0; page < 3; page++ {
		v, e := a.ReadMemory(ctx, w.WorldID, "player", false, before)
		if e != nil {
			t.Fatal(e)
		}
		for _, source := range v.Sources {
			want := int64(251 - count)
			if source.Seq != want || (want > 1 && source.Content != fmt.Sprintf("历史%d", want)) || strings.Contains(source.Content, "失败原话") {
				t.Fatal("incorrect history order", source, want)
			}
			count++
		}
		if page < 2 && (!v.HasMore || v.NextBefore == 0) {
			t.Fatal("missing page")
		}
		if page == 2 && v.HasMore {
			t.Fatal("incorrect terminal page")
		}
		before = v.NextBefore
	}
	if count != 251 {
		t.Fatal(count)
	}
}

func TestSubjectiveMemoryIdentifiesItsOwner(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.CreateWorld(ctx, "主观来源", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, w.WorldID)
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	s, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO memories(recipient_id,kind,content,source_event_id,created_at) VALUES('npc:innkeeper','character_judgment','我还不能信任他。','fixture-history-01:input','now')`)
	s.db.Close()
	if err != nil {
		t.Fatal(err)
	}
	v, err := a.ReadMemory(ctx, w.WorldID, "npc:innkeeper", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, source := range v.Sources {
		if strings.HasPrefix(source.Kind, "subjective:") {
			found = true
			if source.Actor != "npc:innkeeper" || source.EventID != "fixture-history-01:input" {
				t.Fatal("judgment owner confused with triggering speaker", source)
			}
		}
	}
	if !found {
		t.Fatal("subjective memory absent")
	}
}
