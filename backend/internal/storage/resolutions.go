package storage

import (
	"context"
	"database/sql"
	"fmt"
)

type ActionResolutionRecord struct {
	InputID, RuleID string
	Roll, Target    int
	ModifiersJSON   string
	SettledEventID  string
	Status          string
	CreatedAt       string
}

type SettledActionResult struct {
	RuleID, Status, EventID string
}

// PrepareActionResolution creates or reads the durable random choice for one
// stable player input and authored rule. It is diagnostic preparation, not a
// committed world consequence.
func (s *WorldStore) PrepareActionResolution(ctx context.Context, proposed ActionResolutionRecord) (ActionResolutionRecord, bool, error) {
	var result ActionResolutionRecord
	reused := false
	err := s.InTx(ctx, func(tx *WorldTx) error {
		row := tx.tx.QueryRowContext(ctx, `SELECT input_id,rule_id,roll,target,modifiers_json,settled_event_id,status,created_at FROM action_resolutions WHERE input_id=? AND rule_id=?`, proposed.InputID, proposed.RuleID)
		err := row.Scan(&result.InputID, &result.RuleID, &result.Roll, &result.Target, &result.ModifiersJSON, &result.SettledEventID, &result.Status, &result.CreatedAt)
		if err == nil {
			reused = true
			if result.Target != proposed.Target || result.ModifiersJSON != proposed.ModifiersJSON {
				return fmt.Errorf("prepared action resolution no longer matches its frozen inputs")
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `INSERT INTO action_resolutions(input_id,rule_id,roll,target,modifiers_json,created_at) VALUES(?,?,?,?,?,?)`, proposed.InputID, proposed.RuleID, proposed.Roll, proposed.Target, proposed.ModifiersJSON, proposed.CreatedAt)
		if err != nil {
			return err
		}
		result = proposed
		return nil
	})
	return result, reused, err
}

func (t *WorldTx) SettleActionResolution(ctx context.Context, inputID, ruleID, status, eventID string) error {
	if status != "succeeded" && status != "failed" {
		return fmt.Errorf("action resolution has invalid status")
	}
	result, err := t.tx.ExecContext(ctx, `UPDATE action_resolutions SET settled_event_id=?,status=? WHERE input_id=? AND rule_id=? AND (settled_event_id='' OR (settled_event_id=? AND status=?))`, eventID, status, inputID, ruleID, eventID, status)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("action resolution is missing or already settled by another result")
	}
	return nil
}

func (s *WorldStore) ReadActionResolution(ctx context.Context, inputID, ruleID string) (ActionResolutionRecord, bool, error) {
	var result ActionResolutionRecord
	err := s.db.QueryRowContext(ctx, `SELECT input_id,rule_id,roll,target,modifiers_json,settled_event_id,status,created_at FROM action_resolutions WHERE input_id=? AND rule_id=?`, inputID, ruleID).Scan(&result.InputID, &result.RuleID, &result.Roll, &result.Target, &result.ModifiersJSON, &result.SettledEventID, &result.Status, &result.CreatedAt)
	if err == sql.ErrNoRows {
		return ActionResolutionRecord{}, false, nil
	}
	return result, err == nil, err
}

func (s *WorldStore) ReadActionResolutionForInput(ctx context.Context, inputID string) (ActionResolutionRecord, bool, error) {
	var result ActionResolutionRecord
	err := s.db.QueryRowContext(ctx, `SELECT input_id,rule_id,roll,target,modifiers_json,settled_event_id,status,created_at FROM action_resolutions WHERE input_id=?`, inputID).Scan(&result.InputID, &result.RuleID, &result.Roll, &result.Target, &result.ModifiersJSON, &result.SettledEventID, &result.Status, &result.CreatedAt)
	if err == sql.ErrNoRows {
		return ActionResolutionRecord{}, false, nil
	}
	return result, err == nil, err
}

// LoadSettledActionResults returns one durable witness for every committed
// rule/status pair. Prepared resolutions without a committed event are excluded.
func (s *WorldStore) LoadSettledActionResults(ctx context.Context) ([]SettledActionResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rule_id,status,MIN(settled_event_id) FROM action_resolutions WHERE settled_event_id != '' AND status IN ('succeeded','failed') GROUP BY rule_id,status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SettledActionResult
	for rows.Next() {
		var item SettledActionResult
		if err := rows.Scan(&item.RuleID, &item.Status, &item.EventID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
