package storyapp

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRecallBudgetPreservesRequiredAndCausalGroups(t *testing.T) {
	archive := []MemorySource{{ID: "attempt", Seq: 1, RunID: "old", Content: "铜钥匙" + strings.Repeat("长篇旧经历", 15000)}, {ID: "result", Seq: 2, RunID: "old", Content: "尝试失败"}}
	m := withRecall(contextMaterial{System: "NPC", Required: "本轮刺激与完整近期经历", RequiredSources: []string{"current"}}, memoryContext{Archive: archive}, "铜钥匙")
	req, report, err := (ContextComposer{}).Build(m, m.System, 100)
	if err != nil {
		t.Fatalf("optional recall blocked current turn: %v", err)
	}
	if !strings.Contains(req.Input, "本轮刺激与完整近期经历") || strings.Contains(req.Input, "长篇旧经历") || strings.Contains(req.Input, "尝试失败") {
		t.Fatal("required or causal group boundary violated")
	}
	if report.ExcludedSources != 2 || !strings.Contains(req.Input, "预算") {
		t.Fatal("recall exclusion not reported", report)
	}
}

func TestDigestPromptDoesNotGrowWithCoverageIDs(t *testing.T) {
	previous := MemoryDigest{Scope: "npc:a", Revision: 10, Epoch: 1, Content: "既有回顾", States: []SubjectiveState{{Kind: "commitment", Content: "尚未归还铜钥匙", Sources: []string{"old:0"}}}}
	for i := 0; i < 10000; i++ {
		previous.Sources = append(previous.Sources, fmt.Sprintf("old:%05d:long-source-identity", i))
	}
	previous.Sources = append(previous.Sources, "old:0")
	g := &digestGenerator{}
	for i := 0; i < 2; i++ {
		prefix := []MemorySource{{ID: fmt.Sprint("new:", i), Seq: int64(10001 + i), Content: "新进展"}}
		d, err := (&App{}).summarizeMemory(context.Background(), g, worldSnapshot{}, Run{BaseContextEpoch: 1}, "npc:a", previous, prefix)
		if err != nil {
			t.Fatalf("coverage IDs exhausted prompt: %v", err)
		}
		if len(d.Sources) != len(previous.Sources)+1 {
			t.Fatal("coverage metadata lost")
		}
		r := g.requests[len(g.requests)-1]
		if strings.Contains(r.Input, "old:09999:long-source-identity") || !strings.Contains(r.Input, prefix[0].ID) {
			t.Fatal("wrong source catalog")
		}
		previous = d
	}
	if len(g.requests) != 2 {
		t.Fatal("not incremental")
	}
}

func TestDigestStateSourcesUseCurrentCatalog(t *testing.T) {
	previous := MemoryDigest{Sources: []string{"covered", "retained"}, States: []SubjectiveState{{Kind: "belief", Content: "已保留判断", Sources: []string{"retained"}}}}
	for _, tc := range []struct {
		id    string
		valid bool
	}{{"covered", false}, {"retained", true}, {"new", true}} {
		g := fixedJSONGenerator{text: fmt.Sprintf(`{"content":"有效回顾","states":[{"kind":"belief","content":"判断","source_ids":[%q]}]}`, tc.id)}
		_, err := (&App{}).summarizeMemory(context.Background(), g, worldSnapshot{}, Run{}, "npc:a", previous, []MemorySource{{ID: "new", Seq: 10, Content: "新经历"}})
		if (err == nil) != tc.valid {
			t.Fatalf("source=%s err=%v", tc.id, err)
		}
	}
}
