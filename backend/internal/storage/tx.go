package storage

import (
	"context"
	"database/sql"
	"errors"
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

// DeleteMeta removes a header value inside the transaction.
//
// Derived material that depended on what was deleted is no longer valid, so a caller
// removes the header it stored rather than rewriting it to an empty value: absence and
// emptiness are different answers to "is there a stored value".
func (t *WorldTx) DeleteMeta(ctx context.Context, key string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM meta WHERE key=?`, key)
	return err
}

// MaxRunInputSequence reports the highest input sequence accepted so far, which is the
// value a caller compares against the one it read.
func (t *WorldTx) MaxRunInputSequence(ctx context.Context) (int64, error) {
	var seq int64
	err := t.tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(input_seq),0) FROM runs`).Scan(&seq)
	return seq, err
}

// CountCompletedRunsWithInput reports how many runs for one input already completed.
//
// It counts rather than returning a bool because the caller's rule is "more than none",
// and a count is the smaller thing to hand over: the decision stays above.
func (t *WorldTx) CountCompletedRunsWithInput(ctx context.Context, inputID string) (int, error) {
	var count int
	err := t.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE input_id=? AND status='completed'`, inputID).Scan(&count)
	return count, err
}

// InsertRun writes one accepted run.
func (t *WorldTx) InsertRun(ctx context.Context, record RunWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO runs(
		run_id,request_key,request_hash,input,addressee_id,attempt,status,input_id,input_seq,base_turn_seq,base_message_head,base_event_head,base_context_epoch,base_scene_version,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, record.RunID, record.RequestKey, record.RequestHash, record.Input,
		record.AddresseeID, record.Attempt, record.Status, record.InputID, record.InputSeq, record.BaseTurnSeq, record.BaseMessageHead, record.BaseEventHead,
		record.BaseContextEpoch, record.BaseSceneVersion, record.CreatedAt, record.UpdatedAt)
	return err
}

// RunWrite is one run to insert. The timestamps are already formatted text.
type RunWrite struct {
	RunID            string
	RequestKey       string
	RequestHash      string
	Input            string
	AddresseeID      string
	Attempt          int
	Status           string
	InputID          string
	InputSeq         int64
	BaseTurnSeq      int64
	BaseMessageHead  int64
	BaseEventHead    int64
	BaseContextEpoch int64
	BaseSceneVersion int64
	CreatedAt        string
	UpdatedAt        string
}

// RunStatus reports a run's stored status and whether cancellation was requested.
func (t *WorldTx) RunStatus(ctx context.Context, runID string) (status string, cancelRequested bool, err error) {
	var cancelled int
	err = t.tx.QueryRowContext(ctx, `SELECT status,cancel_requested FROM runs WHERE run_id=?`, runID).Scan(&status, &cancelled)
	return status, cancelled != 0, err
}

// EventWrite is one committed event. Stage is the pipeline position that produced it.
type EventWrite struct {
	Seq          int64
	EventID      string
	EventType    string
	ActorID      string
	TargetID     string
	Content      string
	RunID        string
	Stage        int
	SceneVersion int64
	SourceType   string
	CreatedAt    string
}

// InsertEvent writes one committed event.
func (t *WorldTx) InsertEvent(ctx context.Context, event EventWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO events(seq,event_id,event_type,actor_id,target_id,content,run_id,stage,scene_version,source_type,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		event.Seq, event.EventID, event.EventType, event.ActorID, event.TargetID, event.Content, event.RunID, event.Stage, event.SceneVersion, event.SourceType, event.CreatedAt)
	return err
}

// InsertEventDependency records that one event is a projection of another. The edge is
// what a correction walks to find what else stopped being valid.
func (t *WorldTx) InsertEventDependency(ctx context.Context, childID, parentID string) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO event_dependencies(child_id,parent_id) VALUES(?,?)`, childID, parentID)
	return err
}

// PerceptionWrite is one character's record of what it perceived.
type PerceptionWrite struct {
	RecipientID   string
	SourceEventID string
	SourceType    string
	Content       string
	Stage         int
	SceneVersion  int64
	CreatedAt     string
}

// InsertPerceptionIfAbsent records one perception, tolerating a repeat.
func (t *WorldTx) InsertPerceptionIfAbsent(ctx context.Context, perception PerceptionWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT OR IGNORE INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES(?,?,?,?,?,?,?)`,
		perception.RecipientID, perception.SourceEventID, perception.SourceType, perception.Content, perception.Stage, perception.SceneVersion, perception.CreatedAt)
	return err
}

// MemoryWrite is one character's subjective memory of an event.
type MemoryWrite struct {
	RecipientID   string
	Kind          string
	Content       string
	SourceEventID string
	CreatedAt     string
}

// InsertMemoryIfAbsent records one subjective memory, tolerating a repeat.
func (t *WorldTx) InsertMemoryIfAbsent(ctx context.Context, memory MemoryWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT OR IGNORE INTO memories(recipient_id,kind,content,source_event_id,created_at) VALUES(?,?,?,?,?)`,
		memory.RecipientID, memory.Kind, memory.Content, memory.SourceEventID, memory.CreatedAt)
	return err
}

