package storyapp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gameagent/runtime/internal/llm"
	"gameagent/runtime/internal/model"
)

func seedMemoryHistory(t *testing.T, a *App, id string) {
	t.Helper()
	path, _, err := a.worldRecord(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 1; i <= 10; i++ {
		run := fmt.Sprintf("fixture-history-%02d", i)
		content := "我安静地看雨，没有新的约定。"
		if i == 1 {
			content = "我私下答应沈岚，明早在旧码头归还借来的铜钥匙。"
		}
		if _, err = tx.Exec(`INSERT INTO runs(run_id,request_key,request_hash,input,addressee_id,attempt,status,created_at,updated_at) VALUES(?,?,?,?,'npc:innkeeper',1,'completed','now','now')`, run, run, run, content); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO events VALUES(?,?,?,?,?,?,?,?,?,?,?)`, i+1, run+":input", "player_attempt", "player", "npc:innkeeper", content, run, 1, 1, "player_private", "now"); err != nil {
			t.Fatal(err)
		}
		for _, scope := range []string{"npc:innkeeper", "npc:mercenary"} {
			projection := content
			if scope == "npc:mercenary" {
				projection = "旅人与老板低声交谈，听不见内容。"
			}
			if _, err = tx.Exec(`INSERT INTO perceptions(recipient_id,source_event_id,source_type,content,stage,scene_version,created_at) VALUES(?,?,'player_private',?,1,1,'now')`, scope, run+":input", projection); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = tx.Exec(`INSERT INTO messages VALUES(?,?,'player',?,?,'now')`, i*2, run+":player", content, run); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO messages VALUES(?,?,'narrative',?,?,'now')`, i*2+1, run+":story", content, run); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{"event_head": "11", "message_head": "21"} {
		if err = metaSetTx(context.Background(), tx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDigestEditSurvivesOtherScopeAndCopy(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &digestGenerator{})
	w, err := a.CreateWorld(ctx, "记忆修订", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, w.WorldID)
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	s, _ := openWorldDB(path)
	snapshot, err := loadWorldSnapshot(ctx, s, 40)
	if err == nil {
		err = a.prepareLongMemory(ctx, s, &snapshot, Run{BaseContextEpoch: w.ContextEpoch}, &digestGenerator{})
	}
	s.db.Close()
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.ReadMemory(ctx, w.WorldID, "player", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "digest", ExpectedEpoch: view.Epoch, Kind: "digest", Scope: "player", TargetID: fmt.Sprint(view.Digest.Revision), Replacement: "我记得约定，也记得暂时没有参与调查。"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitMemory(t, a, w.WorldID); j.Status != "completed" {
		t.Fatal(j)
	}
	_, err = a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "other", ExpectedEpoch: c.Epoch, Kind: "character", Scope: "npc:mercenary", TargetID: "profile", Replacement: "谨慎的佣兵。"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitMemory(t, a, w.WorldID); j.Status != "completed" {
		t.Fatal(j)
	}
	view, _ = a.ReadMemory(ctx, w.WorldID, "player", false, 0)
	if view.Digest.Content != c.Replacement {
		t.Fatal("unrelated correction lost manual digest", view.Digest)
	}
	status, _ := a.Status(ctx)
	op, err := a.SaveAs(ctx, w.WorldID, "记忆分支", "memory-copy", status.ActiveRevision)
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
	branch, err := a.ReadMemory(ctx, op.TargetWorldID, "player", false, 0)
	if err != nil || branch.Digest.Content != c.Replacement {
		t.Fatal(branch, err)
	}
	_, err = a.Correct(ctx, op.TargetWorldID, CorrectionRequest{RequestKey: "branch-edit", ExpectedEpoch: branch.Epoch, Kind: "digest", Scope: "player", TargetID: fmt.Sprint(branch.Digest.Revision), Replacement: "分支自己的回顾。"})
	if err != nil {
		t.Fatal(err)
	}
	waitMemory(t, a, op.TargetWorldID)
	view, _ = a.ReadMemory(ctx, w.WorldID, "player", false, 0)
	if view.Digest.Content != c.Replacement {
		t.Fatal("branch changed source")
	}
}

func TestMemoryFailureRetry(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &digestGenerator{fail: true})
	w, err := a.CreateWorld(ctx, "重建失败", "open", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, w.WorldID)
	c, err := a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "fail", ExpectedEpoch: w.ContextEpoch, Kind: "character", Scope: "npc:innkeeper", TargetID: "profile", Replacement: "谨慎的老板。"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitMemory(t, a, w.WorldID); j.Status != "failed" {
		t.Fatal(j)
	}
	a.modelMu.Lock()
	a.generator = &digestGenerator{}
	a.modelMu.Unlock()
	if err = a.RetryMemory(ctx, w.WorldID, c.Epoch); err != nil {
		t.Fatal(err)
	}
	if j := waitMemory(t, a, w.WorldID); j.Status != "completed" {
		t.Fatal(j)
	}
}

// Ten committed fixture turns exercise compaction; subsequent calls use a real
// provider. This is not a claim that the fixture turns were model-generated.
func TestM2RealMemoryLifecycle(t *testing.T) {
	path := os.Getenv("WIA_AUTONOMY_MODEL_CONFIG")
	if path == "" {
		t.Skip("opt-in real-model evaluation")
	}
	p, cfg, err := llm.NewProviderFromConfigFile(path)
	if err != nil {
		t.Fatal("real provider configuration unavailable")
	}
	g, ok := p.(model.TextGenerator)
	if !ok || cfg.Provider == "fake" {
		t.Fatal("real model required")
	}
	ctx := context.Background()
	a := newTestApp(t, g)
	logger := &recordingLogger{}
	a.logger = logger
	w, err := a.CreateWorld(ctx, "长期约定验证", "open", "旅人", "谨慎礼貌，不替别人许诺", true)
	if err != nil {
		t.Fatal(err)
	}
	seedMemoryHistory(t, a, w.WorldID)
	play := func(app *App, id, key, input string) {
		t.Helper()
		start := time.Now()
		r, e := app.SubmitRun(ctx, id, RunRequest{RequestKey: key, Input: input, AddresseeID: "npc:innkeeper"})
		if e != nil {
			t.Fatal(e)
		}
		for deadline := time.Now().Add(310 * time.Second); time.Now().Before(deadline); {
			r, e = app.Run(ctx, id, r.RunID)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != "running" && r.Status != "accepted" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Logf("step=%s status=%s reason=%s elapsed_ms=%d", key, r.Status, r.Reason, time.Since(start).Milliseconds())
		if r.Status != "completed" {
			for _, line := range strings.Split(logger.String(), "\n") {
				if strings.Contains(line, r.RunID) && (strings.Contains(line, "failed") || strings.Contains(line, "validation")) {
					t.Log(line)
				}
			}
			t.Fatal("real memory turn incomplete")
		}
		for _, m := range readContextSnapshot(t, app, id).Messages {
			if m.RunID == r.RunID && m.Kind == "narrative" {
				t.Log("narrative=" + m.Content)
			}
		}
	}
	t.Logf("provider=%s model=%s preset_committed_turns=10", cfg.Provider, cfg.Model)
	play(a, w.WorldID, "recall", "我低声问沈岚：之前借的那把钥匙，我约好什么时候、在哪里归还？")
	v, err := a.ReadMemory(ctx, w.WorldID, "npc:innkeeper", true, 0)
	if err != nil || v.Digest.Through == 0 {
		for _, line := range strings.Split(logger.String(), "\n") {
			if strings.Contains(line, "memory") || strings.Contains(line, "JSON validation") {
				t.Log(line)
			}
		}
		t.Fatal("real compaction missing", err)
	}
	t.Logf("digest_revision=%d through=%d sources=%d", v.Digest.Revision, v.Digest.Through, len(v.Sources))
	var target string
	for _, r := range v.Records {
		if r.Kind == "perception" && strings.Contains(r.Content, "明早在旧码头") {
			target = r.TargetID
			break
		}
	}
	if target == "" {
		t.Fatal("fixture perception not available")
	}
	_, err = a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "correct", ExpectedEpoch: v.Epoch, Kind: "perception", Scope: "npc:innkeeper", TargetID: target, Replacement: "旅人答应明早在客栈柜台归还铜钥匙，而非旧码头。"})
	if err != nil {
		t.Fatal(err)
	}
	var job MemoryJob
	for deadline := time.Now().Add(240 * time.Second); time.Now().Before(deadline); {
		_, job, err = a.Corrections(ctx, w.WorldID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" || job.Status == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("rebuild=%s scopes_completed=%d", job.Status, job.Completed)
	if job.Status != "completed" {
		t.Fatal(job)
	}
	status, _ := a.Status(ctx)
	op, err := a.SaveAs(ctx, w.WorldID, "验证分支", "real-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); op.Status != "ready" && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		op, err = a.CopyOperation(ctx, op.OperationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if op.Status != "ready" {
		t.Fatal(op)
	}
	root := a.DataRoot()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: root, Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.logger = logger
	status, _ = reopened.Status(ctx)
	if _, err = reopened.ActivateWorld(ctx, op.TargetWorldID, status.ActiveRevision); err != nil {
		t.Fatal(err)
	}
	play(reopened, op.TargetWorldID, "after-copy-restart", "我低声对沈岚说：再确认一下，你记得我答应在哪里还钥匙？")
}
