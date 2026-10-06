package memory

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferencedRecallPrioritizesCompletePersonalGroups(t *testing.T) {
	items := []MemorySource{
		{Scope: "npc:a", ID: "promise", EventID: "promise:event", RunID: "old", Seq: 1, Content: "星期二送来封套。"},
		{Scope: "npc:a", ID: "result", EventID: "result:event", RunID: "old", Seq: 2, Content: "封套未交付，承诺仍有效。"},
		{Scope: "npc:a", ID: "recent", RunID: "recent", Seq: 3, Content: "天气晴朗。"},
	}
	for _, id := range []string{"promise", "promise:event", "result:event"} {
		got := SearchMemoryGroups(items, "天气", []string{id}, nil, 1)
		if got.Limited || len(got.Groups) != 1 || len(got.Groups[0]) != 2 || got.Groups[0][0].ID != "promise" || got.Groups[0][1].ID != "result" {
			t.Fatalf("reference %s: %+v", id, got)
		}
	}
	got := SearchMemoryGroups(items, "", []string{"promise", "result:event"}, nil, 5)
	if len(got.Groups) != 1 {
		t.Fatal("same complete group recalled twice", got)
	}
	got = SearchMemoryGroups(items, "", []string{"promise:event"}, map[string]bool{"result": true}, 5)
	if len(got.Groups) != 0 || got.Limited {
		t.Fatal("already supplied complete group recalled again", got)
	}
	got = SearchMemoryGroups(items, "无匹配", []string{"another-owner:event"}, nil, 5)
	if len(got.Groups) != 0 || got.Limited {
		t.Fatal("absent source invented a group", got)
	}
}

func TestReferencedRecallSharesExistingLimits(t *testing.T) {
	t.Run("candidate window", func(t *testing.T) {
		items := make([]MemorySource, memorySearchScanCandidates+1)
		for i := range items {
			items[i] = MemorySource{ID: fmt.Sprintf("source:%d", i), RunID: fmt.Sprintf("run:%d", i), Seq: int64(i + 1), Content: "普通经历"}
		}
		got := SearchMemoryGroups(items, "", []string{items[0].ID}, nil, 5)
		if !got.Limited || len(got.Groups) != 0 {
			t.Fatal("exact reference bypassed candidate window", got)
		}
	})
	t.Run("complete group bytes", func(t *testing.T) {
		items := []MemorySource{{ID: "attempt", RunID: "old", Seq: 1, Content: strings.Repeat("x", memorySearchBytes)}, {ID: "result", RunID: "old", Seq: 2, Content: "结果"}}
		got := SearchMemoryGroups(items, "", []string{"result"}, nil, 5)
		if !got.Limited || len(got.Groups) != 0 {
			t.Fatal("partial group escaped byte limit", got)
		}
	})
	t.Run("reference count", func(t *testing.T) {
		refs := make([]string, memorySearchQueryTerms+1)
		for i := range refs {
			refs[i] = fmt.Sprintf("ref:%d", i)
		}
		got := SearchMemoryGroups([]MemorySource{{ID: refs[len(refs)-1], Seq: 1, Content: "结果"}}, "", refs, nil, 5)
		if !got.Limited || len(got.Groups) != 0 {
			t.Fatal("reference list bypassed query limit", got)
		}
	})
}
