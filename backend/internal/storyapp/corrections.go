package storyapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"gameagent/backend/internal/wire"
	"strconv"
	"strings"
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

type CorrectionRequest struct {
	RequestKey    string `json:"request_key"`
	ExpectedEpoch int64  `json:"expected_context_epoch"`
	Kind          string `json:"kind"`
	Scope         string `json:"scope"`
	TargetID      string `json:"target_id"`
	Replacement   string `json:"replacement"`
}
type Correction struct {
	Dependents   []string `json:"-"`
	SceneVersion int64    `json:"scene_version"`
	Epoch        int64    `json:"epoch"`
	Kind         string   `json:"kind"`
	Scope        string   `json:"scope"`
	TargetID     string   `json:"target_id"`
	Original     string   `json:"original"`
	Replacement  string   `json:"replacement"`
	CreatedAt    string   `json:"created_at"`
}
type MemoryJob struct {
	Epoch     int64    `json:"epoch"`
	Status    string   `json:"status"`
	Completed int      `json:"completed"`
	Scopes    []string `json:"scopes"`
	Error     string   `json:"error"`
}

func readCorrections(ctx context.Context, db *sql.DB) ([]Correction, error) {
	rows, err := db.QueryContext(ctx, `SELECT epoch,kind,scope,target_id,original,replacement,created_at,scene_version FROM corrections ORDER BY epoch`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Correction{}
	for rows.Next() {
		var c Correction
		if err = rows.Scan(&c.Epoch, &c.Kind, &c.Scope, &c.TargetID, &c.Original, &c.Replacement, &c.CreatedAt, &c.SceneVersion); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range result {
		if err = expandCorrection(ctx, db, &result[i]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func memoryReady(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_jobs WHERE status NOT IN ('completed','superseded')`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrMemoryRebuilding
	}
	return nil
}

func (a *App) Correct(ctx context.Context, worldID string, request CorrectionRequest) (Correction, error) {
	schedule := false
	defer func() {
		if schedule {
			a.startMemoryRebuild(worldID)
		}
	}()
	if wire.Clean(request.RequestKey) == "" || len(request.RequestKey) > 200 || request.ExpectedEpoch < 1 || wire.Clean(request.Replacement) == "" || len([]rune(request.Replacement)) > 8000 {
		return Correction{}, ErrInvalidRequest
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return Correction{}, err
	}
	if status != "ready" {
		return Correction{}, ErrWorldNotReady
	}
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	path, status, err = a.worldRecord(ctx, worldID)
	if err != nil {
		return Correction{}, err
	}
	if status != "ready" {
		return Correction{}, ErrWorldNotReady
	}
	store, err := openWorldDB(path)
	if err != nil {
		return Correction{}, err
	}
	defer store.db.Close()
	hash := wire.MarshalJSON(request)
	var oldHash string
	err = store.db.QueryRowContext(ctx, `SELECT request_hash FROM corrections WHERE request_key=?`, request.RequestKey).Scan(&oldHash)
	if err == nil {
		if hash != oldHash {
			return Correction{}, ErrIdempotencyConflict
		}
		list, e := readCorrections(ctx, store.db)
		if e != nil {
			return Correction{}, e
		}
		for _, c := range list {
			if c.Epoch == request.ExpectedEpoch+1 {
				return c, nil
			}
		}
		return Correction{}, ErrStorageUnavailable
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Correction{}, err
	}
	if worldRT.savePending {
		return Correction{}, ErrWorldBusy
	}
	if count, e := countActiveRuns(ctx, store.db); e != nil {
		return Correction{}, e
	} else if count > 0 {
		return Correction{}, ErrWorldBusy
	}
	snapshot, err := loadWorldSnapshot(ctx, store, 100)
	if err != nil {
		return Correction{}, err
	}
	if snapshot.Summary.ContextEpoch != request.ExpectedEpoch {
		return Correction{}, ErrVersionConflict
	}
	if err = loadLongMemory(ctx, store, &snapshot); err != nil {
		return Correction{}, err
	}
	original, err := correctionOriginal(ctx, store, snapshot, request)
	if err != nil {
		return Correction{}, err
	}
	c := Correction{Epoch: request.ExpectedEpoch + 1, Kind: request.Kind, Scope: request.Scope, TargetID: request.TargetID, Original: original, Replacement: wire.Clean(request.Replacement), CreatedAt: wire.NowText()}
	c.SceneVersion = snapshot.SceneVersion
	if err = expandCorrection(ctx, store.db, &c); err != nil {
		return c, err
	}
	var eventRun string
	if c.Kind == "event" {
		if err = store.db.QueryRowContext(ctx, `SELECT run_id FROM events WHERE event_id=?`, c.TargetID).Scan(&eventRun); err != nil {
			return c, err
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO corrections VALUES(?,?,?,?,?,?,?,?,?,?)`, c.Epoch, request.RequestKey, hash, c.Kind, c.Scope, c.TargetID, c.Original, c.Replacement, c.CreatedAt, c.SceneVersion)
	if err != nil {
		return c, err
	}
	if err = metaSetTx(ctx, tx, "context_epoch", fmt.Sprint(c.Epoch)); err != nil {
		return c, err
	}
	if err = metaSetTx(ctx, tx, "updated_at", c.CreatedAt); err != nil {
		return c, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE memory_jobs SET status='superseded' WHERE status!='completed'`); err != nil {
		return c, err
	}
	scopes := memoryScopeIDs(snapshot)
	for scope, content := range correctionNotices(snapshot, c, eventRun) {
		_, err = tx.ExecContext(ctx, `INSERT INTO memory_sources SELECT ?,COALESCE(MAX(seq),0)+1,?,'',?,'author',?,?,? FROM memory_sources WHERE scope=?`, scope, fmt.Sprintf("correction:%d", c.Epoch), fmt.Sprintf("correction:%d", c.Epoch), "correction:"+c.Kind, content, c.CreatedAt, scope)
		if err != nil {
			return c, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO memory_jobs VALUES(?,'queued',0,?,'',?)`, c.Epoch, wire.MarshalJSON(scopes), c.CreatedAt); err != nil {
		return c, err
	}
	if err = tx.Commit(); err != nil {
		return c, err
	}
	schedule = true
	return c, nil
}

func correctionNotices(snapshot worldSnapshot, c Correction, eventRun string) map[string]string {
	if c.Kind == "perception" || c.Kind == "subjective" {
		return map[string]string{c.Scope: c.Replacement}
	}
	notices := map[string]string{}
	if c.Kind != "event" {
		return notices
	}
	for scope, m := range snapshot.LongMemory {
		for _, s := range m.Archive {
			if !c.affects(s.EventID) && !(scope == "player" && s.RunID != "" && s.RunID == eventRun) {
				continue
			}
			if s.Content == c.Original {
				notices[scope] = c.Replacement
				break
			}
			notices[scope] = "与记录 " + s.ID + " 关联的经历已纠正；原解释及由它得出的判断已失效，尚未获得替代投影。"
		}
	}
	return notices
}

func correctionOriginal(ctx context.Context, store *worldStore, s worldSnapshot, r CorrectionRequest) (string, error) {
	switch r.Kind {
	case "event":
		if r.Scope != "author" {
			return "", ErrInvalidRequest
		}
		var content string
		err := store.db.QueryRowContext(ctx, `SELECT content FROM events e WHERE event_id=? AND (event_id='opening' OR run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed'))`, r.TargetID).Scan(&content)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidRequest
		}
		if err != nil {
			return "", err
		}
		list, err := readCorrections(ctx, store.db)
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

// A corrected author event does not grant its replacement to recipients that
// originally received only a partial projection. Those projections are retracted.
func correctedSource(s MemorySource, corrections []Correction, eventRuns map[string]string) MemorySource {
	for _, c := range corrections {
		if (c.Kind == "perception" || c.Kind == "subjective") && c.Scope == s.Scope && c.TargetID == s.ID {
			s.Content = c.Replacement
		}
		if c.Kind != "event" {
			continue
		}
		if c.affects(s.EventID) {
			if s.Content == c.Original {
				s.Content = c.Replacement
			} else {
				s.Content = "此条经历依赖的事件已纠正；原有解释已失效，尚未获得替代投影。"
			}
		} else if s.Scope == "player" && s.RunID != "" && s.RunID == eventRuns[c.TargetID] && s.Kind == "message:narrative" {
			s.Content = "本回合旧正文保留在阅读历史中；涉及已纠正事件，不作为后续事实或回顾依据。"
		} else if s.Scope == "player" && s.RunID != "" && s.RunID == eventRuns[c.TargetID] && s.Kind == "message:player" && s.Content == c.Original {
			s.Content = c.Replacement
		}
	}
	return s
}

func correctionEventRuns(ctx context.Context, db *sql.DB, list []Correction) (map[string]string, error) {
	runs := map[string]string{}
	for _, c := range list {
		if c.Kind != "event" {
			continue
		}
		var run string
		if err := db.QueryRowContext(ctx, `SELECT run_id FROM events WHERE event_id=?`, c.TargetID).Scan(&run); err != nil {
			return nil, err
		}
		runs[c.TargetID] = run
	}
	return runs, nil
}

func applySnapshotCorrections(ctx context.Context, store *worldStore, s *worldSnapshot) error {
	list, err := readCorrections(ctx, store.db)
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
			active := make([]generatedEvent, 0, len(s.GeneratedEvents.Active))
			for _, e := range s.GeneratedEvents.Active {
				if !c.affects(e.StartID) && !c.affects(e.TriggerID) {
					active = append(active, e)
				}
			}
			s.GeneratedEvents.Active = active
			for i := range s.Events {
				if s.Events[i].EventID == c.TargetID {
					s.Events[i].Content = c.Replacement
				} else if c.affects(s.Events[i].EventID) {
					s.Events[i].Content = correctedSource(MemorySource{EventID: s.Events[i].EventID, Content: s.Events[i].Content}, []Correction{c}, nil).Content
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
					affected = affected || c.affects(id)
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
			v := correctedSource(MemorySource{Scope: scope, ID: id, EventID: p.SourceEventID, Content: p.Content}, list, nil)
			p.Content = v.Content
		}
		s.Perceptions[scope] = items
	}
	for scope, items := range s.Memories {
		for i := range items {
			p := &items[i]
			id := "memory:" + strconv.FormatInt(p.Seq, 10)
			v := correctedSource(MemorySource{Scope: scope, ID: id, EventID: p.SourceEventID, Content: p.Content}, list, nil)
			p.Content = v.Content
		}
		s.Memories[scope] = items
	}
	return nil
}
