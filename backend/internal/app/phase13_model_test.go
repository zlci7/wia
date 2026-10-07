package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	wiaworld "gameagent/backend/internal/world"
)

type phase13Call struct {
	Request        model.TextRequest
	Diagnostic     model.TextDiagnostic
	Response       string
	ElapsedMS      int64
	ErrorCode      string
	InputTokens    int
	FirstDeltaMS   int64
	ReasoningChars int
	OutputChars    int
}

type phase13Probe struct {
	inner model.TextGenerator
	dir   string
	mu    sync.Mutex
	calls []phase13Call
	err   error
}

func (p *phase13Probe) ModelWindow() model.WindowLimits {
	if g, ok := p.inner.(model.WindowProvider); ok {
		return g.ModelWindow()
	}
	return model.WindowLimits{}
}

func (p *phase13Probe) TextReasoningReserve() int {
	if g, ok := p.inner.(model.TextReasoningProvider); ok {
		return g.TextReasoningReserve()
	}
	return 0
}

func (p *phase13Probe) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return model.TextResponse{}, p.err
	}
	if len(p.calls) >= 40 {
		return model.TextResponse{}, errors.New("live evaluation request budget exhausted")
	}
	started := time.Now()
	firstDelta := int64(0)
	reasoningChars, outputChars := 0, 0
	if os.Getenv("WIA_PHASE13_STREAM") == "1" {
		observer := req.OnDelta
		req.OnDelta = func(delta model.TextDelta) {
			if firstDelta == 0 {
				firstDelta = max(1, time.Since(started).Milliseconds())
			}
			reasoningChars += utf8.RuneCountInString(delta.Reasoning)
			outputChars += utf8.RuneCountInString(delta.Text)
			if observer != nil {
				observer(delta)
			}
		}
	}
	response, err := p.inner.GenerateText(ctx, req)
	call := phase13Call{Request: req, Diagnostic: response.Diagnostic, Response: response.Text, ElapsedMS: time.Since(started).Milliseconds(), ErrorCode: model.TextErrorCode(err), InputTokens: model.FramedTextInputTokens(req), FirstDeltaMS: firstDelta, ReasoningChars: reasoningChars, OutputChars: outputChars}
	var failure *model.TextCallError
	if errors.As(err, &failure) {
		call.Diagnostic = failure.Diagnostic
	}
	p.calls = append(p.calls, call)
	// Only fictional requests and responses are persisted; provider configuration
	// and transport errors are never included in evidence.
	if writeErr := phase13Write(filepath.Join(p.dir, fmt.Sprintf("call-%03d.json", len(p.calls))), call); writeErr != nil {
		p.err = writeErr
		return model.TextResponse{}, writeErr
	}
	return response, err
}

type phase13Round struct {
	Index     int
	Input     string
	ElapsedMS int64
	Calls     int
	Run       wiaworld.Run
	Report    turn.SceneAttemptReport
	Error     string
	Before    turn.Snapshot
	After     turn.Snapshot
	Unchanged bool
}

type phase13Evidence struct {
	Provider string
	Model    string
	Path     string
	Case     string
	WorldID  string
	Rounds   []phase13Round
}

