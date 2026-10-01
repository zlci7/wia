package turn

import (
	"context"
	"path/filepath"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func TestDerivedFactEvidenceDoesNotRequireAnEventRowOnReload(t *testing.T) {
	store, err := storage.OpenWorldDB(filepath.Join(t.TempDir(), "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := Snapshot{PlotProgress: plot.Progress{Nodes: map[string]plot.NodeState{
		"gate": {Status: "skipped", EventID: "", Evidence: []string{"fact:state_at_most:player::strain:3::"}},
	}}}
	metadata, err := loadSourceMetadata(context.Background(), store.Database(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 0 {
		t.Fatalf("derived facts became event metadata: %+v", metadata)
	}
}

type fixedPlotGenerator struct{ result plotResolution }

func (g fixedPlotGenerator) GenerateText(context.Context, model.TextRequest) (model.TextResponse, error) {
	return model.TextResponse{Text: wire.MarshalJSON(g.result)}, nil
}

func gatedPlotSnapshot(node plot.Node, events []wiaworld.Event) Snapshot {
	definition := plot.Definition{Revision: "gate.v1", Facts: "测试", Nodes: []plot.Node{node}}
	return Snapshot{Summary: wiaworld.WorldSummary{WorldID: "world", Mode: "open", Clock: "第 1 日 09:00"}, Plot: &definition, PlotProgress: plot.Progress{Version: 1, Nodes: map[string]plot.NodeState{}}, Events: events, Positions: map[string]string{"player": "office"}, States: map[string]map[string]wiaworld.EntityState{}, Items: map[string]wiaworld.ItemInstance{}, Relationships: []wiaworld.Relationship{}, Sources: map[string]SourceMetadata{}}
}

func TestStructuredPlotGateDistinguishesParticipationRefusalAndWaiting(t *testing.T) {
	node := plot.Node{ID: "window", Condition: "委托窗口", Development: "依据选择结算", Audience: []string{}, Requirements: []plot.FactCondition{{Kind: "rule_result", FactID: "accept", Status: "succeeded"}}, OnUnmet: "skip"}
	for _, tc := range []struct {
		name   string
		events []wiaworld.Event
		want   string
	}{
		{"participated", []wiaworld.Event{{EventID: "accepted", SourceType: "rule:accept:succeeded"}}, "occurred"},
		{"refused", []wiaworld.Event{{EventID: "refused", SourceType: "rule:refuse:succeeded"}}, "skipped"},
		{"waited", nil, "skipped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := gatedPlotSnapshot(node, tc.events)
			output := Output{Clock: snapshot.Summary.Clock, Positions: snapshot.Positions, States: snapshot.States, Items: snapshot.Items, Relationships: snapshot.Relationships}
			_, evidence := evaluateFactConditions(snapshot, output, node.Requirements, "")
			result := plotResolution{Status: tc.want, Content: "程序条件下的结算", SourceIDs: evidence, Projections: []plotProjection{}, DecisionRequests: []string{}, Ending: ""}
			_, err := New(&rosterHost{}, Deps{}).advancePlot(context.Background(), fixedPlotGenerator{result}, snapshot, wiaworld.Run{RunID: "run"}, &output)
			if err != nil {
				t.Fatal(err)
			}
			if output.PlotProgress.Nodes[node.ID].Status != tc.want {
				t.Fatalf("progress=%+v", output.PlotProgress)
			}
		})
	}
}

func TestStructuredPlotGateSkipsDestroyedItemPrecondition(t *testing.T) {
	node := plot.Node{ID: "recovery", Condition: "回收物品", Development: "物品仍在才执行", Audience: []string{}, Requirements: []plot.FactCondition{{Kind: "item_at", FactID: "mirror", LocationID: "clinic"}}, OnUnmet: "skip"}
	snapshot := gatedPlotSnapshot(node, nil)
	snapshot.Items = map[string]wiaworld.ItemInstance{"mirror": {InstanceID: "mirror", HolderID: "player"}}
	output := Output{Clock: snapshot.Summary.Clock, Positions: snapshot.Positions, States: snapshot.States, Items: snapshot.Items, Relationships: snapshot.Relationships}
	_, evidence := evaluateFactConditions(snapshot, output, node.Requirements, "")
	wrong := plotResolution{Status: "occurred", Content: "错误执行", SourceIDs: evidence, Projections: []plotProjection{}, DecisionRequests: []string{}, Ending: ""}
	if _, err := New(&rosterHost{}, Deps{}).advancePlot(context.Background(), fixedPlotGenerator{wrong}, snapshot, wiaworld.Run{RunID: "run"}, &output); err == nil {
		t.Fatal("destroyed precondition was allowed to occur")
	}
	output = Output{Clock: snapshot.Summary.Clock, Positions: snapshot.Positions, States: snapshot.States, Items: snapshot.Items, Relationships: snapshot.Relationships}
	correct := wrong
	correct.Status = "skipped"
	correct.Content = "回收计划因物品已经被取走而落空"
	if _, err := New(&rosterHost{}, Deps{}).advancePlot(context.Background(), fixedPlotGenerator{correct}, snapshot, wiaworld.Run{RunID: "run"}, &output); err != nil {
		t.Fatal(err)
	}
}
