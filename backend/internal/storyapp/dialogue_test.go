package storyapp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gameagent/backend/internal/storage"
	wiaworld "gameagent/backend/internal/world"
)

func TestDialogueUsesFourCommittedTurns(t *testing.T) {
	g := &scriptedGenerator{}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(context.Background(), "对话来源", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	var runs []wiaworld.Run
	for i := 0; i < 5; i++ {
		r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: fmt.Sprint(i), Input: fmt.Sprintf("老板，第%d次问候", i)})
		if err != nil {
			t.Fatal(err)
		}
		if got := waitRun(t, a, w.WorldID, r.RunID); got.Status != "completed" {
			t.Fatalf("%+v", got)
		}
		runs = append(runs, r)
	}
	g.mu.Lock()
	g.fail = true
	g.mu.Unlock()
	r, err := a.SubmitRun(context.Background(), w.WorldID, RunRequest{RequestKey: "failed", Input: "失败的原文不应成为经历"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitRun(t, a, w.WorldID, r.RunID); got.Status != "failed" {
		t.Fatalf("%+v", got)
	}
	path, _, err := a.worldRecord(context.Background(), w.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	items, err := store.LoadDialogue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, event := range items {
		seen[event.RunID] = true
		if strings.Contains(event.Content, "失败的原文") {
			t.Fatal("failed input admitted")
		}
	}
	if len(seen) != 4 || seen[runs[0].RunID] || seen[r.RunID] {
		t.Fatalf("runs=%v", seen)
	}
	for _, run := range runs[1:] {
		if !seen[run.RunID] {
			t.Fatal("committed dialogue missing")
		}
	}
}
