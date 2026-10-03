package turn

import (
	"context"
	"slices"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// Host observes stage completion. The turn owns all stage implementations.
type Host interface {
	// LogStage records one stage of a turn: what it was for, which model version it used,
	// which events it read, who it resolved to, how many repairs it needed and how long
	// it took. It is the one member that may stay — observing a turn is the application's
	// business, not the turn's.
	LogStage(worldID string, run wiaworld.Run, stage Stage, purpose, actorID string, stageIndex int, promptVersion string, sourceEventIDs []string, resolvedAddressee string, repairCount int, elapsed time.Duration)
}

// Service generates one turn's Output. The application validates the world version
// and commits that output in a separate transaction after Execute succeeds.
type Service struct {
	host Host
	deps Deps
}

// New builds a turn service on the given host. deps carries what a turn needs beyond the
// host operations: usage metering and a logger. It is not part of Host because neither is
// an operation a turn performs.
func New(host Host, deps Deps) *Service { return &Service{host: host, deps: deps} }

// Execute runs one turn and returns what it produced.
//
// The order is not an implementation detail, it is the contract: nothing is narrated
// before the world has been advanced, and no result is produced before the scene has
// been resolved. A stage that fails stops the turn and reports which stage it was, so a
// caller can tell a model failure from a storage one.
func (s *Service) Execute(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, generator model.TextGenerator) (Output, error) {
	snapshot, err := s.load(ctx, store, run, generator)
	if err != nil {
		return Output{}, err
	}
	if run.InputID != "" {
		prepared, found, readErr := store.ReadActionResolutionForInput(ctx, run.InputID)
		if readErr != nil {
			return Output{}, AtStage(StageLoad, readErr)
		}
		if found {
			run.PreparedActionRuleID = prepared.RuleID
		}
	}
	return s.executeSnapshotWithStore(ctx, store, generator, snapshot, run)
}

func (s *Service) executeSnapshot(ctx context.Context, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run) (Output, error) {
	return s.executeSnapshotWithStore(ctx, nil, generator, snapshot, run)
}

func (s *Service) executeSnapshotWithStore(ctx context.Context, store *storage.WorldStore, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run) (Output, error) {
	snapshot.materialReads = newMaterialReadBudget()
	intent, err := s.resolveIntentStage(ctx, generator, snapshot, run)
	if err != nil {
		return Output{}, AtStage(StageIntent, err)
	}
	// The player's own words travel with the intent, because every later stage that
	// records what the player did needs them and the run is not passed that far down.
	intent.Input = run.Input
	return s.executeInput(ctx, store, generator, snapshot, run, intent)
}

// load reads the turn's frozen input and records how long it took.
func (s *Service) load(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, generator model.TextGenerator) (Snapshot, error) {
	started := time.Now()
	snapshot, err := s.loadInput(ctx, store, run, generator, LoadSnapshotLimit)
	if err != nil {
		return Snapshot{}, AtStage(StageLoad, err)
	}
	s.host.LogStage(snapshot.Summary.WorldID, run, StageLoad, "load_snapshot", "", 0, "", nil, "", 0, time.Since(started))
	return snapshot, nil
}

// LoadSnapshotLimit is how many messages a turn reads. It is a turn concern: it decides
// how much recent conversation a character can see.
const LoadSnapshotLimit = 40

func (s *Service) executeInput(ctx context.Context, store *storage.WorldStore, generator model.TextGenerator, snapshot Snapshot, run wiaworld.Run, intent TurnIntent) (Output, error) {
	parts := intent.Fragments
	if len(parts) == 0 {
		parts = []InputFragment{{Text: run.Input, ActorID: "player", IntentType: intent.IntentType, AddresseeID: intent.AddresseeID, Visibility: intent.Visibility, WaitMinutes: intent.WaitMinutes, ActionRuleID: intent.ActionRuleID}}
	}
	openingClock := snapshot.Summary.Clock
	var output Output
	var lastHost hostResult
	stageByEvent := map[string]int{}
	if len(parts) > 1 {
		output.Events = []wiaworld.Event{{EventID: run.RunID + ":input", EventType: "player_input", ActorID: "player", Content: run.Input, RunID: run.RunID, SourceType: "author_input", SceneVersion: snapshot.SceneVersion, CreatedAt: time.Now().UTC()}}
	}
	for i, part := range parts {
		partRun := run
		partRun.Input = part.Text
		if len(parts) > 1 {
			partRun.InputPart = i + 1
		}
		partIntent := TurnIntent{IntentType: part.IntentType, AddresseeID: part.AddresseeID, Visibility: part.Visibility, WaitMinutes: part.WaitMinutes, ActionRuleID: part.ActionRuleID, Input: part.Text}
		participants := InScene(snapshot.Characters)
		reachable := true
		if part.AddresseeID != "" {
			if _, ok := FindSceneCharacter(snapshot.Characters, part.AddresseeID); !ok {
				if i == 0 {
					return Output{}, AtStage(StageIntent, ErrInvalidRequest)
				}
				reachable, participants = false, nil
			}
		}
		local := openOutput(&snapshot, partIntent, partRun, participants)
		local.StateChanges = slices.Clone(output.StateChanges)
		local.RelationshipChanges = slices.Clone(output.RelationshipChanges)
		local.ItemTransfers = slices.Clone(output.ItemTransfers)
		local.elapsedMinutes = output.elapsedMinutes
		if len(parts) > 1 {
			segment := wiaworld.Event{EventID: inputPrefix(partRun) + ":segment", EventType: "player_input_segment", ActorID: "player", Content: wire.MarshalJSON(part), RunID: run.RunID, Stage: 1, SourceType: "author_input_segment", ProjectionParentID: run.RunID + ":input", SceneVersion: snapshot.SceneVersion, CreatedAt: time.Now().UTC()}
			local.Events = append([]wiaworld.Event{segment}, local.Events...)
			local.Events[1].ProjectionParentID = segment.EventID
		}
		if !reachable {
			notePlayerAction(&local, partRun, partIntent)
			reason := CharacterDisplayName(snapshot.Characters, part.AddresseeID) + "当前不在可接触范围内，这一段未执行。"
			visible, err := appendHostOutcomes(&local, partRun, snapshot.Characters, snapshot.Definition.BystanderRefs, []hostActionResult{{ActionID: inputPrefix(partRun) + ":player-action", Status: "not_executed", Content: reason, Recipients: []string{"player"}, Projections: []actionProjection{{Recipient: "player", Content: reason}}}})
			if err != nil {
				return Output{}, AtStage(StageCoordination, err)
			}
			local.VisibleEvents = visible
		} else {
			if part.ActionRuleID != "" {
				resolution, err := prepareActionResolution(ctx, store, snapshot, partRun, part.ActionRuleID)
				if err != nil {
					return Output{}, AtStage(StageIntent, err)
				}
				local.ActionResolution = &resolution
			}
			openingParticipants := CharacterIDs(InScene(snapshot.Characters))
			if err := s.runCharacters(ctx, generator, &snapshot, partRun, partIntent, &local); err != nil {
				return Output{}, AtStage(StageNPC, err)
			}
			notePlayerAction(&local, partRun, partIntent)
			host, visible, err := s.coordinateStage(ctx, generator, &snapshot, partRun, partIntent, part.AddresseeID, &local)
			if err != nil {
				return Output{}, AtStage(StageCoordination, err)
			}
			local.VisibleEvents = visible
			recordPlayerExperience(&snapshot, &local, partIntent, openingParticipants, local.PlayerEventID)
			lastHost = host
		}
		for _, e := range local.Events {
			stageByEvent[e.EventID] = i*3 + e.Stage
		}
		mergeInputOutput(&output, local)
		snapshot.Summary.Clock = local.Clock
		snapshot.elapsedMinutes = output.elapsedMinutes
		mergeConfirmedInput(&snapshot, local)
	}
	// World progression and narration run once, after the ordered workspace is settled.
	start := len(output.Events)
	snapshot.Summary.Clock = openingClock
	lastHost.TimeMinutes = output.elapsedMinutes
	worldRun := run
	if len(parts) > 1 {
		worldRun.InputPart = len(parts)
	}
	if err := s.resolveSceneResult(ctx, generator, &snapshot, worldRun, lastHost, &output); err != nil {
		return Output{}, AtStage(StageCoordination, err)
	}
	for _, e := range output.Events[start:] {
		stageByEvent[e.EventID] = (len(parts)-1)*3 + e.Stage
	}
	for i := range output.VisibleEvents {
		if stage, ok := stageByEvent[output.VisibleEvents[i].EventID]; ok {
			output.VisibleEvents[i].Stage = stage
		}
	}
	if err := s.narrate(ctx, generator, &snapshot, worldRun, intent, &output); err != nil {
		return Output{}, AtStage(StageNarration, err)
	}
	for i := range output.Events {
		if stage, ok := stageByEvent[output.Events[i].EventID]; ok {
			output.Events[i].Stage = stage
		} else if output.Events[i].EventType == "turn_settled" {
			output.Events[i].Stage += (len(parts) - 1) * 3
		}
	}
	for i := range output.Perceptions {
		if stage, ok := stageByEvent[output.Perceptions[i].SourceEventID]; ok {
			output.Perceptions[i].Stage = stage
		}
	}
	return output, nil
}
