// Package storage owns the persistence of a story world: the schema, the queries,
// the transactions and the run records.
//
// It answers questions about persisted facts and nothing else. Decisions built on
// those facts stay with the caller: CountActiveRuns reports how many runs are
// active, while "an active run means the world is busy" is a turn rule. The same
// split applies to memory readiness, which happens to read this database but means
// "may a new turn start".
//
// # State of the migration
//
// The boundary is owned here, but the encapsulation is not finished. Most reads are
// methods on WorldStore, so a caller never touches the handle; the writes that
// belong to a transaction still run inside the caller's own transaction with
// MetaGetTx, MetaSetTx and friends, because which statements must be atomic is the
// caller's business rule.
//
// A temporary escape hatch remains: WorldStore.Database(). Some call sites still
// take a raw *sql.DB through their own signatures — the memory job, correction,
// suggestion and promotion paths, which move to their own packages later — and this
// is what keeps them compiling until then. It is counted by a guard test that fails
// if the number goes up:
//
//	never the way to write a new query
//	no new file may call it
//	removed once those paths have owners
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// OpenWorldDB opens (creating if needed) the database of one world and applies its
// schema. The caller closes it.
func OpenWorldDB(path string) (*WorldStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create world database directory: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA synchronous = FULL;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, err
	}
	for _, schema := range []string{worldSchema, memorySchema, correctionSchema} {
		if _, err := db.Exec(schema); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := ensureWorldSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureEventDependencies(db); err != nil {
		db.Close()
		return nil, err
	}
	return &WorldStore{path: path, db: db}, nil
}

// Path is the file this store was opened from.
func (s *WorldStore) Path() string { return s.path }

// Close releases the database handle.
func (s *WorldStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// OpenAppDB opens the application database and applies the schema its caller owns.
// The caller closes the returned handle: the application schema is assembled from
// several features, so this package cannot know it on its own.
func OpenAppDB(path, schema string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create app database directory: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA synchronous = FULL;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Columns added after a release are applied to existing databases here, so an
	// author's workspace keeps working without a migration step.
	for _, column := range []struct{ table, name, definition string }{
		{"content_drafts", "confirmation_key", "TEXT NOT NULL DEFAULT ''"},
		{"content_operations", "plan_json", "TEXT NOT NULL DEFAULT ''"},
		{"pack_revisions", "digest_version", "INTEGER NOT NULL DEFAULT 1"},
	} {
		if err := ensureColumn(db, column.table, column.name, column.definition); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

// sqliteDSN builds the connection string, including the pragmas that must be set
// before the first statement runs.
func sqliteDSN(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p, RawQuery: url.Values{
		"_busy_timeout": []string{"5000"},
		"_foreign_keys": []string{"on"},
		"_journal_mode": []string{"wal"},
		"_synchronous":  []string{"full"},
		"_txlock":       []string{"immediate"},
	}.Encode()}).String()
}

// ensureColumn adds a column when an older database does not have it yet.
func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	exists := false
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var fallback any
		if err = rows.Scan(&cid, &name, &kind, &notNull, &fallback, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			exists = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

// ensureWorldSchema brings a world database written by an earlier release up to the
// columns and indexes this one expects.
func ensureWorldSchema(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	additions := []struct{ name, definition string }{
		{"input_id", `TEXT NOT NULL DEFAULT ''`}, {"input_seq", `INTEGER NOT NULL DEFAULT 0`},
		{"base_turn_seq", `INTEGER NOT NULL DEFAULT 0`}, {"base_message_head", `INTEGER NOT NULL DEFAULT 0`},
		{"base_event_head", `INTEGER NOT NULL DEFAULT 0`}, {"base_context_epoch", `INTEGER NOT NULL DEFAULT 0`},
		{"base_scene_version", `INTEGER NOT NULL DEFAULT 0`},
	}
	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN ` + addition.name + ` ` + addition.definition); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO meta(key,value) SELECT 'input_seq', CAST(COALESCE(MAX(input_seq),0) AS TEXT) FROM runs`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_input_seq ON runs(input_seq); CREATE INDEX IF NOT EXISTS idx_runs_input_id ON runs(input_id);`); err != nil {
		return err
	}
	return nil
}

// TestingDB exposes the handle to test fixtures that seed and assert raw rows.
//
// Production code must not call it: every query business code needs is a function
// in this package. It exists because the migration is staged — the call sites that
// still run their own SQL are being replaced one at a time, and this is the single
// place that keeps them compiling until that is done. It is removed with R4.
func (s *WorldStore) TestingDB() *sql.DB { return s.db }

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
	if err = MetaSetTx(context.Background(), tx, "projection_dependencies_v1", "1"); err != nil {
		return err
	}
	return tx.Commit()
}

// Database is a temporary escape hatch for the staged migration.
//
// Turn assembly (the world snapshot) and the two transaction writers that depend on
// it — world initialization and turn commit — still run their own SQL, and they
// cannot be converted until the turn package exists with its own types. This
// returns the handle so those call sites keep compiling while everything else moves
// to the typed functions in this package. Test fixtures that seed raw rows use it
// for the same reason.
//
// Rules: never the way to write a new query, no new module may call it, removed
// with R4 once those functions are converted.
func (s *WorldStore) Database() *sql.DB { return s.db }
