package storyapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
)

var ErrMemoryRebuilding = errors.New("memory rebuilding")

const correctionSchema = `
CREATE TABLE IF NOT EXISTS corrections (
 epoch INTEGER PRIMARY KEY, request_key TEXT NOT NULL UNIQUE, request_hash TEXT NOT NULL,
 kind TEXT NOT NULL, scope TEXT NOT NULL, target_id TEXT NOT NULL,
 original TEXT NOT NULL, replacement TEXT NOT NULL, created_at TEXT NOT NULL, scene_version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS memory_jobs (
 epoch INTEGER PRIMARY KEY, status TEXT NOT NULL, completed INTEGER NOT NULL,
 scopes TEXT NOT NULL, error TEXT NOT NULL, updated_at TEXT NOT NULL
);`

func (a *App) Correct(ctx context.Context, worldID string, request memorymodel.CorrectionRequest) (memorymodel.Correction, error) {
	schedule := false
	defer func() {
		if schedule {
			a.startMemoryRebuild(worldID)
		}
	}()
	if wire.Clean(request.RequestKey) == "" || len(request.RequestKey) > 200 || request.ExpectedEpoch < 1 || wire.Clean(request.Replacement) == "" || len([]rune(request.Replacement)) > 8000 {
		return memorymodel.Correction{}, ErrInvalidRequest
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return memorymodel.Correction{}, err
	}
	if status != "ready" {
		return memorymodel.Correction{}, ErrWorldNotReady
	}
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	path, status, err = a.worldRecord(ctx, worldID)
	if err != nil {
		return memorymodel.Correction{}, err
	}
	if status != "ready" {
		return memorymodel.Correction{}, ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return memorymodel.Correction{}, err
	}
	defer store.Close()
	hash := wire.MarshalJSON(request)
	var oldHash string
	err = store.Database().QueryRowContext(ctx, `SELECT request_hash FROM corrections WHERE request_key=?`, request.RequestKey).Scan(&oldHash)
	if err == nil {
		if hash != oldHash {
			return memorymodel.Correction{}, ErrIdempotencyConflict
		}
		list, e := readCorrections(ctx, store)
		if e != nil {
			return memorymodel.Correction{}, e
		}
		for _, c := range list {
			if c.Epoch == request.ExpectedEpoch+1 {
				return c, nil
			}
		}
		return memorymodel.Correction{}, ErrStorageUnavailable
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return memorymodel.Correction{}, err
	}
	if worldRT.savePending {
		return memorymodel.Correction{}, ErrWorldBusy
	}
	if count, e := store.CountActiveRuns(ctx); e != nil {
		return memorymodel.Correction{}, e
	} else if count > 0 {
		return memorymodel.Correction{}, ErrWorldBusy
	}
	snapshot, err := loadTurnSnapshot(ctx, store, 100)
	if err != nil {
		return memorymodel.Correction{}, err
	}
	if snapshot.Summary.ContextEpoch != request.ExpectedEpoch {
		return memorymodel.Correction{}, ErrVersionConflict
	}
	if err = loadLongMemory(ctx, store, &snapshot); err != nil {
		return memorymodel.Correction{}, err
	}
	original, err := correctionOriginal(ctx, store, snapshot, request)
	if err != nil {
		return memorymodel.Correction{}, err
	}
	c := memorymodel.Correction{Epoch: request.ExpectedEpoch + 1, Kind: request.Kind, Scope: request.Scope, TargetID: request.TargetID, Original: original, Replacement: wire.Clean(request.Replacement), CreatedAt: wire.NowText()}
	c.SceneVersion = snapshot.SceneVersion
	if err = expandCorrection(ctx, store, &c); err != nil {
		return c, err
	}
	var eventRun string
	if c.Kind == "event" {
		if err = store.Database().QueryRowContext(ctx, `SELECT run_id FROM events WHERE event_id=?`, c.TargetID).Scan(&eventRun); err != nil {
			return c, err
		}
	}
	// This is a business transaction, not a persistence primitive: a correction, the
	// epoch it advances, the rebuilds it invalidates, the notices it writes into each
	// scope's stream, and the new rebuild job it queues all have to land together or
	// the world would believe something was corrected without agreeing on which.
	if err = store.InTx(ctx, func(tx *storage.WorldTx) error {
		if err := tx.InsertCorrection(ctx, storage.CorrectionWrite{
			Epoch: c.Epoch, RequestKey: request.RequestKey, RequestHash: hash, Kind: c.Kind,
			Scope: c.Scope, TargetID: c.TargetID, Original: c.Original, Replacement: c.Replacement,
			CreatedAt: c.CreatedAt, SceneVersion: c.SceneVersion,
		}); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "context_epoch", fmt.Sprint(c.Epoch)); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, "updated_at", c.CreatedAt); err != nil {
			return err
		}
		if err := tx.SupersedeOpenMemoryJobs(ctx); err != nil {
			return err
		}
		for scope, content := range correctionNotices(snapshot, c, eventRun) {
			if err := appendMemorySource(ctx, tx, storage.MemorySourceWrite{
				Scope:     scope,
				ID:        fmt.Sprintf("correction:%d", c.Epoch),
				Actor:     "author",
				Kind:      "correction:" + c.Kind,
				Content:   content,
				CreatedAt: c.CreatedAt,
			}); err != nil {
				return err
			}
		}
		return tx.InsertMemoryJob(ctx, storage.MemoryJobWrite{
			Epoch: c.Epoch, Scopes: wire.MarshalJSON(memoryScopeIDs(snapshot)), CreatedAt: c.CreatedAt,
		})
	}); err != nil {
		return c, err
	}
	schedule = true
	return c, nil
}

