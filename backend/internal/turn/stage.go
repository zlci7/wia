package turn

import "errors"

// The stage vocabulary names the pipeline positions a failure can be attributed to.
// A turn reports which stage failed rather than a bare error, so the caller can say
// whether the model, the storage or the intent step was responsible.
type Stage string

const (
	StageLoad         Stage = "load_world"
	StageIntent       Stage = "intent"
	StageNPC          Stage = "npc"
	StageCoordination Stage = "coordination"
	StageNarration    Stage = "narration"
	StageCommit       Stage = "commit"
)

type StageError struct {
	Stage Stage
	Err   error
}

func (e *StageError) Error() string { return string(e.Stage) + ": " + e.Err.Error() }
func (e *StageError) Unwrap() error { return e.Err }

// AtStage attributes an error to the stage that produced it.
func AtStage(stage Stage, err error) error {
	if err == nil {
		return nil
	}
	return &StageError{Stage: stage, Err: err}
}

// StageOf reports which stage an error came from, or "unknown" when it carries no
// stage attribution.
func StageOf(err error) string {
	var staged *StageError
	if errors.As(err, &staged) {
		return string(staged.Stage)
	}
	return "unknown"
}
