package turn

import (
	"context"
	"errors"
	"testing"
	"time"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type deadlineSceneGenerator struct {
	deadlines []time.Time
	wait      bool
	draft     *SceneDraft
}

func (g *deadlineSceneGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return model.TextResponse{}, errors.New("generation has no deadline")
	}
	g.deadlines = append(g.deadlines, deadline)
	if g.wait {
		<-ctx.Done()
		return model.TextResponse{}, ctx.Err()
	}
	if len(g.deadlines) == 1 {
		return model.TextResponse{Text: "{"}, nil
	}
	return model.TextResponse{Text: wire.MarshalJSON(g.draft)}, nil
}

func TestSceneCorrectionSharesFiveMinuteDeadline(t *testing.T) {
	run := wiaworld.Run{RunID: "budget", Input: "我问甲。"}
	g := &deadlineSceneGenerator{draft: sceneComplete(run.Input, sceneDialogue("b1", "npc:a", refusalSpeech, "input:0"))}
	_, report, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(t.Context(), nil, g, sceneContextFixture(), run)
	if err != nil || report.Repairs != 1 || len(g.deadlines) != 2 {
		t.Fatalf("correction: %+v %v", report, err)
	}
	if remaining := time.Until(g.deadlines[0]); remaining < 295*time.Second || remaining > 300*time.Second {
		t.Fatalf("remaining budget: %s", remaining)
	}
	if !g.deadlines[0].Equal(g.deadlines[1]) {
		t.Fatal("correction restarted the deadline")
	}
}

func TestSceneRespectsShorterCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	g := &deadlineSceneGenerator{wait: true}
	start := time.Now()
	out, _, err := New(coordinationTestHost{}, Deps{}).generateSceneCandidate(ctx, nil, g, sceneContextFixture(), wiaworld.Run{RunID: "short", Input: "我问甲。"})
	if !errors.Is(err, context.DeadlineExceeded) || out.Narrative != "" || time.Since(start) > time.Second {
		t.Fatalf("caller deadline: output=%+v err=%v", out, err)
	}
}
