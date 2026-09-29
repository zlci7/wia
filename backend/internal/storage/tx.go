package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// WorldTx is one transaction over a world database, offering the writes this world
// needs by name.
//
// It deliberately does not expose Exec, Query or QueryRow. A transaction that hands
// out raw SQL would put the statements back in the business files while looking like
// a boundary, which is the opposite of what this package is for. The methods below say
// what is stored — a correction, a memory source, a meta value — and not why a caller
// is storing it.
//
// What must stay outside this type is the decision to combine these writes. That a
// correction, an epoch bump, a job supersession and a new job have to happen together,
// and in that order, is the correction service's rule; this type only performs each
// write and never commits on its own. The transaction belongs to InTx.
type WorldTx struct {
	tx *sql.Tx
}

// InTx runs fn inside one transaction and commits it when fn returns nil.
//
// A non-nil error from fn rolls the transaction back and is returned unchanged, so a
// caller can use its own sentinel errors for business conflicts without this package
// knowing what they mean.
func (s *WorldStore) InTx(ctx context.Context, fn func(*WorldTx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&WorldTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// GetMeta reads a header value inside the transaction, so a caller can check a
// precondition against the same snapshot it is about to write.
func (t *WorldTx) GetMeta(ctx context.Context, key string) (string, error) {
	var value string
	err := t.tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	return value, err
}

// SetMeta writes a header value inside the transaction.
func (t *WorldTx) SetMeta(ctx context.Context, key, value string) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// InsertCorrection writes one correction record.
func (t *WorldTx) InsertCorrection(ctx context.Context, record CorrectionWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO corrections VALUES(?,?,?,?,?,?,?,?,?,?)`,
		record.Epoch, record.RequestKey, record.RequestHash, record.Kind, record.Scope,
		record.TargetID, record.Original, record.Replacement, record.CreatedAt, record.SceneVersion)
	return err
}

// UpdateMemoryJob writes the given fields of one memory job.
//
// It is one update rather than MarkRunning, MarkFailed and MarkCompleted because the
// caller decides what a job's status should become; this method only records it. A
// family of Mark* methods would put the lifecycle here.
func (t *WorldTx) UpdateMemoryJob(ctx context.Context, update MemoryJobUpdate) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE memory_jobs SET status=?,completed=?,error=?,updated_at=? WHERE epoch=?`,
		update.Status, update.Completed, update.Error, update.UpdatedAt, update.Epoch)
	return err
}

// SupersedeOpenMemoryJobs marks every job that is neither completed nor superseded as
// superseded, which is what one correction does to the rebuilds it invalidated.
func (t *WorldTx) SupersedeOpenMemoryJobs(ctx context.Context) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE memory_jobs SET status='superseded' WHERE status!='completed'`)
	return err
}

// InsertMemoryJob writes one rebuild job.
func (t *WorldTx) InsertMemoryJob(ctx context.Context, record MemoryJobWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO memory_jobs VALUES(?,'queued',0,?,'',?)`,
		record.Epoch, record.Scopes, record.CreatedAt)
	return err
}

// UpdateMemoryJob writes the given fields of one memory job on its own.
//
// A single-statement update is atomic by itself, so it does not need a transaction
// around it; this is the form for a caller that is not already inside one.
func (s *WorldStore) UpdateMemoryJob(ctx context.Context, update MemoryJobUpdate) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memory_jobs SET status=?,completed=?,error=?,updated_at=? WHERE epoch=?`,
		update.Status, update.Completed, update.Error, update.UpdatedAt, update.Epoch)
	return err
}

// FailMemoryJobIfOpen records that a rebuild failed, unless it already finished.
//
// The "if open" part is a precondition rather than a business rule: a job that has
// completed must not be relabelled as failed. A caller can express its intent as one
// call instead of writing a WHERE clause, and the clause stays here where the column
// lives.
func (s *WorldStore) FailMemoryJobIfOpen(ctx context.Context, epoch int64, code, updatedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memory_jobs SET status='failed',error=?,updated_at=? WHERE epoch=? AND status IN ('queued','running')`,
		code, updatedAt, epoch)
	return err
}

// RequeueMemoryJobIfFailed puts a failed rebuild back in the queue, which is what a
// retry does. A job in any other state is left alone.
func (s *WorldStore) RequeueMemoryJobIfFailed(ctx context.Context, epoch int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memory_jobs SET status='queued',error='' WHERE epoch=? AND status='failed'`, epoch)
	return err
}

// AppendMemorySourceIfAbsent appends one committed experience to a scope's stream,
// unless that source identifier is already recorded.
//
// The sequence is taken inside the same statement so two appends cannot claim the same
// number.
func (t *WorldTx) AppendMemorySourceIfAbsent(ctx context.Context, record MemorySourceWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_sources(scope,seq,source_id,event_id,run_id,actor,kind,content,created_at)
 SELECT ?,COALESCE(MAX(seq),0)+1,?,?,?,?,?,?,? FROM memory_sources WHERE scope=?`,
		record.Scope, record.ID, record.EventID, record.RunID, record.Actor, record.Kind, record.Content, record.CreatedAt, record.Scope)
	return err
}

// CountMissingEventSources reports how many perceptions or memories name an event that
// is not in the events table.
//
// Which of the two tables to check is the caller's choice of column and is passed as a
// table name from a fixed set; anything else is rejected so a caller cannot turn this
// into an arbitrary query.
func (t *WorldTx) CountMissingEventSources(ctx context.Context, table string) (int, error) {
	var query string
	switch table {
	case "perceptions":
		query = `SELECT COUNT(*) FROM perceptions p LEFT JOIN events e ON e.event_id=p.source_event_id WHERE e.event_id IS NULL`
	case "memories":
		query = `SELECT COUNT(*) FROM memories m LEFT JOIN events e ON e.event_id=m.source_event_id WHERE e.event_id IS NULL`
	default:
		return 0, fmt.Errorf("unknown source table %q", table)
	}
	var missing int
	err := t.tx.QueryRowContext(ctx, query).Scan(&missing)
	return missing, err
}

// CorrectionWrite is one correction to insert.
type CorrectionWrite struct {
	Epoch        int64
	RequestKey   string
	RequestHash  string
	Kind         string
	Scope        string
	TargetID     string
	Original     string
	Replacement  string
	CreatedAt    string
	SceneVersion int64
}

// MemoryJobWrite is one memory job to insert.
type MemoryJobWrite struct {
	Epoch     int64
	Scopes    string
	CreatedAt string
}

// MemoryJobUpdate is the set of memory job fields an update may change.
type MemoryJobUpdate struct {
	Epoch     int64
	Status    string
	Completed int
	Error     string
	UpdatedAt string
}

// MemorySourceWrite is one committed experience to append.
type MemorySourceWrite struct {
	Scope     string
	ID        string
	EventID   string
	RunID     string
	Actor     string
	Kind      string
	Content   string
	CreatedAt string
}
