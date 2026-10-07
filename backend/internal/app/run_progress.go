package app

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/turn"
)

const runReasoningByteLimit = 256 << 10

// RunProgress is transient observation, separate from committed story data.
type RunProgress struct {
	RunID         string            `json:"run_id"`
	Status        string            `json:"status"`
	BudgetSeconds int               `json:"budget_seconds"`
	Transport     string            `json:"transport,omitempty"`
	Calls         []RunCallProgress `json:"calls"`
}

type RunCallProgress struct {
	ID               int64     `json:"id"`
	Purpose          string    `json:"purpose"`
	Recipient        string    `json:"recipient,omitempty"`
	Phase            string    `json:"phase"`
	StartedAt        time.Time `json:"started_at"`
	Reasoning        string    `json:"reasoning,omitempty"`
	ReasoningChars   int       `json:"reasoning_chars"`
	ReasoningLimited bool      `json:"reasoning_limited,omitempty"`
	OutputChars      int       `json:"output_chars"`
}

type runProgressState struct {
	mu        sync.Mutex
	calls     []runCallProgress
	bytes     int
	transport string
}

type runCallProgress struct {
	view     RunCallProgress
	thinking *strings.Builder
}

func (p *runProgressState) begin(id int64, scope turn.ContextScope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, runCallProgress{view: RunCallProgress{ID: id, Purpose: scope.Purpose, Recipient: scope.Recipient, Phase: "waiting", StartedAt: time.Now().UTC()}, thinking: &strings.Builder{}})
}

func (p *runProgressState) delta(id int64, delta model.TextDelta) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.calls {
		call := &p.calls[i].view
		if call.ID != id {
			continue
		}
		if delta.Reasoning != "" {
			call.Phase = "thinking"
			call.ReasoningChars += utf8.RuneCountInString(delta.Reasoning)
			text := delta.Reasoning
			room := runReasoningByteLimit - p.bytes
			if len(text) > room {
				text = text[:room]
				for !utf8.ValidString(text) {
					text = text[:len(text)-1]
				}
				call.ReasoningLimited = true
			}
			p.calls[i].thinking.WriteString(text)
			p.bytes += len(text)
		}
		if delta.Text != "" {
			call.Phase = "writing"
			call.OutputChars += utf8.RuneCountInString(delta.Text)
		}
		return
	}
}

func (p *runProgressState) finish(id int64, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.calls {
		if p.calls[i].view.ID == id {
			p.calls[i].view.Phase = "received"
			if err != nil {
				p.calls[i].view.Phase = "failed"
			}
			return
		}
	}
}

func (p *runProgressState) snapshot(includeThinking bool) []RunCallProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	calls := make([]RunCallProgress, len(p.calls))
	for i := range p.calls {
		calls[i] = p.calls[i].view
		if includeThinking {
			calls[i].Reasoning = p.calls[i].thinking.String()
		}
	}
	return calls
}

func (a *App) progressForRun(worldID, runID string) *runProgressState {
	a.runsMu.Lock()
	defer a.runsMu.Unlock()
	if runtime := a.runs[runID]; runtime != nil && runtime.WorldID == worldID {
		return &runtime.Progress
	}
	return nil
}

// ReadRunProgress applies the same ownership and run lookup as the run API.
// Raw provider reasoning is returned only for an explicit disclosure request.
func (a *App) ReadRunProgress(ctx context.Context, worldID, runID string, includeThinking bool) (RunProgress, error) {
	run, err := a.Run(ctx, worldID, runID)
	if err != nil {
		return RunProgress{}, err
	}
	progress := RunProgress{RunID: run.RunID, Status: run.Status, BudgetSeconds: int(turn.GenerationTimeBudget / time.Second), Calls: []RunCallProgress{}}
	if run.Status != "accepted" && run.Status != "running" {
		return progress, nil
	}
	if state := a.progressForRun(worldID, runID); state != nil {
		progress.Transport = state.transport
		progress.Calls = state.snapshot(includeThinking)
	}
	return progress, nil
}
