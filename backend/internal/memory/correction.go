package memory

import (
	"strings"

	wiaworld "gameagent/backend/internal/world"
)

// Correction is an author-issued replacement for something the world already
// committed: an event, a character's private material, one perception, one subjective
// state, or a digest. It is the record of what was corrected and what it became.
//
// Dependents holds the events that were derived from the corrected one. It is not
// serialised: it is computed when a correction is applied, so a reader can tell what
// else stopped being valid without storing a second copy of the graph.
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

// CorrectionRequest is what an author asks for. The epoch is the one they read, so a
// correction written against a stale world is rejected rather than applied to
// something else.
type CorrectionRequest struct {
	RequestKey    string `json:"request_key"`
	ExpectedEpoch int64  `json:"expected_context_epoch"`
	Kind          string `json:"kind"`
	Scope         string `json:"scope"`
	TargetID      string `json:"target_id"`
	Replacement   string `json:"replacement"`
}

// MemoryJob is one scheduled rebuild of a scope's memory, and the only durable record
// that a rebuild is outstanding.
type MemoryJob struct {
	Epoch     int64    `json:"epoch"`
	Status    string   `json:"status"`
	Completed int      `json:"completed"`
	Scopes    []string `json:"scopes"`
	Error     string   `json:"error"`
}

// Affects reports whether this correction is about the given event, which is how a
// derived record learns that its basis was replaced. A dependent event counts: the
// correction of a parent invalidates what was projected from it.
func (c Correction) Affects(eventID string) bool {
	return eventID == c.TargetID || wiaworld.ContainsID(c.Dependents, eventID)
}

// ReplaceSource applies every correction that concerns one committed experience and
// returns what that experience now reads as.
//
// A corrected author event does not grant its replacement to a reader who originally
// received only a partial projection: that projection is retracted with an
// explanation instead, because the correction removes the basis for it rather than
// handing over more than was ever authorised.
func ReplaceSource(source MemorySource, corrections []Correction, eventRuns map[string]string) MemorySource {
	for _, c := range corrections {
		if (c.Kind == "perception" || c.Kind == "subjective") && c.Scope == source.Scope && c.TargetID == source.ID {
			source.Content = c.Replacement
		}
		if c.Kind != "event" {
			continue
		}
		if c.Affects(source.EventID) {
			if source.Content == c.Original {
				source.Content = c.Replacement
			} else {
				source.Content = "此条经历依赖的事件已纠正；原有解释已失效，尚未获得替代投影。"
			}
		} else if source.Scope == "player" && source.RunID != "" && source.RunID == eventRuns[c.TargetID] && source.Kind == "message:narrative" {
			source.Content = "本回合旧正文保留在阅读历史中；涉及已纠正事件，不作为后续事实或回顾依据。"
		} else if source.Scope == "player" && source.RunID != "" && source.RunID == eventRuns[c.TargetID] && source.Kind == "message:player" && source.Content == c.Original {
			source.Content = c.Replacement
		}
	}
	return source
}

// Notice is the experience-level message that replaces a retracted projection. It is
// written into the scope's stream so the character's memory carries the retraction
// rather than silently losing the record.
func Notice(c Correction, recordID string) string {
	if recordID != "" {
		return "与记录 " + recordID + " 关联的经历已纠正；原解释及由它得出的判断已失效，尚未获得替代投影。"
	}
	return "关联的经历已纠正；原解释及由它得出的判断已失效，尚未获得替代投影。"
}

// NoticeKind names the kind of stream entry a correction writes.
func NoticeKind(c Correction) string { return "correction:" + strings.TrimSpace(c.Kind) }
