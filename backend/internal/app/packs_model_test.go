package app

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
)

// Uses isolated worlds and reads, but never modifies, the supplied model config.
func TestPackRealModel(t *testing.T) {
	path := os.Getenv("WIA_PACK_MODEL_CONFIG")
	if path == "" {
		t.Skip("opt-in real model")
	}
	provider, config, err := llm.NewProviderFromConfigFile(path)
	if err != nil || config.Provider == "fake" {
		t.Fatal("real provider unavailable")
	}
	generator, ok := provider.(model.TextGenerator)
	if !ok {
		t.Fatal("text generation unavailable")
	}
	t.Logf("provider=%s model=%s", config.Provider, config.Model)
	ctx := context.Background()
	root := t.TempDir()
	app, err := Open(ctx, Options{DataRoot: root, Generator: generator})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { app.Close() }()
	for _, gameID := range []string{"lantern-dusk", "orbital-repair"} {
		game, _ := app.Game(gameID)
		world, e := app.CreateStoryWorld(ctx, CreateWorldRequest{GameID: gameID, ExpectedRevision: game.Revision, RequestKey: gameID, Activate: true})
		if e != nil {
			t.Fatal(e)
		}
		inputs := []string{"跟老板打声招呼，说我只是来歇脚的。", "我看看窗外，然后问老板现在有没有空房。"}
		if gameID == "orbital-repair" {
			inputs = []string{"向周宁打招呼，问她今天值班有哪些需要先了解的事情。", "向许遥问问补给船几点到，我只是先了解情况。"}
		}
		for i, input := range inputs {
			if gameID == "orbital-repair" && i == 1 {
				app.Close()
				app, e = Open(ctx, Options{DataRoot: root, StoryPacksPath: t.TempDir(), Generator: generator})
				if e != nil {
					t.Fatal(e)
				}
			}
			start := time.Now()
			run, e := app.SubmitRun(ctx, world.WorldID, RunRequest{Input: input, RequestKey: fmt.Sprintf("live-%s-%d", gameID, i)})
			if e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(310 * time.Second)
			for time.Now().Before(deadline) {
				run, e = app.Run(ctx, world.WorldID, run.RunID)
				if e != nil {
					t.Fatal(e)
				}
				if run.Status != "accepted" && run.Status != "running" {
					break
				}
				time.Sleep(150 * time.Millisecond)
			}
			t.Logf("game=%s turn=%d status=%s elapsed_ms=%d reason=%s", gameID, i+1, run.Status, time.Since(start).Milliseconds(), run.Reason)
			if run.Status != "completed" {
				t.Fatalf("run failed: %s", run.Reason)
			}
			snapshot := readContextSnapshot(t, app, world.WorldID)
			if len(snapshot.Messages) > 0 {
				t.Log(snapshot.Messages[len(snapshot.Messages)-1].Content)
			}
		}
	}
}