// InsertMessage writes one message at the given sequence.
func (t *WorldTx) InsertMessage(ctx context.Context, seq int64, messageID, kind, content, runID, createdAt string) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(?,?,?,?,?,?)`,
		seq, messageID, kind, content, runID, createdAt)
	return err
}

// ClearScenePresence takes every character out of the scene.
func (t *WorldTx) ClearScenePresence(ctx context.Context) error {
	_, err := t.tx.ExecContext(ctx, `UPDATE characters SET in_scene=0`)
	return err
}

// CharacterWrite is one character in the cast. InScene is stored as an integer because
// that is how the column holds it.
type CharacterWrite struct {
	EntityID     string
	DefinitionID string
	Name         string
	Role         string
	Profile      string
	Knowledge    string
	InScene      bool
}

// InsertCharacter adds one character to the cast.
func (t *WorldTx) InsertCharacter(ctx context.Context, character CharacterWrite) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO characters(entity_id,definition_id,name,role,profile,knowledge,in_scene) VALUES(?,?,?,?,?,?,?)`,
		character.EntityID, character.DefinitionID, character.Name, character.Role, character.Profile, character.Knowledge, boolInt(character.InScene))
	return err
}

// boolInt stores a flag the way the schema holds it. It is duplicated from wire rather
// than imported so that this package stays free of the module's other packages.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// SetScenePresence puts one character in the scene. It reports whether that character
// exists, because a scene naming someone the world does not have is a caller error
// rather than a storage one.
func (t *WorldTx) SetScenePresence(ctx context.Context, entityID string) (bool, error) {
	result, err := t.tx.ExecContext(ctx, `UPDATE characters SET in_scene=1 WHERE entity_id=?`, entityID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// CompleteRun marks one run completed and reports whether that was still possible.
//
// The two conditions — the run is running, and cancellation was not requested — are
// preconditions of the write itself: a turn that finished after a cancellation must not
// be recorded as the run's result. A caller learns "not possible" from the bool and
// decides what it means.
func (t *WorldTx) CompleteRun(ctx context.Context, runID string, messageSeq int64, updatedAt string) (bool, error) {
	result, err := t.tx.ExecContext(ctx, `UPDATE runs SET status='completed',reason='',error='',message_seq=?,updated_at=? WHERE run_id=? AND status='running' AND cancel_requested=0`,
		messageSeq, updatedAt, runID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
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

// AppendMemorySource reports the persisted fact for one source identity. A source that
// is not present is appended, the same content is an idempotent repeat, and different
// content is reported as a conflict for the caller to interpret.
func (t *WorldTx) AppendMemorySource(ctx context.Context, record MemorySourceWrite) (MemorySourceAppendResult, error) {
	var content string
	err := t.tx.QueryRowContext(ctx, `SELECT content FROM memory_sources WHERE scope=? AND source_id=?`, record.Scope, record.ID).Scan(&content)
	switch {
	case err == nil && content == record.Content:
		return MemorySourceUnchanged, nil
	case err == nil:
		return MemorySourceContentConflict, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}
	_, err = t.tx.ExecContext(ctx, `INSERT INTO memory_sources(scope,seq,source_id,event_id,run_id,actor,kind,content,created_at)
 SELECT ?,COALESCE(MAX(seq),0)+1,?,?,?,?,?,?,? FROM memory_sources WHERE scope=?`,
		record.Scope, record.ID, record.EventID, record.RunID, record.Actor, record.Kind, record.Content, record.CreatedAt, record.Scope)
	if err != nil {
		return 0, err
	}
	return MemorySourceInserted, nil
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

// MemoryProjectionCandidates returns the projections that are eligible to become
// committed experiences, in the order a reader should see them.
//
// Who is eligible is a join against committed runs and character identities, so it is a
// query rather than a rule here. It runs inside the caller's transaction so the rows it
// returns are the same ones the caller is about to append against; the ordering is the
// one the stream depends on and is not the caller's to choose.
func (t *WorldTx) MemoryProjectionCandidates(ctx context.Context) ([]MemorySourceRecord, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT scope,id,event_id,run_id,actor,kind,content,created_at FROM (
 SELECT p.recipient_id scope,'perception:'||p.seq id,e.event_id,e.run_id,e.actor_id actor,'perception:'||p.source_type kind,p.content,p.created_at,e.seq ordering,0 priority,p.seq tie_seq FROM perceptions p JOIN events e ON e.event_id=p.source_event_id WHERE e.run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed')
 UNION ALL
 SELECT m.recipient_id,'memory:'||m.seq,e.event_id,e.run_id,m.recipient_id,'subjective:'||m.kind,m.content,m.created_at,e.seq,1,m.seq FROM memories m JOIN events e ON e.event_id=m.source_event_id WHERE e.run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed')
 UNION ALL
 SELECT e.actor_id,'speech:'||e.event_id,e.event_id,e.run_id,e.actor_id,'own_speech',e.content,e.created_at,e.seq,2,e.seq FROM events e WHERE e.event_type='npc_dialogue' AND EXISTS(SELECT 1 FROM characters c WHERE c.entity_id=e.actor_id) AND EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed')
 UNION ALL
 SELECT 'player','message:'||m.message_id,'',m.run_id,CASE WHEN m.kind='player' THEN 'player' ELSE 'narrator' END,'message:'||m.kind,m.content,m.created_at,0,3,m.seq FROM messages m WHERE m.message_id='opening' OR m.run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=m.run_id AND r.status='completed')
) ORDER BY ordering,priority,tie_seq,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemorySourceRecord{}
	for rows.Next() {
		var record MemorySourceRecord
		if err := rows.Scan(&record.Scope, &record.ID, &record.EventID, &record.RunID, &record.Actor, &record.Kind, &record.Content, &record.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
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

// MemorySourceAppendResult is the storage-level outcome of appending one source.
// It deliberately carries no business error: the caller decides what a content
// conflict means for indexing, correction or another workflow.
type MemorySourceAppendResult uint8

const (
	MemorySourceInserted MemorySourceAppendResult = iota + 1
	MemorySourceUnchanged
	MemorySourceContentConflict
)
