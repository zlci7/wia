package turn

import (
	"fmt"
	"strings"
	"testing"

	"gameagent/backend/internal/memorymodel"
)

func TestWithRecallFiltersSuppliedGroupsBeforeTakingFive(t *testing.T) {
	archive := make([]memorymodel.MemorySource, 0, 10)
	supplied := map[string]bool{}
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("source:%02d", i)
		archive = append(archive, memorymodel.MemorySource{ID: id, Seq: int64(i), RunID: fmt.Sprintf("run:%02d", i), Content: "copper key"})
		if i > 5 {
			supplied[id] = true
		}
	}
	material := WithRecall(Material{}, MemoryProjection{Context: MemoryContext{Archive: archive}, Supplied: supplied}, "copper")
	if len(material.Optional) != 5 || len(material.RecallSources) != 5 {
		t.Fatalf("recall groups/sources = %d/%d, want 5/5", len(material.Optional), len(material.RecallSources))
	}
	for i, id := range []string{"source:05", "source:04", "source:03", "source:02", "source:01"} {
		if material.RecallSources[i] != id {
			t.Fatalf("RecallSources[%d] = %q, want %q", i, material.RecallSources[i], id)
		}
	}
}

func TestWithRecallKeepsRetrievalLimitsVisible(t *testing.T) {
	t.Run("oversized group is excluded whole", func(t *testing.T) {
		archive := make([]memorymodel.MemorySource, 0, 513)
		for i := 0; i < 513; i++ {
			content := "ordinary memory"
			if i == 0 {
				content = strings.Repeat("x", 32<<20)
			}
			if i == 512 {
				content = "copper key"
			}
			archive = append(archive, memorymodel.MemorySource{ID: fmt.Sprintf("source:%03d", i), Seq: int64(i + 1), RunID: "run:one", Content: content})
		}

		material := WithRecall(Material{}, MemoryProjection{Context: MemoryContext{Archive: archive}}, "copper")
		if len(material.Optional) != 0 || len(material.RecallSources) != 0 {
			t.Fatalf("partial oversized group escaped: sections=%d sources=%d", len(material.Optional), len(material.RecallSources))
		}
		if !material.RecallLimited {
			t.Fatal("oversized group did not report a limited search")
		}
	})

	t.Run("candidate window is reported with no hits", func(t *testing.T) {
		archive := make([]memorymodel.MemorySource, 513)
		for i := range archive {
			archive[i] = memorymodel.MemorySource{ID: fmt.Sprintf("source:%03d", i), Seq: int64(i + 1), RunID: fmt.Sprintf("run:%03d", i), Content: "ordinary memory"}
		}
		material := WithRecall(Material{Required: "本轮事实"}, MemoryProjection{Context: MemoryContext{Archive: archive}}, "copper")
		if !material.RecallLimited {
			t.Fatal("candidate window did not report a limited search")
		}
		req, report, err := (ContextComposer{}).Build(material, "system", 64)
		if err != nil {
			t.Fatal(err)
		}
		if !report.RecallLimited || !strings.Contains(req.Input, "本次检索受近期候选窗口或检索预算限制") {
			t.Fatalf("limited retrieval was not visible: report=%+v input=%q", report, req.Input)
		}
	})
}
