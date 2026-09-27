package storyapp

import (
	"context"
	"fmt"
	"strings"
)

type MemoryRecord struct {
	Kind     string `json:"kind"`
	Scope    string `json:"scope"`
	TargetID string `json:"target_id"`
	Content  string `json:"content"`
}
type MemoryView struct {
	Corrections []Correction   `json:"corrections"`
	WorldID     string         `json:"world_id"`
	Epoch       int64          `json:"context_epoch"`
	Scope       string         `json:"scope"`
	Scopes      []string       `json:"scopes"`
	Digest      MemoryDigest   `json:"digest"`
	Sources     []MemorySource `json:"sources"`
	Records     []MemoryRecord `json:"records"`
	Job         MemoryJob      `json:"job"`
	HasMore     bool           `json:"has_more"`
	NextBefore  int64          `json:"next_before_seq,omitempty"`
}

func (a *App) ReadMemory(ctx context.Context, worldID, scope string, author bool, before int64) (MemoryView, error) {
	out := MemoryView{WorldID: worldID, Scope: scope, Scopes: []string{"player"}, Sources: []MemorySource{}, Records: []MemoryRecord{}}
	out.Corrections = []Correction{}
	if before < 0 || (!author && scope != "player") {
		return out, ErrInvalidRequest
	}
	world := a.worldRuntimeFor(worldID)
	world.mu.Lock()
	defer world.mu.Unlock()
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return out, err
	}
	if status != "ready" {
		return out, ErrWorldNotReady
	}
	store, err := openWorldDB(path)
	if err != nil {
		return out, err
	}
	defer store.db.Close()
	snapshot, err := loadWorldSnapshot(ctx, store, 100)
	if err != nil {
		return out, err
	}
	if err = loadLongMemory(ctx, store, &snapshot); err != nil {
		return out, err
	}
	out.Epoch = snapshot.Summary.ContextEpoch
	corrections, err := readCorrections(ctx, store.db)
	if err != nil {
		return out, err
	}
	for i := len(corrections) - 1; i >= 0 && len(out.Corrections) < 100; i-- {
		c := corrections[i]
		if (author && (c.Scope == scope || scope == "author")) || (!author && c.Scope == "player" && c.Kind == "digest") {
			out.Corrections = append(out.Corrections, c)
		}
	}
	out.Job, err = readMemoryJob(ctx, store.db)
	if err != nil {
		return out, err
	}
	if !author {
		out.Job.Scopes = nil
		out.Job.Completed = 0
	}
	if author {
		out.Scopes = append(memoryScopeIDs(snapshot), "author")
	}
	if !containsID(out.Scopes, scope) {
		return out, ErrInvalidRequest
	}
	if scope == "author" {
		// Author records explicitly expose world facts, never through the player API.
		rows, e := store.db.QueryContext(ctx, `SELECT seq,event_id,actor_id,event_type,content,run_id,created_at FROM events e WHERE (?=0 OR seq<?) AND (event_id='opening' OR run_id='' OR EXISTS(SELECT 1 FROM runs r WHERE r.run_id=e.run_id AND r.status='completed')) ORDER BY seq DESC LIMIT 101`, before, before)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			s := MemorySource{Scope: "author"}
			if e = rows.Scan(&s.Seq, &s.ID, &s.Actor, &s.Kind, &s.Content, &s.RunID, &s.CreatedAt); e != nil {
				rows.Close()
				return out, e
			}
			s.EventID = s.ID
			out.Sources = append(out.Sources, s)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		list, e := readCorrections(ctx, store.db)
		if e != nil {
			return out, e
		}
		for i := range out.Sources {
			for _, c := range list {
				if c.Kind == "event" && c.TargetID == out.Sources[i].ID {
					out.Sources[i].Content = c.Replacement
				}
			}
		}
	} else {
		m := snapshot.LongMemory[scope]
		out.Digest = m.Digest
		for i := len(m.Archive) - 1; i >= 0; i-- {
			s := m.Archive[i]
			if before > 0 && s.Seq >= before {
				continue
			}
			out.Sources = append(out.Sources, s)
			if len(out.Sources) == 101 {
				break
			}
		}
		if m.Digest.Revision > 0 {
			out.Records = append(out.Records, MemoryRecord{"digest", scope, fmt.Sprint(m.Digest.Revision), m.Digest.Content})
		}
		if author && scope != "player" {
			for i, s := range m.Digest.States {
				out.Records = append(out.Records, MemoryRecord{"subjective", scope, fmt.Sprintf("state:%d:%d", m.Digest.Revision, i), s.Kind + "：" + s.Content})
			}
			for _, c := range snapshot.Characters {
				if c.EntityID == scope {
					out.Records = append(out.Records, MemoryRecord{"character", scope, "profile", c.Profile}, MemoryRecord{"character", scope, "knowledge", c.Knowledge}, MemoryRecord{"character", scope, "initial_concerns", c.InitialConcerns})
				}
			}
		}
	}
	if len(out.Sources) > 100 {
		out.HasMore = true
		out.Sources = out.Sources[:100]
		out.NextBefore = out.Sources[len(out.Sources)-1].Seq
	}
	if author {
		for _, s := range out.Sources {
			kind := ""
			switch {
			case scope == "author":
				kind = "event"
			case strings.HasPrefix(s.Kind, "perception:"):
				kind = "perception"
			case strings.HasPrefix(s.Kind, "subjective:"):
				kind = "subjective"
			}
			if kind != "" {
				out.Records = append(out.Records, MemoryRecord{kind, scope, s.ID, s.Content})
			}
		}
	}
	return out, nil
}

func (a *App) Corrections(ctx context.Context, worldID string) ([]Correction, MemoryJob, error) {
	world := a.worldRuntimeFor(worldID)
	world.mu.Lock()
	defer world.mu.Unlock()
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return nil, MemoryJob{}, err
	}
	if status != "ready" {
		return nil, MemoryJob{}, ErrWorldNotReady
	}
	store, err := openWorldDB(path)
	if err != nil {
		return nil, MemoryJob{}, err
	}
	defer store.db.Close()
	items, err := readCorrections(ctx, store.db)
	if err != nil {
		return nil, MemoryJob{}, err
	}
	job, err := readMemoryJob(ctx, store.db)
	return items, job, err
}
