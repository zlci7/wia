package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/story"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type CreationScene struct {
	Narrative string           `json:"narrative"`
	Changes   *CreationChanges `json:"scene_changes,omitempty"`
	Notes     []CreationNote   `json:"continuity_notes,omitempty"`
}

type CreationChanges struct {
	ElapsedMinutes int                   `json:"elapsed_minutes,omitempty"`
	Positions      map[string]string     `json:"positions"`
	StateChanges   []CreationStateChange `json:"state_changes,omitempty"`
	ItemMoves      []CreationItemMove    `json:"item_moves,omitempty"`
}

// Kind preserves the distinction between an observation, testimony, a belief
// and a promise. Recipients are the people who actually acquired this record.
type CreationNote struct {
	Kind       string   `json:"kind"`
	Content    string   `json:"content"`
	Recipients []string `json:"recipients"`
	SpeakerID  string   `json:"speaker_id,omitempty"`
}

type CreationOptions struct {
	Input            string
	AllowPlotAdvance bool
	Reasoning        model.ReasoningMode
	Streaming        *bool
	OnDelta          func(model.TextDelta)
}

type CreationReport struct {
	CoreCalls         int      `json:"core_calls"`
	Repairs           int      `json:"repairs"`
	SelectedEntityIDs []string `json:"selected_entity_ids"`
}

type CreationResult struct {
	Scene  CreationScene  `json:"scene"`
	Report CreationReport `json:"report"`
}

type CreationStatus struct {
	Turn      int64             `json:"turn"`
	Clock     string            `json:"clock"`
	Positions map[string]string `json:"positions"`
}

type creationExchange struct {
	Input     string `json:"input"`
	Narrative string `json:"narrative"`
	RunID     string `json:"run_id"`
}

// CreationSession accepts complete scenes and their resource changes in memory.
type CreationSession struct {
	mu        sync.Mutex
	busy      bool
	cancel    context.CancelFunc
	snapshot  Snapshot
	exchanges []creationExchange
	sources   map[string][]memory.MemorySource
	turn      int64
}

var ErrCreationBusy = errors.New("creation session already generating")

func NewCreationSession(definition story.Definition) (*CreationSession, error) {
	// A session owns its definition, including all nested maps and slices.
	data, err := json.Marshal(definition)
	if err != nil {
		return nil, err
	}
	var def story.Definition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, err
	}
	if _, err := plot.ClockMinute(def.Clock); err != nil {
		return nil, err
	}
	snapshot := Snapshot{
		Definition: def, PlayerName: def.Summary.Player.Name, PlayerProfile: def.Summary.Player.Profile,
		Narrative: def.Settings,
		Summary:   wiaworld.WorldSummary{GameID: def.Summary.ID, WorldID: wire.NewID("play"), Clock: def.Clock, GameTitle: def.Summary.Title, Name: def.Summary.Title, Revision: def.Revision, Mode: def.Summary.Mode, Calendar: def.Calendar, Status: "playing"},
		Positions: clonePositions(def.InitialLocations), PositionSources: map[string]string{},
		Characters: slices.Clone(def.Characters),
	}
	for id := range snapshot.Positions {
		snapshot.PositionSources[id] = "definition:" + def.Revision
	}
	initializeCreationResources(&snapshot)
	if err := applySpatialProjection(&snapshot); err != nil {
		return nil, err
	}
	return &CreationSession{snapshot: snapshot, sources: map[string][]memory.MemorySource{}}, nil
}

func (s *CreationSession) Status() CreationStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return CreationStatus{Turn: s.turn, Clock: s.snapshot.Summary.Clock, Positions: clonePositions(s.snapshot.Positions)}
}

func (s *CreationSession) PersonalSources(owner string) []memory.MemorySource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sources[owner])
}

