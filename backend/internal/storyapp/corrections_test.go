package storyapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
)

type blockingDigestGenerator struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *blockingDigestGenerator) GenerateText(ctx context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
	case <-ctx.Done():
		return model.TextResponse{}, ctx.Err()
	}
	return model.TextResponse{Text: `{"content":"有效的个人回顾","states":[]}`}, nil
}

func TestRebuildBlocksGenerationAndCopyButAllowsDeletion(t *testing.T) {
	ctx := context.Background()
	g := &blockingDigestGenerator{entered: make(chan struct{}), release: make(chan struct{})}
	a := newTestApp(t, g)
	w, err := a.createFixtureWorld(ctx, "blocking", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, _ := storage.OpenWorldDB(path)
	for i := 1; i <= 12; i++ {
		_, err = store.Database().Exec(`INSERT INTO memory_sources VALUES('player',?,?,'',?,'player','message:player','已提交约定','now')`, i, fmt.Sprint(i), fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	store.Database().Close()
	if _, err = a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "block", ExpectedEpoch: w.ContextEpoch, Kind: "character", Scope: "npc:innkeeper", TargetID: "profile", Replacement: "谨慎的老板"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("rebuild not started")
	}
	if _, err = a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "busy", Input: "等待"}); !errors.Is(err, ErrMemoryRebuilding) {
		t.Fatal(err)
	}
	_, revision, _ := a.activeWorldState(ctx)
	if _, err = a.SaveAs(ctx, w.WorldID, "copy", "copy-blocked", revision); !errors.Is(err, ErrMemoryRebuilding) {
		t.Fatal("copy allowed", err)
	}
	if err = a.DeleteWorld(ctx, w.WorldID, revision); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late rebuild recreated world", err)
	}
}

func waitMemory(t *testing.T, a *App, id string) MemoryJob {
	t.Helper()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		_, job, err := a.Corrections(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" || job.Status == "failed" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("memory did not settle")
	return MemoryJob{}
}

func TestCorrectionVersionScopeAndImmutableHistory(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "corrections", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "turn", Input: "私下对沈岚说，暗号是白鹭。", AddresseeID: "npc:innkeeper"})
	if err != nil {
		t.Fatal(err)
	}
	r = waitRun(t, a, w.WorldID, r.RunID)
	if r.Status != "completed" {
		t.Fatal(r)
	}
	before := readContextSnapshot(t, a, w.WorldID)
	c := CorrectionRequest{RequestKey: "edit1", ExpectedEpoch: before.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: r.RunID + ":input", Replacement: "悄悄对沈岚说，暗号是青鹭。"}
	result, err := a.Correct(ctx, w.WorldID, c)
	if err != nil {
		t.Fatal(err)
	}
	if job := waitMemory(t, a, w.WorldID); job.Status != "completed" {
		t.Fatal(job)
	}
	same, err := a.Correct(ctx, w.WorldID, c)
	if err != nil || same.Epoch != result.Epoch {
		t.Fatal("idempotency", same, err)
	}
	c.RequestKey = "stale"
	if _, err = a.Correct(ctx, w.WorldID, c); !errors.Is(err, ErrVersionConflict) {
		t.Fatal(err)
	}
	after := readContextSnapshot(t, a, w.WorldID)
	if wire.MarshalJSON(before.Messages) != wire.MarshalJSON(after.Messages) {
		t.Fatal("rewrote original narration")
	}
	own, err := a.ReadMemory(ctx, w.WorldID, "npc:innkeeper", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.ReadMemory(ctx, w.WorldID, "npc:mercenary", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wire.MarshalJSON(other), "青鹭") || strings.Contains(wire.MarshalJSON(other), "白鹭") {
		t.Fatal("event correction leaked private text")
	}
	if !strings.Contains(wire.MarshalJSON(own), "青鹭") {
		t.Fatal("authorized full projection did not update")
	}
	if _, err = a.ReadMemory(ctx, w.WorldID, "npc:innkeeper", false, 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("player route exposed NPC", err)
	}
	c.RequestKey = "edit2"
	c.ExpectedEpoch = after.Summary.ContextEpoch
	c.Replacement = "悄悄对沈岚说，暗号是玄鹭。"
	if _, err = a.Correct(ctx, w.WorldID, c); err != nil {
		t.Fatal(err)
	}
	waitMemory(t, a, w.WorldID)
	own, err = a.ReadMemory(ctx, w.WorldID, "npc:innkeeper", true, 0)
	if err != nil || !strings.Contains(wire.MarshalJSON(own), "玄鹭") {
		t.Fatal("second revision failed", err)
	}
}