func correctionNotices(snapshot turn.Snapshot, c memorymodel.Correction, eventRun string) map[string]string {
	if c.Kind == "perception" || c.Kind == "subjective" {
		return map[string]string{c.Scope: c.Replacement}
	}
	notices := map[string]string{}
	if c.Kind != "event" {
		return notices
	}
	for scope, m := range snapshot.LongMemory {
		for _, s := range m.Archive {
			if !c.Affects(s.EventID) && !(scope == "player" && s.RunID != "" && s.RunID == eventRun) {
				continue
			}
			if s.Content == c.Original {
				notices[scope] = c.Replacement
				break
			}
			notices[scope] = memorymodel.Notice(c, s.ID)
		}
	}
	return notices
}

func correctionOriginal(ctx context.Context, store *storage.WorldStore, s turn.Snapshot, r memorymodel.CorrectionRequest) (string, error) {
	switch r.Kind {
	case "event":
		if r.Scope != "author" {
			return "", ErrInvalidRequest
		}
		var content string
		err := store.Database().QueryRowContext(ctx, `SELECT content FROM events e WHERE event_id=? AND (event_id='opening' OR run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed'))`, r.TargetID).Scan(&content)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidRequest
		}
		if err != nil {
			return "", err
		}
		list, err := readCorrections(ctx, store)
		if err != nil {
			return "", err
		}
		for _, c := range list {
			if c.Kind == "event" && c.TargetID == r.TargetID {
				content = c.Replacement
			}
		}
		return content, nil
	case "character":
		for _, c := range s.Characters {
			if c.EntityID != r.Scope {
				continue
			}
			switch r.TargetID {
			case "profile":
				return c.Profile, nil
			case "knowledge":
				return c.Knowledge, nil
			case "initial_concerns":
				return c.InitialConcerns, nil
			}
		}
	case "perception", "subjective":
		m, ok := s.LongMemory[r.Scope]
		if !ok || r.Scope == "player" {
			return "", ErrInvalidRequest
		}
		for _, item := range m.Archive {
			if item.ID == r.TargetID && ((r.Kind == "perception" && strings.HasPrefix(item.Kind, "perception:")) || (r.Kind == "subjective" && strings.HasPrefix(item.Kind, "subjective:"))) {
				return item.Content, nil
			}
		}
		if r.Kind == "subjective" {
			for i, state := range m.Digest.States {
				if r.TargetID == fmt.Sprintf("state:%d:%d", m.Digest.Revision, i) {
					return state.Content, nil
				}
			}
		}
	case "digest":
		m, ok := s.LongMemory[r.Scope]
		if ok && m.Digest.Revision > 0 && r.TargetID == fmt.Sprint(m.Digest.Revision) {
			return m.Digest.Content, nil
		}
	}
	return "", ErrInvalidRequest
}