func (s *CreationSession) Interact(ctx context.Context, service *Service, generator model.TextGenerator, options CreationOptions) (CreationResult, error) {
	if service == nil || generator == nil || strings.TrimSpace(options.Input) == "" || !utf8.ValidString(options.Input) ||
		utf8.RuneCountInString(options.Input) > 4000 || !options.Reasoning.Valid() {
		return CreationResult{}, model.ErrInvalidTextRequest
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return CreationResult{}, ErrCreationBusy
	}
	s.busy = true
	callCtx, cancel := context.WithTimeout(ctx, GenerationTimeBudget)
	s.cancel = cancel
	// While busy, the accepted state stays immutable. Getters can still inspect it.
	snapshot := s.snapshot
	material, selected := s.creationMaterial(options)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.cancel = nil
		s.mu.Unlock()
	}()
	defer cancel()
	snapshot.sceneEntities = selected
	snapshot.materialReads = newMaterialReadBudget()
	run := wiaworld.Run{RunID: wire.NewID("creation"), Attempt: 1}
	call := NewContextGenerator(service.deps, service.deps.Owner, generator, material, snapshot, run, "co_creation", "player", 0, "co-creation-v1").(*ContextGenerator)
	call.composer.Scope.SelectedEntityIDs = slices.Clone(selected)
	result := CreationResult{Report: CreationReport{SelectedEntityIDs: slices.Clone(selected)}}
	outputTokens := 4096
	if call.composer.Window.OutputTokens > 0 {
		outputTokens = min(outputTokens, call.composer.Window.OutputTokens)
	}
	var scene CreationScene
	var positions map[string]string
	var clock string
	var resources Snapshot
	for attempt := 0; attempt < 2; attempt++ {
		if err := callCtx.Err(); err != nil {
			return result, err
		}
		response, err := call.GenerateText(callCtx, model.TextRequest{
			System: material.System, MaxOutputTokens: outputTokens, Reasoning: options.Reasoning, JSON: true,
			Streaming: options.Streaming, OnDelta: options.OnDelta,
		})
		result.Report.CoreCalls = call.calls
		if err != nil {
			return result, err
		}
		if err := callCtx.Err(); err != nil {
			return result, err
		}
		if err := model.ValidateTextResponse(model.TextRequest{MaxOutputTokens: outputTokens, MaxResponseBytes: 1 << 20}, response); err != nil {
			return result, err
		}
		if response.Diagnostic.FinishReason != "" && response.Diagnostic.FinishReason != "stop" {
			return result, model.ErrInvalidTextResponse
		}
		err = DecodeGeneratedJSON(response.Text, &scene, nil, []string{"narrative", "scene_changes"})
		if err == nil {
			positions, clock, err = validateCreationScene(snapshot, selected, scene)
		}
		if err == nil {
			resources, err = applyCreationResources(snapshot, selected, positions, scene.Changes, run.RunID)
		}
		call.recordJSONValidation(err)
		if err == nil {
			break
		}
		if attempt == 1 {
			return result, err
		}
		result.Report.Repairs++
		var detail *GenerationError
		correction := "重新生成满足合同的完整JSON。"
		if errors.As(err, &detail) {
			correction += fmt.Sprintf("本地校验 code=%s field=%s expected=%s。", detail.Code, detail.Field, detail.Expected)
		}
		call.material = appendRequiredMaterial(call.material, Section{Name: "creation_correction", Text: correction})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := callCtx.Err(); err != nil {
		return result, err
	}
	next := resources
	next.Summary.TurnSeq = s.turn + 1
	next.Characters = slices.Clone(snapshot.Characters)
	next.Bystanders, next.BystanderRefs = nil, nil
	next.Positions, next.Summary.Clock = positions, clock
	next.PositionSources = clonePositions(snapshot.PositionSources)
	for id, location := range positions {
		if location != snapshot.Positions[id] {
			next.PositionSources[id] = run.RunID
		}
	}
	if err := applySpatialProjection(&next); err != nil {
		return result, err
	}
	s.snapshot = next
	s.turn++
	s.exchanges = append(s.exchanges, creationExchange{options.Input, scene.Narrative, run.RunID})
	s.recordCreation("player", run.RunID, "player", "input", options.Input)
	s.recordCreation("player", run.RunID, "", "narrative", scene.Narrative)
	for _, note := range scene.Notes {
		for _, owner := range note.Recipients {
			s.recordCreation(owner, run.RunID, note.SpeakerID, note.Kind, note.Content)
		}
		if note.SpeakerID != "" && !slices.Contains(note.Recipients, note.SpeakerID) {
			s.recordCreation(note.SpeakerID, run.RunID, note.SpeakerID, note.Kind, note.Content)
		}
	}
	result.Scene = scene
	return result, nil
}

