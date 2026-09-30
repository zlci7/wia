package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
)

func readGeneratedEvents(ctx context.Context, store *storage.WorldStore, def story.Definition) (turn.GeneratedEventState, error) {
	s := turn.GeneratedEventState{Active: []turn.GeneratedEvent{}}
	raw, err := store.MetaGet(ctx, "generated_events")
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if json.Unmarshal([]byte(raw), &s) != nil || def.EventGeneration == nil || s.LastOfferTurn < 0 || s.Completed < 0 || len(s.Active) > def.EventGeneration.MaxActive {
		return s, ErrStorageUnavailable
	}
	seen := map[string]bool{}
	for _, e := range s.Active {
		if seen[e.Node.ID] || e.StartID == "" || e.TriggerID == "" || e.Premise == "" || !slices.Contains(def.EventGeneration.Locations, e.Location) || e.Node.Terminal || len(e.Node.After) != 0 {
			return s, ErrStorageUnavailable
		}
		seen[e.Node.ID] = true
		if err := plot.ValidateDefinition(plot.Definition{Revision: "generated.v1", Nodes: []plot.Node{e.Node}}); err != nil {
			return s, err
		}
		if e.State.Status != "" && e.State.Status != "deferred" {
			return s, ErrStorageUnavailable
		}
		for _, id := range e.Node.Audience {
			if id != "player" && !slices.Contains(def.EventGeneration.Participants, id) {
				return s, ErrStorageUnavailable
			}
		}
		for _, id := range []string{e.StartID, e.TriggerID} {
			exists, err := store.EventExists(ctx, id)
			if err != nil {
				return s, err
			}
			if !exists {
				return s, turn.ErrContextSourceMissing
			}
		}
	}
	return s, nil
}

// An authored evaluation consumes the round even when it defers without an event.

// The author plan is archived with the same transaction, never projected as facts.
