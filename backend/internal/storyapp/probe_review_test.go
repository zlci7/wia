package storyapp

import (
	"context"
	"testing"
)

// The review's counter-example: promotion after a real committed turn, which is when
// scene views already exist on disk.
func TestProbePromotionAfterCommittedTurn(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "提升回归", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	// A real turn commits scene views.
	run, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "probe-turn-1", Input: "我先看看客栈里的情况。"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, run.RunID); done.Status != "completed" {
		t.Fatalf("first turn: %+v", done)
	}
	before, err := a.ReadWorld(ctx, w.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.SceneViews) == 0 {
		t.Fatal("the first turn did not persist scene views, so this probe proves nothing")
	}
	t.Logf("scene views after turn 1: %d, characters: %d, scene_version: %d", len(before.SceneViews), len(before.Characters), len(before.SceneViews))

	target := before.Definition.BystanderRefs[0]
	promoted, err := a.PromoteCharacter(ctx, w.WorldID, PromotionRequest{
		RequestKey: "probe-promote", ExpectedContextEpoch: before.Summary.ContextEpoch, BystanderID: target.BystanderID,
		Draft: PromotionDraft{Role: "船夫", Profile: "在栈桥上等活的船夫。"},
	})
	if err != nil {
		t.Fatalf("promotion failed: %v", err)
	}
	t.Logf("promoted %s", promoted.EntityID)

	// Reading the world immediately is the first thing that can break.
	after, err := a.ReadWorld(ctx, w.WorldID, 5)
	if err != nil {
		t.Fatalf("world unreadable after promotion: %v", err)
	}
	t.Logf("scene views after promotion: %d, characters: %d", len(after.SceneViews), len(after.Characters))

	// Continuing the story is the second thing that can break.
	run2, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "probe-turn-2", Input: "我向船夫打招呼。"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, run2.RunID); done.Status != "completed" {
		t.Fatalf("turn after promotion: %+v", done)
	}
}
