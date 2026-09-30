package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
)

// PlotDefinition is the stored compatibility shape of an authored plot.
type PlotDefinition struct {
	Revision string      `json:"revision"`
	Facts    string      `json:"facts"`
	Nodes    []plot.Node `json:"nodes"`
}

type PlotNode struct {
	ID          string   `json:"id"`
	After       []string `json:"after"`
	AtMinute    int      `json:"at_minute"`
	Condition   string   `json:"condition"`
	Development string   `json:"development"`
	Audience    []string `json:"audience"`
	Terminal    bool     `json:"terminal"`
}

type PlotNodeState struct {
	Status    string   `json:"status"`
	EventID   string   `json:"event_id,omitempty"`
	Content   string   `json:"content,omitempty"`
	NextCheck int      `json:"next_check"`
	Evidence  []string `json:"evidence"`
}

type PlotProgress struct {
	Version int64                     `json:"version"`
	Nodes   map[string]plot.NodeState `json:"nodes"`
	Ending  string                    `json:"ending,omitempty"`
}

func readPlot(ctx context.Context, store *storage.WorldStore) (*plot.Definition, plot.Progress, error) {
	var def plot.Definition
	var state plot.Progress
	raw, err := store.MetaGet(ctx, "plot_definition")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, state, nil
	}
	if err != nil {
		return nil, state, err
	}
	if err = json.Unmarshal([]byte(raw), &def); err != nil {
		return nil, state, err
	}
	raw, err = store.MetaGet(ctx, "plot_progress")
	if err != nil {
		return nil, state, err
	}
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, state, err
	}
	if err = plot.Validate(def, state); err != nil {
		return nil, state, err
	}
	return &def, state, nil
}
