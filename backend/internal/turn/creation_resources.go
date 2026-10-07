package turn

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	wiaworld "gameagent/backend/internal/world"
)

type CreationStateChange struct {
	EntityID string               `json:"entity_id"`
	StateID  string               `json:"state_id"`
	Delta    *int                 `json:"delta,omitempty"`
	Value    *wiaworld.StateValue `json:"value,omitempty"`
	Reason   string               `json:"reason"`
}

type CreationItemMove struct {
	InstanceID string `json:"instance_id"`
	HolderID   string `json:"holder_id,omitempty"`
	LocationID string `json:"location_id,omitempty"`
	Reason     string `json:"reason"`
}

func initializeCreationResources(snapshot *Snapshot) {
	snapshot.States = map[string]map[string]wiaworld.EntityState{}
	snapshot.Items = map[string]wiaworld.ItemInstance{}
	snapshot.PerceivedSources = map[string]map[string]bool{}
	if snapshot.Definition.Capabilities["state"] == 1 {
		for owner, values := range snapshot.Definition.InitialStates {
			snapshot.States[owner] = map[string]wiaworld.EntityState{}
			for id, value := range values {
				snapshot.States[owner][id] = wiaworld.EntityState{EntityID: owner, StateID: id, Value: value, SourceEvent: "opening", Version: 1}
			}
		}
	}
	if snapshot.Definition.Capabilities["items"] == 1 {
		for _, item := range snapshot.Definition.InitialItems {
			snapshot.Items[item.InstanceID] = wiaworld.ItemInstance{InstanceID: item.InstanceID, DefinitionID: item.DefinitionID, HolderID: item.HolderID, LocationID: item.LocationID, SourceEvent: "opening", Version: 1}
		}
	}
}

// Changes use the accepted scene as their source. Numeric limits, policies and
// unique placement are enforced by the same modules as persistent turns.
func applyCreationResources(snapshot Snapshot, selected []string, positions map[string]string, changes *CreationChanges, runID string) (Snapshot, error) {
	bad := func(field, expected string) (Snapshot, error) {
		return Snapshot{}, coordinationInvalid("creation_resources_invalid", field, expected)
	}
	if len(changes.StateChanges) > 16 || len(changes.ItemMoves) > 16 {
		return bad("scene_changes", "at-most-16-state-changes-and-item-moves")
	}
	effects := mechanicEffects{sources: map[string]mechanicSource{}}
	seen := map[string]bool{}
	validReason := func(reason string) bool {
		return strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= 400
	}
	for i, change := range changes.StateChanges {
		key := change.EntityID + "\x00" + change.StateID
		definition, ok := stateDefinition(snapshot.Definition, change.StateID)
		if !ok || !slices.Contains(selected, change.EntityID) || seen[key] || !validReason(change.Reason) || (change.Delta == nil) == (change.Value == nil) || (definition.Currency != nil && change.Value != nil) {
			return bad("scene_changes.state_changes", "unique-selected-declared-state;actual-reason;delta-or-value;currency-delta")
		}
		seen[key] = true
		id := fmt.Sprintf("%s:state:%d", runID, i)
		effect := stateEffect{EntityID: change.EntityID, StateID: change.StateID, Value: change.Value, ActionID: id}
		if change.Delta != nil {
			effect.Delta = *change.Delta
		}
		effects.StateEffects = append(effects.StateEffects, effect)
		effects.sources[id] = mechanicSource{status: "succeeded", resultID: id}
	}
	for i, move := range changes.ItemMoves {
		current, ok := snapshot.Items[move.InstanceID]
		if !ok || seen[move.InstanceID] || !validReason(move.Reason) || (move.HolderID == "") == (move.LocationID == "") || (move.HolderID != "" && !slices.Contains(selected, move.HolderID)) || (current.HolderID != "" && !slices.Contains(selected, current.HolderID)) {
			return bad("scene_changes.item_moves", "unique-known-instance;selected-holders;one-destination;actual-reason")
		}
		definition, defined := itemDefinition(snapshot.Definition, current.DefinitionID)
		if !defined {
			return bad("scene_changes.item_moves", "declared-item-definition")
		}
		visible := false
		for _, owner := range selected {
			view := snapshot
			view.Positions = positions
			visible = visible || itemVisibleTo(snapshot, owner, current, definition) || itemVisibleTo(view, owner, current, definition)
		}
		if !visible || !creationItemContact(snapshot.Positions, positions, current, move) {
			return bad("scene_changes.item_moves", "perceivable-item-and-shared-start-or-ending-place")
		}
		seen[move.InstanceID] = true
		id := fmt.Sprintf("%s:item:%d", runID, i)
		effects.ItemTransfers = append(effects.ItemTransfers, itemTransferEffect{InstanceID: move.InstanceID, FromHolderID: current.HolderID, FromLocationID: current.LocationID, ToHolderID: move.HolderID, ToLocationID: move.LocationID, ActionID: id})
		effects.sources[id] = mechanicSource{status: "succeeded", resultID: id}
	}
	working := Output{States: snapshot.States, Items: snapshot.Items}
	if err := applyMechanicEffects(snapshot, &working, effects); err != nil {
		return Snapshot{}, err
	}
	next := snapshot
	next.States, next.Items = working.States, working.Items
	next.PerceivedSources = map[string]map[string]bool{}
	for owner, grants := range snapshot.PerceivedSources {
		next.PerceivedSources[owner] = map[string]bool{}
		for id, allowed := range grants {
			next.PerceivedSources[owner][id] = allowed
		}
	}
	grant := func(owner, source string) {
		if owner == "" {
			return
		}
		if next.PerceivedSources[owner] == nil {
			next.PerceivedSources[owner] = map[string]bool{}
		}
		next.PerceivedSources[owner][source] = true
	}
	for _, change := range working.StateChanges {
		grant(change.After.EntityID, change.SourceEventID)
		definition, _ := stateDefinition(snapshot.Definition, change.After.StateID)
		if definition.Projection == "public" && positions[change.After.EntityID] == positions["player"] {
			grant("player", change.SourceEventID)
		}
	}
	for _, transfer := range working.ItemTransfers {
		grant(transfer.After.HolderID, transfer.SourceEventID)
		definition, _ := itemDefinition(snapshot.Definition, transfer.After.DefinitionID)
		if definition.Projection == "public" && creationItemContact(snapshot.Positions, positions, transfer.Before, CreationItemMove{HolderID: "player"}) {
			grant("player", transfer.SourceEventID)
		}
	}
	return next, nil
}