func (s *CreationSession) recordCreation(owner, runID, actor, kind, content string) {
	seq := int64(len(s.sources[owner])) + 1
	s.sources[owner] = append(s.sources[owner], memory.MemorySource{
		Scope: owner, Seq: seq, ID: fmt.Sprintf("%s:%s:%d", runID, owner, seq), RunID: runID,
		Actor: actor, Kind: kind, Content: content, CreatedAt: s.snapshot.Summary.Clock,
	})
}

func validateCreationScene(snapshot Snapshot, selected []string, scene CreationScene) (map[string]string, string, error) {
	bad := func(field, expected string) (map[string]string, string, error) {
		return nil, "", coordinationInvalid("creation_contract_invalid", field, expected)
	}
	if strings.TrimSpace(scene.Narrative) == "" || utf8.RuneCountInString(scene.Narrative) > 6000 {
		return bad("narrative", "non-empty-string:1..6000-runes")
	}
	positions := clonePositions(snapshot.Positions)
	if scene.Changes == nil || len(scene.Changes.Positions) == 0 || len(scene.Changes.Positions) > len(positions) {
		return bad("scene_changes.positions", "ending-position-for-every-selected-entity")
	}
	elapsed := scene.Changes.ElapsedMinutes
	if elapsed < 0 || elapsed > 120 {
		return bad("scene_changes.elapsed_minutes", "integer:0..120")
	}
	graph := story.PlaceGraph(snapshot.Definition)
	for _, id := range selected {
		if _, ok := scene.Changes.Positions[id]; !ok {
			return bad("scene_changes.positions", "ending-position-for-every-selected-entity")
		}
	}
	for id, destination := range scene.Changes.Positions {
		origin, known := positions[id]
		if _, ok := graph[destination]; !known || !ok || destination != origin && !slices.Contains(selected, id) {
			return bad("scene_changes.positions", "known-place;selected-entity-for-movement")
		}
		if origin != destination && !creationReachable(origin, destination, graph) {
			return bad("scene_changes.positions", "destination-reachable-from-current-position")
		}
		positions[id] = destination
	}
	clock, err := plot.AdvanceClock(snapshot.Summary.Clock, elapsed)
	if err != nil {
		return bad("scene_changes.elapsed_minutes", "supported-world-date")
	}
	if len(scene.Notes) > 12 {
		return bad("continuity_notes", "array:0..12")
	}
	for _, note := range scene.Notes {
		if !slices.Contains([]string{"observed", "statement", "hypothesis", "commitment"}, note.Kind) {
			return bad("continuity_notes.kind", "observed|statement|hypothesis|commitment")
		}
		if strings.TrimSpace(note.Content) == "" || utf8.RuneCountInString(note.Content) > 800 || len(note.Recipients) == 0 || len(note.Recipients) > len(selected) {
			return bad("continuity_notes", "content:1..800-runes;actual-recipients")
		}
		if note.Kind == "statement" && note.SpeakerID == "" || note.SpeakerID != "" && !slices.Contains(selected, note.SpeakerID) {
			return bad("continuity_notes.speaker_id", "selected-speaker;required-for-statement")
		}
		seen := map[string]bool{}
		for _, id := range note.Recipients {
			if !slices.Contains(selected, id) || seen[id] {
				return bad("continuity_notes.recipients", "unique-selected-entity-ids")
			}
			seen[id] = true
		}
	}
	return positions, clock, nil
}

// This prototype accepts ending positions, without claiming intermediate action
// settlement. A route is checked against the existing directed place graph.
func creationReachable(from, to string, graph map[string][]string) bool {
	paths := [][]string{{from}}
	visited := map[string]bool{from: true}
	for len(paths) > 0 {
		path := paths[0]
		paths = paths[1:]
		if len(path) >= 16 {
			continue
		}
		for _, next := range graph[path[len(path)-1]] {
			if visited[next] {
				continue
			}
			visited[next] = true
			route := append(slices.Clone(path), next)
			if next == to {
				return wiaworld.ValidateRoute(route, from, to, graph) == nil
			}
			paths = append(paths, route)
		}
	}
	return false
}
