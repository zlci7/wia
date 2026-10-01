package storage

import (
	"context"
	"encoding/json"
	"strings"

	wiaworld "gameagent/backend/internal/world"
)

func (s *WorldStore) LoadEntityStates(ctx context.Context) (map[string]map[string]wiaworld.EntityState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT entity_id,state_id,value_json,source_event_id,updated_turn,version FROM entity_states ORDER BY entity_id,state_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]wiaworld.EntityState{}
	for rows.Next() {
		var item wiaworld.EntityState
		var encoded string
		if err := rows.Scan(&item.EntityID, &item.StateID, &encoded, &item.SourceEvent, &item.UpdatedTurn, &item.Version); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &item.Value); err != nil {
			return nil, err
		}
		if result[item.EntityID] == nil {
			result[item.EntityID] = map[string]wiaworld.EntityState{}
		}
		result[item.EntityID][item.StateID] = item
	}
	return result, rows.Err()
}

func (s *WorldStore) LoadRelationships(ctx context.Context) ([]wiaworld.Relationship, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT subject_id,target_id,relation_type,value,source_event_id,updated_turn,version FROM relationships ORDER BY subject_id,target_id,relation_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []wiaworld.Relationship{}
	for rows.Next() {
		var item wiaworld.Relationship
		if err := rows.Scan(&item.SubjectID, &item.TargetID, &item.RelationType, &item.Value, &item.SourceEvent, &item.UpdatedTurn, &item.Version); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *WorldStore) LoadAppliedRelationshipSources(ctx context.Context, candidateSourceIDs []string) (map[string]bool, error) {
	result := map[string]bool{}
	unique := []string{}
	seen := map[string]bool{}
	for _, id := range candidateSourceIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	for start := 0; start < len(unique); start += 200 {
		end := min(start+200, len(unique))
		args := make([]any, 0, end-start)
		for _, id := range unique[start:end] {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT proposal_source_event_id,subject_id,target_id,relation_type FROM relationship_changes WHERE proposal_source_event_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var source, subject, target, relationType string
			if err := rows.Scan(&source, &subject, &target, &relationType); err != nil {
				rows.Close()
				return nil, err
			}
			result[source+"\x00"+subject+"\x00"+target+"\x00"+relationType] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *WorldStore) LoadItems(ctx context.Context) (map[string]wiaworld.ItemInstance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,definition_id,holder_id,location_id,source_event_id,updated_turn,version FROM item_instances ORDER BY instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]wiaworld.ItemInstance{}
	for rows.Next() {
		var item wiaworld.ItemInstance
		if err := rows.Scan(&item.InstanceID, &item.DefinitionID, &item.HolderID, &item.LocationID, &item.SourceEvent, &item.UpdatedTurn, &item.Version); err != nil {
			return nil, err
		}
		result[item.InstanceID] = item
	}
	return result, rows.Err()
}

// LoadStructuredFactSources returns the source events still supporting current
// state, relationship, item, or settled rule facts. Corrections reject these until a rebuild
// mechanism can recompute the corresponding structured facts.
func (s *WorldStore) LoadStructuredFactSources(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_event_id FROM entity_states UNION SELECT source_event_id FROM relationships UNION SELECT source_event_id FROM item_instances UNION SELECT source_event_id FROM state_changes UNION SELECT source_event_id FROM relationship_changes UNION SELECT source_event_id FROM item_transfers UNION SELECT settled_event_id FROM action_resolutions WHERE settled_event_id != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

type StateChangeWrite struct {
	ChangeID      string
	Before, After wiaworld.EntityState
	SourceEventID string
	TurnSeq       int64
	Order         int
}

func (t *WorldTx) SetEntityState(ctx context.Context, state wiaworld.EntityState) error {
	value, err := json.Marshal(state.Value)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(ctx, `INSERT INTO entity_states(entity_id,state_id,value_json,source_event_id,updated_turn,version) VALUES(?,?,?,?,?,?) ON CONFLICT(entity_id,state_id) DO UPDATE SET value_json=excluded.value_json,source_event_id=excluded.source_event_id,updated_turn=excluded.updated_turn,version=excluded.version`, state.EntityID, state.StateID, string(value), state.SourceEvent, state.UpdatedTurn, state.Version)
	return err
}

func (t *WorldTx) InsertStateChange(ctx context.Context, change StateChangeWrite) error {
	before, err := json.Marshal(change.Before.Value)
	if err != nil {
		return err
	}
	after, err := json.Marshal(change.After.Value)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(ctx, `INSERT INTO state_changes(change_id,entity_id,state_id,before_json,after_json,source_event_id,turn_seq,effect_order) VALUES(?,?,?,?,?,?,?,?)`, change.ChangeID, change.After.EntityID, change.After.StateID, string(before), string(after), change.SourceEventID, change.TurnSeq, change.Order)
	return err
}

type RelationshipChangeWrite struct {
	ChangeID              string
	Before, After         wiaworld.Relationship
	SourceEventID         string
	ProposalSourceEventID string
	TurnSeq               int64
	Order                 int
}

func (t *WorldTx) SetRelationship(ctx context.Context, relation wiaworld.Relationship) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO relationships(subject_id,target_id,relation_type,value,source_event_id,updated_turn,version) VALUES(?,?,?,?,?,?,?) ON CONFLICT(subject_id,target_id,relation_type) DO UPDATE SET value=excluded.value,source_event_id=excluded.source_event_id,updated_turn=excluded.updated_turn,version=excluded.version`, relation.SubjectID, relation.TargetID, relation.RelationType, relation.Value, relation.SourceEvent, relation.UpdatedTurn, relation.Version)
	return err
}

func (t *WorldTx) InsertRelationshipChange(ctx context.Context, change RelationshipChangeWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO relationship_changes(change_id,subject_id,target_id,relation_type,before_value,after_value,source_event_id,proposal_source_event_id,turn_seq,effect_order) VALUES(?,?,?,?,?,?,?,?,?,?)`, change.ChangeID, change.After.SubjectID, change.After.TargetID, change.After.RelationType, change.Before.Value, change.After.Value, change.SourceEventID, change.ProposalSourceEventID, change.TurnSeq, change.Order)
	return err
}

type ItemTransferWrite struct {
	TransferID    string
	Before, After wiaworld.ItemInstance
	SourceEventID string
	TurnSeq       int64
	Order         int
}

func (t *WorldTx) SetItem(ctx context.Context, item wiaworld.ItemInstance) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO item_instances(instance_id,definition_id,holder_id,location_id,source_event_id,updated_turn,version) VALUES(?,?,?,?,?,?,?) ON CONFLICT(instance_id) DO UPDATE SET definition_id=excluded.definition_id,holder_id=excluded.holder_id,location_id=excluded.location_id,source_event_id=excluded.source_event_id,updated_turn=excluded.updated_turn,version=excluded.version`, item.InstanceID, item.DefinitionID, item.HolderID, item.LocationID, item.SourceEvent, item.UpdatedTurn, item.Version)
	return err
}

func (t *WorldTx) InsertItemTransfer(ctx context.Context, change ItemTransferWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO item_transfers(transfer_id,instance_id,before_holder_id,before_location_id,after_holder_id,after_location_id,source_event_id,turn_seq,effect_order) VALUES(?,?,?,?,?,?,?,?,?)`, change.TransferID, change.After.InstanceID, change.Before.HolderID, change.Before.LocationID, change.After.HolderID, change.After.LocationID, change.SourceEventID, change.TurnSeq, change.Order)
	return err
}
