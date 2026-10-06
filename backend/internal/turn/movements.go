package turn

import (
	"fmt"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func applyMovements(snapshot Snapshot, movements []movementResult, sources map[string]mechanicSource) (map[string]string, []PositionChange, []string, error) {
	positions := clonePositions(snapshot.Positions)
	graph := story.PlaceGraph(snapshot.Definition)
	changes := make([]PositionChange, 0, len(movements))
	seenEntity, seenAction := map[string]bool{}, map[string]bool{}
	for index, movement := range movements {
		field := fmt.Sprintf("movements[%d]", index)
		movement.EntityID, movement.From, movement.To, movement.ActionID = wire.Clean(movement.EntityID), wire.Clean(movement.From), wire.Clean(movement.To), wire.Clean(movement.ActionID)
		source, exists := sources[movement.ActionID]
		action := source.action
		if !exists || seenAction[movement.ActionID] {
			return nil, nil, nil, coordinationInvalid("movement_source_invalid", field+".action_id", "unique-current-action-with-outcome")
		}
		if movement.EntityID == "" || positions[movement.EntityID] == "" || seenEntity[movement.EntityID] || action.ActorID != movement.EntityID {
			return nil, nil, nil, coordinationInvalid("movement_entity_invalid", field+".entity_id", "positioned-actor-of-source-action")
		}
		if source.status != "succeeded" && source.status != "partial" {
			return nil, nil, nil, coordinationInvalid("movement_outcome_invalid", field+".action_id", "succeeded-or-partial-outcome")
		}
		if positions[movement.EntityID] != movement.From || movement.From == movement.To {
			return nil, nil, nil, coordinationInvalid("movement_origin_invalid", field+".from", "current-location-and-different-destination")
		}
		if err := wiaworld.ValidateRoute(movement.Route, movement.From, movement.To, graph); err != nil {
			return nil, nil, nil, coordinationInvalid("movement_route_invalid", field+".route", "directed-connected-place-route")
		}
		seenEntity[movement.EntityID], seenAction[movement.ActionID] = true, true
		positions[movement.EntityID] = movement.To
		changes = append(changes, PositionChange{
			EntityID: movement.EntityID, To: movement.To, ActionID: movement.ActionID,
			SourceEventID:         source.resultID,
			PreviousSourceEventID: snapshot.PositionSources[movement.EntityID],
		})
	}
	return positions, changes, spatialSceneCharacters(snapshot.Characters, positions), nil
}
