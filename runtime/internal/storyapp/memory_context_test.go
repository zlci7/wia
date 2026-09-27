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