// correctedSource applies the corrections that touch one committed experience.
func correctedSource(s memorymodel.MemorySource, corrections []memorymodel.Correction, eventRuns map[string]string) memorymodel.MemorySource {
	return memorymodel.ReplaceSource(s, corrections, eventRuns)
}

// correctionNotices reports the stream entries a correction writes for each scope.

func applySnapshotCorrections(ctx context.Context, store *storage.WorldStore, s *turn.Snapshot) error {
	list, err := readCorrections(ctx, store)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Kind == "character" {
			for i := range s.Characters {
				ch := &s.Characters[i]
				if ch.EntityID != c.Scope {
					continue
				}
				switch c.TargetID {
				case "profile":
					ch.Profile = c.Replacement
				case "knowledge":
					ch.Knowledge = c.Replacement
				case "initial_concerns":
					ch.InitialConcerns = c.Replacement
				}
			}
		}
		if c.Kind == "event" {
			active := make([]turn.GeneratedEvent, 0, len(s.GeneratedEvents.Active))
			for _, e := range s.GeneratedEvents.Active {
				if !c.Affects(e.StartID) && !c.Affects(e.TriggerID) {
					active = append(active, e)
				}
			}
			s.GeneratedEvents.Active = active
			for i := range s.Events {
				if s.Events[i].EventID == c.TargetID {
					s.Events[i].Content = c.Replacement
				} else if c.Affects(s.Events[i].EventID) {
					s.Events[i].Content = correctedSource(memorymodel.MemorySource{EventID: s.Events[i].EventID, Content: s.Events[i].Content}, []memorymodel.Correction{c}, nil).Content
				}
			}
			for i := range s.Dialogue {
				if s.Dialogue[i].EventID == c.TargetID {
					s.Dialogue[i].Content = c.Replacement
				}
			}
			for i := range s.SceneViews {
				view := &s.SceneViews[i]
				affected := false
				for _, id := range view.SourceIDs {
					affected = affected || c.Affects(id)
				}
				if view.Version <= c.SceneVersion && affected {
					view.Content = "当前位置沿用已提交经历；关联情境已纠正，应以本人有效经历重新确认细节。"
				}
			}
			for id, n := range s.PlotProgress.Nodes {
				if n.EventID == c.TargetID {
					n.Content = c.Replacement
					s.PlotProgress.Nodes[id] = n
				}
			}
		}
	}
	for scope, items := range s.Perceptions {
		for i := range items {
			p := &items[i]
			id := "perception:" + strconv.FormatInt(p.Seq, 10)
			v := correctedSource(memorymodel.MemorySource{Scope: scope, ID: id, EventID: p.SourceEventID, Content: p.Content}, list, nil)
			p.Content = v.Content
		}
		s.Perceptions[scope] = items
	}
	for scope, items := range s.Memories {
		for i := range items {
			p := &items[i]
			id := "memory:" + strconv.FormatInt(p.Seq, 10)
			v := correctedSource(memorymodel.MemorySource{Scope: scope, ID: id, EventID: p.SourceEventID, Content: p.Content}, list, nil)
			p.Content = v.Content
		}
		s.Memories[scope] = items
	}
	return nil
}
