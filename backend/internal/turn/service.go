package turn

import (
	"context"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	wiaworld "gameagent/backend/internal/world"
)

// Host is what a turn still needs from the application around it.
//
// Each method here is a piece of the turn that has not finished moving, and each one
// says in its own comment what has to happen before it can go. The list is meant to
// shrink to the logger alone; if it grows, the extraction has failed even though it
// compiles, because a wide Host is just the application hidden behind an interface.
//
// The rule for adding a method: it must name an operation a turn performs, not a
// capability of the application. Observing a turn and reading its frozen input are the
// two that legitimately stay; `GetStore` or `AppConfig` would be the application leaking
// in.
type Host interface {
	// LogStage records one stage of a turn: what it was for, which model version it used,
	// which events it read, who it resolved to, how many repairs it needed and how long
	// it took. It is the one member that may stay — observing a turn is the application's
	// business, not the turn's.
	LogStage(worldID string, run wiaworld.Run, stage Stage, purpose, actorID string, stageIndex int, promptVersion string, sourceEventIDs []string, resolvedAddressee string, repairCount int, elapsed time.Duration)

	// LoadInput reads the turn's frozen input: the world snapshot, the long-memory
	// material and the coordination evidence, all taken at one moment so every later
	// stage sees one world. It goes when the loader and the memory material it gathers
	// move: reading a turn's input means assembling memory, which is the memory module's
	// work first.
	LoadInput(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, generator model.TextGenerator, limit int) (Snapshot, error)
}

// Service runs one
//
// Execute is the whole of it, and it is meant to be read: the steps below are the story
// of a turn, in the order they happen. Everything else in this package is either a piece
// of the input those steps read or a helper they use.
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
	intent, output, err := s.resolveIntent(ctx, generator, snapshot, run)
	if err != nil {
		return Output{}, AtStage(StageIntent, err)
	}
	// The player's own words travel with the intent, because every later stage that
	// records what the player did needs them and the run is not passed that far down.
	intent.Input = run.Input
	// Who was present when the player acted is decided here and kept, because recording
	// what the player did must answer that question, not "who is here at the end". A
	// character who left during the turn still perceived the input, and one who arrived
	// afterwards did not; the closing roster would get both of those wrong.
	openingParticipants := CharacterIDs(InScene(snapshot.Characters))
	if err := s.runCharacters(ctx, generator, &snapshot, run, intent, &output); err != nil {
		return Output{}, AtStage(StageNPC, err)
	}
	notePlayerAction(&output, run, intent)
	if err := s.coordinate(ctx, generator, &snapshot, run, intent, &output); err != nil {
		return Output{}, AtStage(StageCoordination, err)
	}
	if err := s.narrate(ctx, generator, &snapshot, run, intent, &output); err != nil {
		return Output{}, AtStage(StageNarration, err)
	}
	recordPlayerExperience(&snapshot, &output, intent, openingParticipants, output.PlayerEventID)
	return output, nil
}

// load reads the turn's frozen input and records how long it took.
func (s *Service) load(ctx context.Context, store *storage.WorldStore, run wiaworld.Run, generator model.TextGenerator) (Snapshot, error) {
	started := time.Now()
	snapshot, err := s.host.LoadInput(ctx, store, run, generator, LoadSnapshotLimit)
	if err != nil {
		return Snapshot{}, AtStage(StageLoad, err)
	}
	s.host.LogStage(snapshot.Summary.WorldID, run, StageLoad, "load_snapshot", "", 0, "", nil, "", 0, time.Since(started))
	return snapshot, nil
}

// LoadSnapshotLimit is how many messages a turn reads. It is a turn concern: it decides
// how much recent conversation a character can see.
const LoadSnapshotLimit = 40
