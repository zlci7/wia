package turn

import (
	"errors"
	"reflect"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

func TestMemoryDigestRejectsForeignOwnerAndPreservesPrevious(t *testing.T) {
	previous := memory.MemoryDigest{Scope: "npc:a", Revision: 2, Through: 10, Content: "本人仍有效的约定", States: []memory.SubjectiveState{{Kind: "commitment", Content: "归还钥匙", Sources: []string{"old"}}}}
	for _, digestForeign := range []bool{false, true} {
		before := previous
		prefix := []memory.MemorySource{{Scope: "npc:b", ID: "private", Seq: 11, Content: "其他人物的秘密"}}
		if digestForeign {
			before.Scope = "npc:b"
			prefix[0].Scope = "npc:a"
		}
		g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) {
			t.Fatal("foreign owner sent to digest model")
			return "", nil
		}}
		d, err := New(coordinationTestHost{}, Deps{}).SummarizeMemory(t.Context(), g, Snapshot{}, wiaworld.Run{}, "npc:a", before, prefix)
		if !errors.Is(err, ErrContextSourceMissing) || !reflect.DeepEqual(d, before) {
			t.Fatalf("invalid owner changed previous digest: %+v %v", d, err)
		}
	}
}

func TestMemoryDigestKeepsOldSummaryOnInvalidNewState(t *testing.T) {
	previous := memory.MemoryDigest{Scope: "npc:a", Revision: 2, Through: 10, Content: "有效回顾"}
	prefix := []memory.MemorySource{{Scope: "npc:a", ID: "new", Seq: 11, Content: "[世界时间=2189-12-31 23:55] 我尝试归还钥匙但失败。", CreatedAt: "2026-10-07T00:00:00Z"}}
	g := &sceneSequenceGenerator{reply: func(int, model.TextRequest) (string, error) {
		return `{"content":"新回顾","states":[{"kind":"commitment","content":"归还钥匙","source_ids":["foreign"]}]}`, nil
	}}
	d, err := New(coordinationTestHost{}, Deps{}).SummarizeMemory(t.Context(), g, Snapshot{}, wiaworld.Run{}, "npc:a", previous, prefix)
	if err == nil || !reflect.DeepEqual(d, previous) {
		t.Fatalf("invalid state replaced valid previous summary: %+v %v", d, err)
	}
}