func phase13Write(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// TestPhase13RealSequence is explicitly opt-in. Its data root belongs to the
// evidence directory and can be resumed without opening any player's saves.
func TestPhase13RealSequence(t *testing.T) {
	configPath := os.Getenv("WIA_PHASE13_MODEL_CONFIG")
	if configPath == "" {
		t.Skip("set WIA_PHASE13_MODEL_CONFIG and WIA_PHASE13_EVIDENCE_DIR for isolated real-model evaluation")
	}
	evidenceDir := os.Getenv("WIA_PHASE13_EVIDENCE_DIR")
	if evidenceDir == "" {
		t.Fatal("an isolated evidence directory is required")
	}
	provider, config, err := llm.NewProviderFromConfigFile(configPath)
	if err != nil {
		t.Fatal("real model configuration unavailable")
	}
	generator, ok := provider.(model.TextGenerator)
	if !ok || config.Provider == "fake" {
		t.Fatal("real text generator required")
	}
	path := os.Getenv("WIA_PHASE13_PATH")
	if path != "candidate" && path != "formal" {
		t.Fatal("WIA_PHASE13_PATH must select candidate or formal")
	}
	scenario := os.Getenv("WIA_PHASE13_SCENARIO")
	inputs := []string{
		"我先不急着答应，问马丁刚才停顿的那次最后见面究竟发生了什么：诺拉那天说了什么、去了哪里，近来有无反常。",
		"我想请马丁同意我先去裁缝店看看诺拉的房间和留下的东西，找出那单活计的账目或线索，好确认她打算接的是什么顾客。",
		"我拿起记事本，把账目上不同字迹的情况记下，再问马丁这些字迹分别是谁写的，这说明什么问题；对于不确定的地方先保留推测。",
	}
	if scenario == "ensemble" || scenario == "privacy" {
		inputs = []string{
			"我请沈岚和铁杉一起说说今晚各自最担心什么，想请铁杉帮忙查看码头，但不替他答应；如果两人意见不一样，就当面讨论。",
			"我靠近沈岚耳语，只让她听见：我们约定的口令是青石，稍后我会再来核对，请不要公开复述。",
			"我恢复正常音量，问铁杉刚才看见了什么、是否愿意帮忙，再请沈岚只谈她愿意公开的事。",
		}
		if scenario == "privacy" {
			inputs = inputs[1:]
		}
	} else if scenario != "investigation" {
		t.Fatal("select investigation, ensemble or privacy")
	}
	limit := len(inputs)
	if value := os.Getenv("WIA_PHASE13_STEPS"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > len(inputs) {
			t.Fatal("invalid step limit")
		}
	}
	dir := filepath.Join(evidenceDir, path, scenario)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	callDir := filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.Mkdir(callDir, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := &phase13Probe{inner: generator, dir: callDir}
	root := filepath.Join(dir, "data")
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		installTestPacks(t, root)
	}
	a, err := Open(t.Context(), Options{DataRoot: root, UserID: LocalUserID, Generator: probe})
	if err != nil {
		t.Fatal("isolated application could not open")
	}
	defer a.Close()
	evidence := phase13Evidence{Provider: config.Provider, Model: config.Model, Path: path, Case: scenario}
	evidencePath := filepath.Join(dir, "rounds.json")
	if data, err := os.ReadFile(evidencePath); err == nil {
		if err := json.Unmarshal(data, &evidence); err != nil {
			t.Fatal("invalid prior evidence")
		}
	}
	if evidence.WorldID == "" {
		var world wiaworld.WorldSummary
		if scenario == "investigation" {
			world = createPackWorld(t, a, "mist-embers")
		} else {
			world, err = a.createFixtureWorld(t.Context(), "多人私聊验证", "open", "旅人", "谨慎而礼貌的旅人", true)
			if err != nil {
				t.Fatal(err)
			}
		}
		evidence.WorldID = world.WorldID
	}
	completed := 0
	for _, round := range evidence.Rounds {
		if round.Run.Status == "completed" {
			completed++
		}
	}
	if len(evidence.Rounds) > completed && os.Getenv("WIA_PHASE13_RETRY") != "1" {
		t.Fatal("previous failure retained; set WIA_PHASE13_RETRY=1 after local investigation for the one technical retry")
	}
	for i := completed; i < limit; i++ {
		failures := 0
		previousRunID := ""
		for _, round := range evidence.Rounds {
			if round.Index == i+1 && round.Run.Status != "completed" {
				failures++
				previousRunID = round.Run.RunID
			}
		}
		if failures >= 2 {
			t.Fatal("technical retry allowance exhausted")
		}
		before := readContextSnapshot(t, a, evidence.WorldID)
		started := time.Now()
		probe.mu.Lock()
		callsBefore := len(probe.calls)
		probe.mu.Unlock()
		round := phase13Round{Index: i + 1, Input: inputs[i], Before: before}
		if path == "candidate" {
			worldPath, _, err := a.worldRecord(t.Context(), evidence.WorldID)
			if err != nil {
				t.Fatal(err)
			}
			store, err := storage.OpenWorldDB(worldPath)
			if err != nil {
				t.Fatal(err)
			}
			request := RunRequest{RequestKey: fmt.Sprintf("live-%d-%d", i+1, failures), Input: inputs[i]}
			attempt := 1
			if previousRunID != "" {
				previous, found, err := store.ReadRun(t.Context(), previousRunID)
				if err != nil || !found || previous.BaseContextEpoch != before.Summary.ContextEpoch || previous.BaseMessageHead != before.Summary.MessageHead || previous.BaseEventHead != before.Summary.EventHead || previous.BaseTurnSeq != before.Summary.TurnSeq || previous.BaseSceneVersion != before.SceneVersion {
					t.Fatal("retry world no longer matches the failed input")
				}
				request.inputID, request.inputSeq = previous.InputID, previous.InputSeq
				attempt = previous.Attempt + 1
			}
			run, err := a.submitRunTx(t.Context(), store, before, request, attempt, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateRunStatus(t.Context(), run.RunID, "running", "", ""); err != nil {
				t.Fatal(err)
			}
			out, report, generationErr := a.turnService().BuildSceneCandidate(t.Context(), store, run, probe)
			round.Report = report
			if generationErr == nil {
				generationErr = commitSceneCandidate(t, store, run, out)
			}
			if generationErr != nil {
				status, reason, message := classifyTurnFailure(generationErr)
				if err := store.UpdateRunStatus(t.Context(), run.RunID, status, reason, message); err != nil {
					t.Fatal(err)
				}
				round.Error = phase13Failure(generationErr)
			}
			round.Run, _, err = store.ReadRun(t.Context(), run.RunID)
			store.Close()
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var run wiaworld.Run
			key := fmt.Sprintf("live-%d-%d", i+1, failures)
			if previousRunID == "" {
				run, err = a.SubmitRun(t.Context(), evidence.WorldID, RunRequest{RequestKey: key, Input: inputs[i]})
			} else {
				run, err = a.RetryRun(t.Context(), evidence.WorldID, previousRunID, key)
			}
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(310 * time.Second)
			for time.Now().Before(deadline) {
				run, err = a.Run(t.Context(), evidence.WorldID, run.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if run.Status != "accepted" && run.Status != "running" {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			round.Run = run
		}
		round.ElapsedMS = time.Since(started).Milliseconds()
		probe.mu.Lock()
		round.Calls = len(probe.calls) - callsBefore
		probe.mu.Unlock()
		round.After = readContextSnapshot(t, a, evidence.WorldID)
		round.Unchanged = phase13WorldUnchanged(before, round.After)
		evidence.Rounds = append(evidence.Rounds, round)
		if err := phase13Write(evidencePath, evidence); err != nil {
			t.Fatal(err)
		}
		t.Logf("case=%s path=%s turn=%d status=%s reason=%s calls=%d elapsed_ms=%d error=%s", scenario, path, i+1, round.Run.Status, round.Run.Reason, round.Calls, round.ElapsedMS, round.Error)
		if round.Run.Status != "completed" {
			if !round.Unchanged {
				t.Fatal("failed turn changed the committed world")
			}
			t.Fatal("real turn failed; retained evidence")
		}
	}
}

func phase13WorldUnchanged(before, after turn.Snapshot) bool {
	return before.Summary.Clock == after.Summary.Clock && before.Summary.TurnSeq == after.Summary.TurnSeq && before.Summary.MessageHead == after.Summary.MessageHead && before.Summary.EventHead == after.Summary.EventHead && before.Summary.ContextEpoch == after.Summary.ContextEpoch && before.SceneVersion == after.SceneVersion && reflect.DeepEqual(before.Positions, after.Positions) && reflect.DeepEqual(before.States, after.States) && reflect.DeepEqual(before.Items, after.Items) && reflect.DeepEqual(before.Relationships, after.Relationships) && reflect.DeepEqual(before.OpenProgress, after.OpenProgress)
}

func phase13Failure(err error) string {
	var invalid *turn.GenerationError
	if errors.As(err, &invalid) {
		return strings.Join([]string{invalid.Code, invalid.Field, invalid.Expected}, ":")
	}
	if code := model.TextErrorCode(err); code != "" {
		return code
	}
	return "local_validation_or_storage_failure"
}
