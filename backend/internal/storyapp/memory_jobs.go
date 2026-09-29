package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func readMemoryJob(ctx context.Context, db *sql.DB) (MemoryJob, error) {
	j := MemoryJob{Status: "completed", Scopes: []string{}}
	var scopes string
	err := db.QueryRowContext(ctx, `SELECT epoch,status,completed,scopes,error FROM memory_jobs ORDER BY epoch DESC LIMIT 1`).Scan(&j.Epoch, &j.Status, &j.Completed, &scopes, &j.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return j, nil
	}
	if err != nil {
		return j, err
	}
	err = json.Unmarshal([]byte(scopes), &j.Scopes)
	return j, err
}

func (a *App) startMemoryRebuild(worldID string) {
	a.copyMu.Lock()
	defer a.copyMu.Unlock()
	if a.closing {
		return
	}
	if a.memoryWorkers == nil {
		a.memoryWorkers = map[string]bool{}
		a.memoryWake = map[string]bool{}
	}
	if a.memoryWorkers[worldID] {
		a.memoryWake[worldID] = true
		return
	}
	a.memoryWorkers[worldID] = true
	a.copyWG.Add(1)
	go func() {
		defer a.copyWG.Done()
		defer func() {
			a.copyMu.Lock()
			wake := a.memoryWake[worldID]
			delete(a.memoryWake, worldID)
			delete(a.memoryWorkers, worldID)
			a.copyMu.Unlock()
			if wake {
				a.startMemoryRebuild(worldID)
			}
		}()
		for {
			done, err := a.rebuildMemoryStep(a.copyCtx, worldID)
			if err != nil || done {
				return
			}
		}
	}()
}

