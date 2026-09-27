package storyapp

import "context"

// Coordinator history consists of world events, never player literary memory.
// Recent runs stay whole; old evidence is selected by the current input and the
// frozen plot conditions before Composer applies its group budget.
func loadCoordinationEvidence(ctx context.Context, store *worldStore, snapshot *worldSnapshot, input string) error {
	events, err := loadEvents(ctx, store.db, int(snapshot.Summary.EventHead))
	if err != nil {
		return err
	}
	effective := worldSnapshot{Events: events}
	if err = applySnapshotCorrections(ctx, store, &effective); err != nil {
		return err
	}
	events = effective.Events
	runs := []string{}
	seen := map[string]bool{}
	records := []MemorySource{}
	for _, e := range events {
		if !seen[e.RunID] {
			seen[e.RunID] = true
			runs = append(runs, e.RunID)
		}
		records = append(records, MemorySource{ID: e.EventID, Seq: e.Seq, RunID: e.RunID, Content: e.Content})
	}
	selected := map[string]bool{}
	for i := max(0, len(runs)-4); i < len(runs); i++ {
		selected[runs[i]] = true
	}
	queries := []string{input}
	if snapshot.Plot != nil {
		for _, node := range snapshot.Plot.Nodes {
			queries = append(queries, node.Condition)
		}
	}
	for _, query := range queries {
		for _, s := range searchMemory(records, query, 5) {
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
