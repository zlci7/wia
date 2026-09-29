package storyapp

import (
	"context"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
)

func TestPlotRootCorrectionInvalidatesScopedProjections(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &plotTestGenerator{wake: true})
	w, err := a.createFixtureWorld(ctx, "投影纠正", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "plot", Input: "等待"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, w.WorldID, r.RunID); done.Status != "completed" {
		t.Fatal(done)
	}
	before := readContextSnapshot(t, a, w.WorldID)
	root := "plot:" + before.Plot.Revision + ":" + before.Plot.Nodes[0].ID
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = loadLongMemory(ctx, store, &before); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"player", "npc:mercenary"} {
		m := before.LongMemory[scope]
		head := m.Archive[len(m.Archive)-1].Seq
		if _, err = store.Database().Exec(`INSERT INTO memory_digests VALUES(?,1,1,?,?, '旧铃声回顾','[]','[]','now')`, scope, head, head); err != nil {
			t.Fatal(err)
		}
	}
	// Emulate a pre-upgrade save: reconstruct only validated legacy links.
	if _, err = store.Database().Exec(`DELETE FROM event_dependencies; DELETE FROM meta WHERE key='projection_dependencies_v1'`); err != nil {
		t.Fatal(err)
	}
	store.Database().Close()
	_, err = a.Correct(ctx, w.WorldID, memorymodel.CorrectionRequest{RequestKey: "root", ExpectedEpoch: before.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: root, Replacement: "作者秘密：铃声并未发生"})
	if err != nil {
		t.Fatal(err)
	}
	if job := waitMemory(t, a, w.WorldID); job.Status != "completed" {
		t.Fatal(job)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	for _, scope := range []string{"player", "npc:mercenary"} {
		m, err := a.ReadMemory(ctx, w.WorldID, scope, scope != "player", 0)
		if err != nil {
			t.Fatal(err)
		}
		text := wire.MarshalJSON(m)
		if m.Digest.Content == "旧铃声回顾" {
			t.Fatal("covered digest not invalidated")
		}
		if strings.Contains(text, "你所在位置能听见铃声") || strings.Contains(text, "作者秘密") {
			t.Fatalf("invalid effective memory for %s: %s", scope, text)
		}
	}
	if !strings.Contains(sceneFor(after, "player"), "已纠正") {
		t.Fatal("stale scene survived", sceneFor(after, "player"))
	}
	for _, e := range after.Events {
		if strings.HasPrefix(e.EventID, root+":projection:") && !strings.Contains(e.Content, "已失效") {
			t.Fatal("effective projection survived", e)
		}
	}
	if wire.MarshalJSON(before.Messages) != wire.MarshalJSON(after.Messages) {
		t.Fatal("history rewritten")
	}
	store, err = storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	var original string
	if err = store.Database().QueryRow(`SELECT content FROM events WHERE event_id=?`, root).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if original != "作者隐藏事实：信使去向" {
		t.Fatal("raw event overwritten")
	}
	store.Database().Close()
	status, _ := a.Status(ctx)
	op, err := a.SaveAs(ctx, w.WorldID, "纠正副本", "copy-root", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); op.Status != "ready" && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		op, err = a.CopyOperation(ctx, op.OperationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if op.Status != "ready" {
		t.Fatal(op)
	}
	dataRoot := a.dataRoot
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: dataRoot, Generator: &plotTestGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, id := range []string{w.WorldID, op.TargetWorldID} {
		s := readContextSnapshot(t, reopened, id)
		if !strings.Contains(sceneFor(s, "player"), "已纠正") {
			t.Fatal("copy/restart lost scene correction")
		}
		m, err := reopened.ReadMemory(ctx, id, "npc:mercenary", true, 0)
		if err != nil || strings.Contains(wire.MarshalJSON(m), "你所在位置能听见铃声") {
			t.Fatal("copy/restart lost projection correction", err)
		}
	}
}
