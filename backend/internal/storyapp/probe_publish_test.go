package storyapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gameagent/backend/internal/wire"
)

// R03, first half: a publication interrupted after the rename but before the
// application record must finish, not delete a complete package and then refuse to
// publish the same content again.
func TestProbeInterruptedAfterRenameFinishesOnRecovery(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-interrupted")

	// Build the exact package a publication would produce and put it in place, the way
	// a process that died between rename and registration leaves it.
	revision, files, _, err := a.buildPackage(ctx, draft, project)
	if err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(a.contentRoot(), project.GameID, revision)
	if err = os.MkdirAll(final, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		target := filepath.Join(final, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan := wire.MarshalJSON(publishPlan{GameID: project.GameID, ProjectID: project.ProjectID, ProjectVersion: project.Version, Revision: revision, FinalPath: final})
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_operations(user_id,request_key,request_hash,operation_id,kind,target_id,stage,status,result_json,safe_error,plan_json,created_at,updated_at)
		VALUES(?,'rename-then-crash','hash','publish_renamed_crash','publish',?,'renamed','running','','',?,?,?)`,
		a.userID, draft.DraftID, plan, wire.NowText(), wire.NowText()); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	var status, digest string
	if err = a.appDB.QueryRowContext(ctx, `SELECT status FROM content_operations WHERE user_id=? AND operation_id='publish_renamed_crash'`, a.userID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("an interrupted rename was not finished: %q", status)
	}
	if err = a.appDB.QueryRowContext(ctx, `SELECT digest FROM content_revisions WHERE user_id=? AND revision=?`, a.userID, revision).Scan(&digest); err != nil {
		t.Fatalf("the completed package was not registered: %v", err)
	}
	// Publishing the same content again is idempotent because the directory is intact
	// and registered, not a dead end.
	reloaded, err := a.ReadContentDraft(ctx, draft.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := a.ReadContentProject(ctx, project.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "after-recovery", DraftID: draft.DraftID, ExpectedDraftVersion: reloaded.Version, ExpectedProjectVersion: fresh.Version}); err != nil {
		t.Fatalf("publishing again after recovery: %v", err)
	}
}

// R03, second half: a request that arrives while the original publication is still
// running must not be treated as a crash to recover.
func TestProbeSameKeyWhileRunningIsNotRecovered(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-live")

	// Hold the live slot the way an in-flight publication does.
	operation := ContentOperation{OperationID: "publish_live", Kind: "publish", TargetID: draft.DraftID, Stage: publishFilesWritten, Status: "running"}
	staging := filepath.Join(a.contentRoot(), operation.OperationID+".staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "story.json"), []byte(`{"partial":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.writeContentOperation(ctx, "live-key", "live-hash", operation, ""); err != nil {
		t.Fatal(err)
	}
	a.beginLiveOperation("live-key", operation)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The original writer is still working; a same-key request must not delete its
		// staging directory out from under it.
		time.Sleep(200 * time.Millisecond)
		a.endLiveOperation("live-key")
	}()
	live, err := a.PublishContentDraft(ctx, PublishRequest{RequestKey: "live-key", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version, ExpectedProjectVersion: project.Version})
	if err != nil {
		t.Fatalf("same-key request while running failed: %v", err)
	}
	if live.Status != "running" || live.OperationID != operation.OperationID {
		t.Fatalf("same-key request did not return the live operation: %+v", live)
	}
	if _, err = os.Stat(staging); err != nil {
		t.Fatalf("the live publication lost its staging directory: %v", err)
	}
	<-done
	// Once the writer finishes, a stale running row is recovered as before.
	if err = a.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = a.appDB.QueryRowContext(ctx, `SELECT status FROM content_operations WHERE user_id=? AND request_key='live-key'`, a.userID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("an abandoned operation was not marked failed: %q", status)
	}
	if _, err = os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned staging survived recovery: %v", err)
	}
}
