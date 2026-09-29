package storyapp

import (
	"context"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
)

func loadCoordinationEvidence(ctx context.Context, store *storage.WorldStore, snapshot *turn.Snapshot, input string) error {
	events, err := store.LoadEvents(ctx, int(snapshot.Summary.EventHead))
	if err != nil {
		return err
	}
	effective := turn.Snapshot{Events: events}
	if err = applySnapshotCorrections(ctx, store, &effective); err != nil {
		return err
	}
	events = effective.Events
	runs := []string{}
	seen := map[string]bool{}
	records := []memorymodel.MemorySource{}
	for _, e := range events {
		if !seen[e.RunID] {
			seen[e.RunID] = true
			runs = append(runs, e.RunID)
		}
		records = append(records, memorymodel.MemorySource{ID: e.EventID, Seq: e.Seq, RunID: e.RunID, Content: e.Content})
	}
	selected := map[string]bool{}
	for i := max(0, len(runs)-4); i < len(runs); i++ {
		selected[runs[i]] = true
	}
	queries := []string{input}
	for _, e := range snapshot.GeneratedEvents.Active {
		queries = append(queries, e.Node.Condition)
		for _, source := range events {
			if source.EventID == e.StartID || source.EventID == e.TriggerID {
				selected[source.RunID] = true
			}
		}
	}
	if snapshot.Plot != nil {
		for _, node := range snapshot.Plot.Nodes {
			queries = append(queries, node.Condition)
		}
	}
	for _, query := range queries {
		for _, s := range memorymodel.SearchMemory(records, query, 5) {
			selected[s.RunID] = true
		}
	}
	snapshot.Events = nil
	for _, e := range events {
		if selected[e.RunID] {
			snapshot.Events = append(snapshot.Events, e)
		}
	}
	return nil
}