func TestCorrectionTargetsAndRestart(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	w, err := a.createFixtureWorld(ctx, "targets", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	request := CorrectionRequest{RequestKey: "character", ExpectedEpoch: w.ContextEpoch, Kind: "character", Scope: "npc:innkeeper", TargetID: "profile", Replacement: "谨慎的客栈老板，左手戴着旧手套。"}
	if _, err = a.Correct(ctx, w.WorldID, request); err != nil {
		t.Fatal(err)
	}
	waitMemory(t, a, w.WorldID)
	s := readContextSnapshot(t, a, w.WorldID)
	if !strings.Contains(s.Characters[0].Profile, "旧手套") {
		t.Fatal(s.Characters)
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, _ := storage.OpenWorldDB(path)
	if _, err = store.Database().Exec(`UPDATE memory_jobs SET status='running',completed=0`); err != nil {
		t.Fatal(err)
	}
	store.Database().Close()
	root := a.dataRoot
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: root, Generator: &scriptedGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if job := waitMemory(t, reopened, w.WorldID); job.Status != "completed" {
		t.Fatal(job)
	}
	v, err := reopened.ReadMemory(ctx, w.WorldID, "player", false, 0)
	if err != nil || v.Job.Status != "completed" {
		t.Fatal(v, err)
	}
}

func TestCorrectionNoticesStayScopedAndRecent(t *testing.T) {
	s := worldSnapshot{LongMemory: map[string]memoryContext{
		"npc:innkeeper": {Archive: []memorymodel.MemorySource{{ID: "p1", EventID: "private", Content: "暗号白鹭"}}},
		"npc:mercenary": {Archive: []memorymodel.MemorySource{{ID: "p2", EventID: "private", Content: "看见交谈"}}},
	}}
	n := correctionNotices(s, Correction{Kind: "event", TargetID: "private", Original: "暗号白鹭", Replacement: "暗号青鹭"}, "run")
	if n["npc:innkeeper"] != "暗号青鹭" || strings.Contains(n["npc:mercenary"], "青鹭") || n["npc:mercenary"] == "" {
		t.Fatal(n)
	}
	n = correctionNotices(s, Correction{Kind: "perception", Scope: "npc:innkeeper", Replacement: "明早在柜台归还。"}, "")
	if len(n) != 1 || n["npc:innkeeper"] != "明早在柜台归还。" {
		t.Fatal(n)
	}
	ctx := context.Background()
	a := newTestApp(t, &digestGenerator{})
	w, err := a.createFixtureWorld(ctx, "个人纠正", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, w.WorldID)
	_, err = a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "perception", ExpectedEpoch: w.ContextEpoch, Kind: "perception", Scope: "npc:innkeeper", TargetID: "perception:1", Replacement: "明早在柜台归还。"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitMemory(t, a, w.WorldID); j.Status != "completed" {
		t.Fatal(j)
	}
	v, err := a.ReadMemory(ctx, w.WorldID, "npc:innkeeper", true, 0)
	if err != nil || len(v.Sources) == 0 || v.Sources[0].Kind != "correction:perception" {
		t.Fatal(v, err)
	}
	v, err = a.ReadMemory(ctx, w.WorldID, "npc:mercenary", true, 0)
	if err != nil || strings.Contains(wire.MarshalJSON(v), "柜台归还") {
		t.Fatal("personal correction leaked", err)
	}
}