func creationItemContact(before, after map[string]string, current wiaworld.ItemInstance, move CreationItemMove) bool {
	places := func(holder, location string) []string {
		if location != "" {
			return []string{location}
		}
		return []string{before[holder], after[holder]}
	}
	for _, origin := range places(current.HolderID, current.LocationID) {
		if origin != "" && slices.Contains(places(move.HolderID, move.LocationID), origin) {
			return true
		}
	}
	return false
}

func creationResourceContext(snapshot Snapshot, selected []string) string {
	view := snapshot
	view.States = map[string]map[string]wiaworld.EntityState{}
	view.Items = map[string]wiaworld.ItemInstance{}
	view.Relationships = nil
	view.Definition.RelationDefinitions = nil
	view.Definition.StateDefinitions = nil
	view.Definition.ItemDefinitions = nil
	stateIDs, itemIDs := map[string]bool{}, map[string]bool{}
	for _, owner := range selected {
		view.States[owner] = snapshot.States[owner]
	}
	for _, values := range view.States {
		for id := range values {
			stateIDs[id] = true
		}
	}
	for id, item := range snapshot.Items {
		definition, _ := itemDefinition(snapshot.Definition, item.DefinitionID)
		for _, owner := range selected {
			if itemVisibleTo(snapshot, owner, item, definition) {
				view.Items[id] = item
				itemIDs[item.DefinitionID] = true
				break
			}
		}
	}
	for _, definition := range snapshot.Definition.StateDefinitions {
		if stateIDs[definition.ID] {
			view.Definition.StateDefinitions = append(view.Definition.StateDefinitions, definition)
		}
	}
	for _, definition := range snapshot.Definition.ItemDefinitions {
		if itemIDs[definition.ID] {
			view.Definition.ItemDefinitions = append(view.Definition.ItemDefinitions, definition)
		}
	}
	return HostMechanicsContext(view)
}
