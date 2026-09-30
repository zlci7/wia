package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gameagent/backend/internal/wire"
)

func TestProbeInterruptedAfterRenameFinishesOnRecovery(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	project, draft := publishableTestDraft(t, service, "harbor-interrupted")
	revision, files, _, err := service.buildPackage(ctx, draft, project)
	if err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(service.contentRoot(), project.GameID, revision)
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
	if _, err = service.appDB.ExecContext(ctx, `INSERT INTO content_operations(user_id,request_key,request_hash,operation_id,kind,target_id,stage,status,result_json,safe_error,plan_json,created_at,updated_at)
		VALUES(?,'rename-then-crash','hash','publish_renamed_crash','publish',?,'renamed','running','','',?,?,?)`, service.userID, draft.DraftID, plan, wire.NowText(), wire.NowText()); err != nil {
		t.Fatal(err)
	}
	if err = service.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	var status, digest string
	if err = service.appDB.QueryRowContext(ctx, `SELECT status FROM content_operations WHERE user_id=? AND operation_id='publish_renamed_crash'`, service.userID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("an interrupted rename was not finished: %q", status)
	}
	if err = service.appDB.QueryRowContext(ctx, `SELECT digest FROM content_revisions WHERE user_id=? AND revision=?`, service.userID, revision).Scan(&digest); err != nil {
		t.Fatalf("the completed package was not registered: %v", err)
	}
	reloaded, err := service.ReadContentDraft(ctx, draft.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := service.ReadContentProject(ctx, project.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.PublishContentDraft(ctx, PublishRequest{RequestKey: "after-recovery", DraftID: draft.DraftID, ExpectedDraftVersion: reloaded.Version, ExpectedProjectVersion: fresh.Version}); err != nil {
		t.Fatalf("publishing again after recovery: %v", err)
	}
}

func TestProbeSameKeyWhileRunningIsNotRecovered(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	project, draft := publishableTestDraft(t, service, "harbor-live")
	operation := ContentOperation{OperationID: "publish_live", Kind: "publish", TargetID: draft.DraftID, Stage: publishFilesWritten, Status: "running"}
	staging := filepath.Join(service.contentRoot(), operation.OperationID+".staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "story.json"), []byte(`{"partial":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.writeContentOperation(ctx, "live-key", "live-hash", operation, ""); err != nil {
		t.Fatal(err)
	}
	service.beginLiveOperation("live-key", operation)
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(200 * time.Millisecond)
		service.endLiveOperation("live-key")
	}()
	live, err := service.PublishContentDraft(ctx, PublishRequest{RequestKey: "live-key", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version, ExpectedProjectVersion: project.Version})
	if err != nil || live.Status != "running" || live.OperationID != operation.OperationID {
		t.Fatalf("same-key request while running: operation=%+v err=%v", live, err)
	}
	if _, err = os.Stat(staging); err != nil {
		t.Fatalf("the live publication lost its staging directory: %v", err)
	}
	<-done
	if err = service.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = service.appDB.QueryRowContext(ctx, `SELECT status FROM content_operations WHERE user_id=? AND request_key='live-key'`, service.userID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("an abandoned operation was not marked failed: %q", status)
	}
	if _, err = os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned staging survived recovery: %v", err)
	}
}

func TestRecoveredPublishKeepsTheOriginalRequestIdentity(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	project, draft := publishableTestDraft(t, service, "harbor-recover")
	revision, files, _, err := service.buildPackage(ctx, draft, project)
	if err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(service.contentRoot(), project.GameID, revision)
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
	if _, err = service.appDB.ExecContext(ctx, `INSERT INTO content_operations(user_id,request_key,request_hash,operation_id,kind,target_id,stage,status,result_json,safe_error,plan_json,created_at,updated_at)
		VALUES(?,'my-own-key','my-own-hash','publish_recover','publish',?,'renamed','running','','',?,?,?)`, service.userID, draft.DraftID, plan, wire.NowText(), wire.NowText()); err != nil {
		t.Fatal(err)
	}
	if err = service.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	operation, _, found, err := service.readContentOperation(ctx, "my-own-key")
	if err != nil || !found || operation.Status != "succeeded" || operation.Stage != publishReady {
		t.Fatalf("recovered operation: %+v found=%v err=%v", operation, found, err)
	}
	var rows, emptyKeys int
	if err = service.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_operations WHERE user_id=? AND operation_id='publish_recover'`, service.userID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err = service.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_operations WHERE user_id=? AND request_key=''`, service.userID).Scan(&emptyKeys); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || emptyKeys != 0 {
		t.Fatalf("recovery rows: operation=%d empty_keys=%d", rows, emptyKeys)
	}
}

func TestRecoveryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	project, draft := publishableTestDraft(t, service, "harbor-idempotent")
	operation, err := service.PublishContentDraft(ctx, PublishRequest{RequestKey: "idem-key", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version, ExpectedProjectVersion: project.Version})
	if err != nil || operation.Status != "succeeded" {
		t.Fatalf("publish: %+v %v", operation, err)
	}
	count := func() int {
		var rows int
		if queryErr := service.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_operations WHERE user_id=? AND request_key=?`, service.userID, "idem-key").Scan(&rows); queryErr != nil {
			t.Fatal(queryErr)
		}
		return rows
	}
	before := count()
	if err = service.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	again, _, found, err := service.readContentOperation(ctx, "idem-key")
	if err != nil || !found || again.Status != "succeeded" || before != count() || before != 1 {
		t.Fatalf("idempotent recovery: operation=%+v found=%v rows=%d err=%v", again, found, count(), err)
	}
}
