package storyapp

import (
	"context"
	"database/sql"
	"fmt"

	"gameagent/backend/internal/memorymodel"
	_ "gameagent/backend/internal/storage"
)

// Dependencies describe projections, not permissions to read the parent.

// The legacy writer generated numeric projection suffixes in the same run
// and stage as a plot_result. Validate that exact contract once on upgrade.

func expandCorrection(ctx context.Context, db *sql.DB, c *memorymodel.Correction) error {
	if c.Kind != "event" {
		return nil
	}
	rows, err := db.QueryContext(ctx, `WITH RECURSIVE affected(id) AS (
 SELECT child_id FROM event_dependencies WHERE parent_id=?
 UNION SELECT d.child_id FROM event_dependencies d JOIN affected a ON d.parent_id=a.id)
 SELECT id FROM affected`, c.TargetID)
	if err != nil {
		return fmt.Errorf("load correction dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		c.Dependents = append(c.Dependents, id)
	}
	return rows.Err()
}
