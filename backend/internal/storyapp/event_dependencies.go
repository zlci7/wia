package storyapp

import (
	"context"
	"database/sql"
	"fmt"
	wiaworld "gameagent/backend/internal/world"
	"strconv"
	"strings"
)

// Dependencies describe projections, not permissions to read the parent.
func ensureEventDependencies(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS event_dependencies (
 child_id TEXT PRIMARY KEY REFERENCES events(event_id), parent_id TEXT NOT NULL REFERENCES events(event_id));
 CREATE INDEX IF NOT EXISTS event_dependencies_parent ON event_dependencies(parent_id);`); err != nil {
		return err
	}
	var migrated int
	if err := db.QueryRow(`SELECT COUNT(*) FROM meta WHERE key='projection_dependencies_v1'`).Scan(&migrated); err != nil {
		return err
	}
	if migrated > 0 {
		return nil
	}
	// The legacy writer generated numeric projection suffixes in the same run
	// and stage as a plot_result. Validate that exact contract once on upgrade.
	rows, err := db.Query(`SELECT p.event_id,r.event_id FROM events p JOIN events r
 ON p.run_id=r.run_id AND p.stage=r.stage AND p.scene_version=r.scene_version
 WHERE p.event_type='plot_perceived' AND p.source_type='plot_observed' AND r.event_type='plot_result'
 AND p.event_id LIKE r.event_id || ':projection:%'`)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for rows.Next() {
		var child, parent string
		if err = rows.Scan(&child, &parent); err != nil {
			rows.Close()
			return err
		}
		suffix := strings.TrimPrefix(child, parent+":projection:")
		n, e := strconv.Atoi(suffix)
		if e == nil && n >= 0 && strconv.Itoa(n) == suffix {
			pairs = append(pairs, [2]string{child, parent})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, pair := range pairs {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO event_dependencies(child_id,parent_id) VALUES(?,?)`, pair[0], pair[1]); err != nil {
			return err
		}
	}
	if err = metaSetTx(context.Background(), tx, "projection_dependencies_v1", "1"); err != nil {
		return err
	}
	return tx.Commit()
}

func expandCorrection(ctx context.Context, db *sql.DB, c *Correction) error {
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

func (c Correction) affects(eventID string) bool {
	return eventID == c.TargetID || wiaworld.ContainsID(c.Dependents, eventID)
}
