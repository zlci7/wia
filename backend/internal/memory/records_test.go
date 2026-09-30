package memory

import (
	"fmt"
	"strings"
	"testing"
)

func TestDigestCoverageMatchesTheContinuousScopePrefix(t *testing.T) {
	items := []MemorySource{
		{Scope: "npc:a", Seq: 1, ID: "source:1"},
		{Scope: "npc:a", Seq: 2, ID: "source:2"},
		{Scope: "npc:a", Seq: 3, ID: "source:3"},
	}
	valid := MemoryDigest{Scope: "npc:a", Through: 2, Head: 3, Sources: []string{"source:1", "source:2"}}
	if !DigestCoverageMatches(valid, items) {
		t.Fatal("valid continuous coverage was rejected")
	}
	for name, change := range map[string]func(MemoryDigest, []MemorySource) (MemoryDigest, []MemorySource){
		"missing id": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			d.Sources = d.Sources[:1]
			return d, items
		},
		"extra id": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			d.Sources = append(d.Sources, "source:3")
			return d, items
		},
		"wrong order": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			d.Sources[0], d.Sources[1] = d.Sources[1], d.Sources[0]
			return d, items
		},
		"sequence gap": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			items[1].Seq = 3
			return d, items
		},
		"foreign scope": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			items[1].Scope = "npc:b"
			return d, items
		},
		"duplicate id": func(d MemoryDigest, items []MemorySource) (MemoryDigest, []MemorySource) {
			items[1].ID = items[0].ID
			d.Sources[1] = items[0].ID
			return d, items
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := valid
			d.Sources = append([]string{}, valid.Sources...)
			copyItems := append([]MemorySource{}, items...)
			d, copyItems = change(d, copyItems)
			if DigestCoverageMatches(d, copyItems) {
				t.Fatal("invalid coverage was accepted")
			}
		})
	}
}

func TestSearchMemoryAppliesQueryAndScanBudgets(t *testing.T) {
	t.Run("query runes", func(t *testing.T) {
		query := strings.Repeat("界", memorySearchQueryChars) + " 铜钥匙"
		if got := SearchMemory([]MemorySource{{ID: "late", Seq: 1, Content: "铜钥匙"}}, query, 1); len(got) != 0 {
			t.Fatalf("text after query rune limit matched: %+v", got)
		}
	})

	t.Run("query terms", func(t *testing.T) {
		terms := make([]string, memorySearchQueryTerms+1)
		for i := range terms {
			terms[i] = fmt.Sprintf("term%02d", i)
		}
		if got := SearchMemory([]MemorySource{{ID: "late", Seq: 1, Content: terms[len(terms)-1]}}, strings.Join(terms, " "), 1); len(got) != 0 {
			t.Fatalf("term after query term limit matched: %+v", got)
		}
	})

	t.Run("newest scan candidates", func(t *testing.T) {
		items := make([]MemorySource, memorySearchScanCandidates+1)
		for i := range items {
			items[i] = MemorySource{ID: fmt.Sprintf("source:%03d", i), Seq: int64(i + 1), Content: "copper key"}
		}
		got := SearchMemory(items, "copper", len(items))
		if len(got) != memorySearchScanCandidates || got[0].Seq != int64(len(items)) || got[len(got)-1].Seq != 2 {
			t.Fatalf("bounded newest candidates = %d [%d..%d]", len(got), got[0].Seq, got[len(got)-1].Seq)
		}
	})

	t.Run("source bytes", func(t *testing.T) {
		items := []MemorySource{
			{ID: "older", Seq: 1, Content: "copper key"},
			{ID: "oversized", Seq: 2, Content: strings.Repeat("x", memorySearchBytes+1)},
		}
		if got := SearchMemory(items, "copper", 1); len(got) != 0 {
			t.Fatalf("search crossed source byte budget: %+v", got)
		}
	})
}