// Each read/publish phase holds the world lock and closes its database before
// model execution. Deletion can complete and late results cannot recreate a DB.
func (a *App) rebuildMemoryStep(ctx context.Context, worldID string) (bool, error) {
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil || status != "ready" {
		worldRT.mu.Unlock()
		return true, err
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		worldRT.mu.Unlock()
		return true, err
	}
	job, err := readMemoryJob(ctx, store.Database())
	if err != nil || job.Status == "completed" || job.Status == "failed" {
		store.Close()
		worldRT.mu.Unlock()
		return true, err
	}
	snapshot, err := loadWorldSnapshot(ctx, store, 40)
	if err == nil {
		err = loadLongMemory(ctx, store, &snapshot)
	}
	if err != nil {
		failMemoryJob(ctx, store.Database(), job.Epoch, err)
		store.Close()
		worldRT.mu.Unlock()
		return true, err
	}
	if job.Completed >= len(job.Scopes) {
		_, err = store.Database().ExecContext(ctx, `UPDATE memory_jobs SET status='completed',updated_at=? WHERE epoch=?`, wire.NowText(), job.Epoch)
		store.Close()
		worldRT.mu.Unlock()
		return true, err
	}
	scope := job.Scopes[job.Completed]
	m := snapshot.LongMemory[scope]
	corrections, err := readCorrections(ctx, store.Database())
	var eventRuns map[string]string
	if err == nil {
		eventRuns, err = correctionEventRuns(ctx, store.Database(), corrections)
	}
	if err != nil {
		failMemoryJob(ctx, store.Database(), job.Epoch, err)
		store.Close()
		worldRT.mu.Unlock()
		return true, err
	}
	_, err = store.Database().ExecContext(ctx, `UPDATE memory_jobs SET status='running',error='',updated_at=? WHERE epoch=?`, wire.NowText(), job.Epoch)
	store.Close()
	worldRT.mu.Unlock()
	if err != nil {
		return true, err
	}

	previous := m.Digest
	basis := previous
	if digestNeedsRebuild(basis, scope, m.Archive, corrections, eventRuns) {
		basis.Content = ""
		basis.States = []memorymodel.SubjectiveState{}
		basis.Sources = []string{}
		basis.Through = 0
	}
	remaining := []memorymodel.MemorySource{}
	for _, s := range m.Archive {
		if s.Seq > basis.Through {
			remaining = append(remaining, s)
		}
	}
	groups := memoryGroups(remaining)
	prefix := []memorymodel.MemorySource{}
	if len(groups) > 4 {
		for _, group := range groups[:len(groups)-4] {
			candidate := append(append([]memorymodel.MemorySource{}, prefix...), group...)
			if len(memoryRecordsText(candidate)) > 18000 && len(prefix) > 0 {
				break
			}
			prefix = candidate
		}
	}
	d := basis
	d.Scope = scope
	d.Epoch = job.Epoch
	d.Revision = previous.Revision + 1
	manualDigest := pendingDigestEdit(previous, scope, job.Epoch, m.Archive, corrections, eventRuns)
	if len(prefix) > 0 && manualDigest == nil {
		a.modelMu.RLock()
		generator := a.generator
		a.modelMu.RUnlock()
		if generator == nil {
			err = ErrModelNotConfigured
		} else {
			d, err = a.summarizeMemory(ctx, generator, snapshot, wiaworld.Run{RunID: fmt.Sprintf("memory:%d", job.Epoch), BaseContextEpoch: job.Epoch}, scope, basis, prefix)
			d.Revision = previous.Revision + 1
		}
	}
	if manualDigest != nil {
		d = previous
		d.Revision++
		d.Epoch = job.Epoch
		d.Content = manualDigest.Replacement
		if digestNeedsRebuild(previous, scope, m.Archive, corrections, eventRuns) {
			d.States = []memorymodel.SubjectiveState{}
		}
		err = nil
		prefix = nil
	}
	if len(m.Archive) > 0 {
		d.Head = m.Archive[len(m.Archive)-1].Seq
	}

	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	path, status, checkErr := a.worldRecord(ctx, worldID)
	if checkErr != nil || status != "ready" {
		return true, checkErr
	}
	store, checkErr = storage.OpenWorldDB(path)
	if checkErr != nil {
		return true, checkErr
	}
	defer store.Close()
	current, checkErr := readMemoryJob(ctx, store.Database())
	if checkErr != nil {
		return true, checkErr
	}
	if current.Epoch != job.Epoch {
		return false, nil
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err != nil {
		saveErr := failMemoryJob(ctx, store.Database(), job.Epoch, err)
		return true, saveErr
	}
	if err = publishDigest(ctx, store, d, previous.Revision, job.Epoch); err != nil {
		failMemoryJob(ctx, store.Database(), job.Epoch, err)
		return true, err
	}
	doneScope := len(prefix) == 0 || len(memoryGroups(afterMemory(m.Archive, d.Through))) <= 4
	completed := job.Completed
	if doneScope {
		completed++
	}
	status = "running"
	if completed == len(job.Scopes) {
		status = "completed"
	}
	_, err = store.Database().ExecContext(ctx, `UPDATE memory_jobs SET status=?,completed=?,updated_at=? WHERE epoch=?`, status, completed, wire.NowText(), job.Epoch)
	return status == "completed", err
}

func failMemoryJob(ctx context.Context, db *sql.DB, epoch int64, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, err := db.ExecContext(ctx, `UPDATE memory_jobs SET status='failed',error=?,updated_at=? WHERE epoch=? AND status IN ('queued','running')`, safeTurnErrorCode(cause), wire.NowText(), epoch)
	return err
}

// Job supersession cancels work, not accepted edits. The published digest epoch
// is the watermark for edits already incorporated; later relevant corrections
// invalidate a pending manual digest just as they invalidate a published one.
func pendingDigestEdit(previous memorymodel.MemoryDigest, scope string, epoch int64, archive []memorymodel.MemorySource, corrections []Correction, eventRuns map[string]string) *Correction {
	var edit *Correction
	for _, c := range corrections {
		if c.Kind == "digest" && c.Scope == scope && c.Epoch > previous.Epoch && c.Epoch <= epoch {
			copy := c
			edit = &copy
		}
	}
	if edit != nil {
		basis := previous
		basis.Epoch = edit.Epoch
		if digestNeedsRebuild(basis, scope, archive, corrections, eventRuns) {
			return nil
		}
	}
	return edit
}

// Unrelated corrections advance the world epoch without invalidating a person's
// effective digest, including an explicit edit to that digest.
func digestNeedsRebuild(d memorymodel.MemoryDigest, scope string, archive []memorymodel.MemorySource, corrections []Correction, eventRuns map[string]string) bool {
	for _, c := range corrections {
		if c.Epoch <= d.Epoch || c.Kind == "digest" {
			continue
		}
		if c.Scope == scope {
			return true
		}
		if c.Kind == "event" {
			for _, s := range archive {
				if s.Seq <= d.Through && (c.affects(s.EventID) || (scope == "player" && s.RunID != "" && s.RunID == eventRuns[c.TargetID])) {
					return true
				}
			}
		}
	}
	return false
}

func afterMemory(items []memorymodel.MemorySource, seq int64) []memorymodel.MemorySource {
	var result []memorymodel.MemorySource
	for _, s := range items {
		if s.Seq > seq {
			result = append(result, s)
		}
	}
	return result
}

func (a *App) RetryMemory(ctx context.Context, worldID string, epoch int64) error {
	schedule := false
	defer func() {
		if schedule {
			a.startMemoryRebuild(worldID)
		}
	}()
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return err
	}
	if status != "ready" {
		return ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return err
	}
	defer store.Close()
	current, err := store.MetaInt(ctx, "context_epoch")
	if err != nil {
		return err
	}
	if current != epoch {
		return ErrVersionConflict
	}
	if _, err = store.Database().ExecContext(ctx, `UPDATE memory_jobs SET status='queued',error='' WHERE epoch=? AND status='failed'`, epoch); err != nil {
		return err
	}
	schedule = true
	return nil
}

func (a *App) resumeMemoryJobs(ctx context.Context) error {
	worlds, err := a.ListWorlds(ctx)
	if err != nil {
		return err
	}
	for _, w := range worlds {
		a.startMemoryRebuild(w.WorldID)
	}
	return nil
}
