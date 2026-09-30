package turn

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestComposePlotPreservesEveryRequiredEventOrFailsCapacity(t *testing.T) {
	snapshot := Snapshot{
		Summary:      wiaworld.WorldSummary{Mode: "open"},
		Plot:         &plot.Definition{Revision: "plot:v1", Facts: "facts"},
		PlotProgress: plot.Progress{Nodes: map[string]plot.NodeState{}},
		Sources:      map[string]SourceMetadata{},
	}
	node := plot.Node{ID: "node:1"}
	output := Output{Clock: "第 1 日 08:00"}
	for i := 1; i <= 33; i++ {
		output.Events = append(output.Events, wiaworld.Event{EventID: fmt.Sprintf("event:%02d", i), EventType: "result", Content: fmt.Sprintf("result %02d", i)})
	}
	material := composePlot(snapshot, wiaworld.Run{}, node, &output)
	if !strings.Contains(material.Required, wire.MarshalJSON(output.Events)) || !strings.Contains(material.Required, "event:33") {
		t.Fatal("required plot events were truncated before composition")
	}
	request, report, err := (ContextComposer{}).Build(material, material.System, 512)
	if err != nil {
		t.Fatalf("33 small required events did not fit: %v", err)
	}
	if !report.RequiredComplete || len(report.SelectedSources) != len(output.Events) || !strings.Contains(request.Input, "event:33") {
		t.Fatalf("required event report is incomplete: complete=%t sources=%d input_has_last=%t", report.RequiredComplete, len(report.SelectedSources), strings.Contains(request.Input, "event:33"))
	}

	large := strings.Repeat("x", 1<<20)
	output.Events = []wiaworld.Event{{EventID: "event:large", EventType: "result", Content: large}}
	material = composePlot(snapshot, wiaworld.Run{}, node, &output)
	if !strings.Contains(material.Required, "event:large") || !strings.Contains(material.Required, large) {
		t.Fatal("oversized non-empty event was replaced before capacity validation")
	}
	_, report, err = (ContextComposer{}).Build(material, material.System, 512)
	if !errors.Is(err, ErrContextCapacity) || report.RequiredComplete {
		t.Fatalf("oversized required event = (%v, complete=%t), want ErrContextCapacity/incomplete", err, report.RequiredComplete)
	}
}
