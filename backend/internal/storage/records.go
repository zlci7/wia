package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The record types below are what this package can see: rows. They carry the column
// names and nothing else.
//
// They are deliberately not the memory or correction types of the domain. Storage
// answers "what is written down"; whether a correction is about an event, how it
// affects a projection, and what a memory scope is allowed to read are decisions made
// above this package. A shared type would put those decisions here, and this package
// importing the narrative types is the coupling the refactor is removing.

// CorrectionRecord is one stored correction row.
type CorrectionRecord struct {
	Epoch        int64
	Kind         string
	Scope        string
	TargetID     string
	Original     string
	Replacement  string
	CreatedAt    string
	SceneVersion int64
}

// MemoryJobRecord is one stored memory rebuild job row.
type MemoryJobRecord struct {
	Epoch     int64
	Status    string
	Completed int
	Scopes    string
	Error     string
	UpdatedAt string
}

// MemorySourceRecord is one stored projection row: a committed experience, already
// narrowed to what its reader was allowed to perceive when it was written.
type MemorySourceRecord struct {
	Scope     string
	Seq       int64
	ID        string
	EventID   string
	RunID     string
	Actor     string
	Kind      string
	Content   string
	CreatedAt string
}

// MemoryDigestRecord is one stored standing summary row.
type MemoryDigestRecord struct {
	Revision int64
	Epoch    int64
	Through  int64
	Head     int64
	Content  string
	States   string
	Sources  string
}

// LoadCorrections reads every correction in epoch order.
func (s *WorldStore) LoadCorrections(ctx context.Context) ([]CorrectionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT epoch,kind,scope,target_id,original,replacement,created_at,scene_version FROM corrections ORDER BY epoch`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CorrectionRecord{}
	for rows.Next() {
		var c CorrectionRecord
		if err := rows.Scan(&c.Epoch, &c.Kind, &c.Scope, &c.TargetID, &c.Original, &c.Replacement, &c.CreatedAt, &c.SceneVersion); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LoadCorrectionDependents returns every event derived from the given one, following
// the dependency chain transitively.
//
// The walk is a query rather than a loop in Go because the chain is stored as edges
// and a recursive walk belongs where the edges are.
func (s *WorldStore) LoadCorrectionDependents(ctx context.Context, eventID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE affected(id) AS (
 SELECT child_id FROM event_dependencies WHERE parent_id=?
 UNION SELECT d.child_id FROM event_dependencies d JOIN affected a ON d.parent_id=a.id)
 SELECT id FROM affected`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LoadEventRunIDs reports which run each named event belongs to.
func (s *WorldStore) LoadEventRunIDs(ctx context.Context, eventIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(eventIDs))
	for _, id := range eventIDs {
		var run string
		if err := s.db.QueryRowContext(ctx, `SELECT run_id FROM events WHERE event_id=?`, id).Scan(&run); err != nil {
			return nil, err
		}
		out[id] = run
	}
	return out, nil
}

// CountUnfinishedMemoryJobs reports how many memory rebuilds are neither completed nor
// superseded. Whether that means "a new turn may not start yet" is decided above.
func (s *WorldStore) CountUnfinishedMemoryJobs(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_jobs WHERE status NOT IN ('completed','superseded')`).Scan(&count)
	return count, err
}

// LatestMemoryDigest reads the newest digest for one scope, if there is one.
func (s *WorldStore) LatestMemoryDigest(ctx context.Context, scope string) (MemoryDigestRecord, bool, error) {
	var d MemoryDigestRecord
	err := s.db.QueryRowContext(ctx, `SELECT revision,epoch,through_seq,source_head,content,states,sources FROM memory_digests WHERE scope=? ORDER BY revision DESC LIMIT 1`, scope).
		Scan(&d.Revision, &d.Epoch, &d.Through, &d.Head, &d.Content, &d.States, &d.Sources)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	return d, true, nil
}

// LoadMemorySources reads the committed experiences of one scope after a sequence
// number. It returns rows; applying corrections to them happens above, because which
// correction reaches which projection is a rule about memory, not about the table.
func (s *WorldStore) LoadMemorySources(ctx context.Context, scope string, after int64) ([]MemorySourceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scope,seq,source_id,event_id,run_id,actor,kind,content,created_at FROM memory_sources WHERE scope=? AND seq>? ORDER BY seq`, scope, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemorySourceRecord{}
	for rows.Next() {
		var r MemorySourceRecord
		if err := rows.Scan(&r.Scope, &r.Seq, &r.ID, &r.EventID, &r.RunID, &r.Actor, &r.Kind, &r.Content, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MemoryDigestWrite is one standing summary to insert, with the preconditions the
// caller read before deciding to write it.
//
// The preconditions are values, not rules. This package checks that the world has not
// moved since the caller looked — the epoch, the scope's current revision, and its
// source head — and inserts only then. Whether a mismatch should be reported to a
// reader as a conflict, a retry or nothing at all is the caller's decision.
type MemoryDigestWrite struct {
	Scope            string
	Revision         int64
	Epoch            int64
	Through          int64
	ExpectedHead     int64
	ExpectedEpoch    int64
	ExpectedRevision int64
	Content          string
	States           string
	Sources          string
	CreatedAt        string
}

// MemoryDigestConflict reports why a conditional insert did not happen, and what the
// world actually held when it was checked.
type MemoryDigestConflict struct {
	// Epoch is the context epoch that was required but not found.
	Epoch bool
	// Revision and Head are the values found where the caller expected others.
	Revision int64
	Head     int64
}

// CompareAndInsertMemoryDigest inserts one digest only if the world still matches what
// the caller read.
//
// It is a single transaction because the check and the write must not be separated:
// between them another turn could commit and the digest would describe a source stream
// that no longer exists in that form.
func (s *WorldStore) CompareAndInsertMemoryDigest(ctx context.Context, write MemoryDigestWrite) (MemoryDigestConflict, error) {
	var conflict MemoryDigestConflict
	err := s.InTx(ctx, func(tx *WorldTx) error {
		epoch, err := tx.GetMeta(ctx, "context_epoch")
		if err != nil {
			return err
		}
		if epoch != fmt.Sprint(write.ExpectedEpoch) {
			conflict.Epoch = true
			return errDigestConflict
		}
		if err := tx.tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0) FROM memory_digests WHERE scope=?`, write.Scope).Scan(&conflict.Revision); err != nil {
			return err
		}
		if err := tx.tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM memory_sources WHERE scope=?`, write.Scope).Scan(&conflict.Head); err != nil {
			return err
		}
		if conflict.Revision != write.ExpectedRevision || conflict.Head != write.ExpectedHead || conflict.Head < write.Through {
			return errDigestConflict
		}
		_, err = tx.tx.ExecContext(ctx, `INSERT INTO memory_digests(scope,revision,epoch,through_seq,source_head,content,states,sources,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			write.Scope, write.Revision, write.Epoch, write.Through, conflict.Head, write.Content, write.States, write.Sources, write.CreatedAt)
		return err
	})
	if errors.Is(err, errDigestConflict) {
		return conflict, nil
	}
	return conflict, err
}

// errDigestConflict rolls back a conditional insert without escaping this package: the
// caller learns about the conflict from the returned struct, not from an error whose
// meaning it would have to interpret.
var errDigestConflict = errors.New("memory digest precondition not met")
