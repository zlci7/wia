package storyapp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/storage"
)

func TestPendingDigestEditSurvivesSupersedingJob(t *testing.T) {
	ctx := context.Background()
	g := &blockingDigestGenerator{entered: make(chan struct{}), release: make(chan struct{})}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(ctx, "连续纠正", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 12; i++ {
		if _, err = store.Database().Exec(`INSERT INTO memory_sources VALUES('player',?,?,'',?,'player','message:player','已提交约定','now')`, i, fmt.Sprint(i), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.Database().Exec(`INSERT INTO memory_digests VALUES('npc:mercenary',1,1,0,0,'旧回顾','[]','[]','now')`); err != nil {
		t.Fatal(err)
	}
	store.Database().Close()
	c, err := a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "first", ExpectedEpoch: w.ContextEpoch, Kind: "digest", Scope: "npc:mercenary", TargetID: "1", Replacement: "人工确认的回顾"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker not blocked")
	}
	_, err = a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "second", ExpectedEpoch: c.Epoch, Kind: "character", Scope: "npc:innkeeper", TargetID: "profile", Replacement: "另一人物的新设定"})
	if err != nil {
		close(g.release)
		t.Fatal(err)
	}
	close(g.release)
	if job := waitMemory(t, a, w.WorldID); job.Status != "completed" {
		t.Fatal(job)
	}
	view, err := a.ReadMemory(ctx, w.WorldID, "npc:mercenary", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if view.Digest.Content != "人工确认的回顾" {
		t.Fatalf("accepted edit lost: %+v", view.Digest)
	}
	s := readContextSnapshot(t, a, w.WorldID)
	if s.Characters[0].Profile != "另一人物的新设定" {
		t.Fatal("second edit lost")
	}
	store, err = storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	var count int
	if err = store.Database().QueryRow(`SELECT COUNT(*) FROM memory_digests WHERE epoch=?`, c.Epoch).Scan(&count); err != nil || count != 0 {
		t.Fatal("superseded worker published", count, err)
	}
}

func TestPendingDigestEditDoesNotReviveInvalidatedHistory(t *testing.T) {
	d := memorymodel.MemoryDigest{Epoch: 1, Through: 5}
	archive := []memorymodel.MemorySource{{Seq: 1, EventID: "projection"}}
	cs := []Correction{{Epoch: 2, Kind: "digest", Scope: "npc:a", Replacement: "人工回顾"}, {Epoch: 3, Kind: "event", TargetID: "root", Dependents: []string{"projection"}}}
	if pendingDigestEdit(d, "npc:a", 3, archive, cs, nil) != nil {
		t.Fatal("later source correction must invalidate manual digest")
	}
	d.Epoch = 3
	if pendingDigestEdit(d, "npc:a", 4, archive, cs, nil) != nil {
		t.Fatal("already incorporated edit must not replay")
	}
}
