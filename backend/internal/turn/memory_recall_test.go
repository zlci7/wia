package turn

import (
	"fmt"
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
