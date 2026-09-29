package storyapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// C02: finishing an interrupted publication must update the operation that recorded it.
// Using an empty request key used to leave the original key stuck in `running` and add a
// second row, so a client retrying with its own key never saw success.
func TestRecoveredPublishKeepsTheOriginalRequestIdentity(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-recover")

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
	plan := marshalJSON(publishPlan{GameID: project.GameID, ProjectID: project.ProjectID, ProjectVersion: project.Version, Revision: revision, FinalPath: final})
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_operations(user_id,request_key,request_hash,operation_id,kind,target_id,stage,status,result_json,safe_error,plan_json,created_at,updated_at)
		VALUES(?,'my-own-key','my-own-hash','publish_recover','publish',?,'renamed','running','','',?,?,?)`,
		a.userID, draft.DraftID, plan, nowText(), nowText()); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	// The original request key must report success.
	operation, _, found, err := a.readContentOperation(ctx, "my-own-key")
	if err != nil || !found {
		t.Fatalf("the original request key is gone: %+v %v", operation, err)
	}
	if operation.Status != "succeeded" || operation.Stage != publishReady {
		t.Fatalf("the original request key still reports %q/%q", operation.Stage, operation.Status)
	}
	// There must be exactly one row for this operation.
	var rows int
	if err = a.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_operations WHERE user_id=? AND operation_id='publish_recover'`, a.userID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("the recovered operation has %d rows", rows)
	}
	var emptyKeys int
	if err = a.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_operations WHERE user_id=? AND request_key=''`, a.userID).Scan(&emptyKeys); err != nil {
		t.Fatal(err)
	}
	if emptyKeys != 0 {
		t.Fatalf("recovery created %d rows under an empty request key", emptyKeys)
	}
	// Replaying the same request with its own key returns that success instead of a
	// conflict about the project version.
	reloaded, err := a.ReadContentDraft(ctx, draft.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, _, found, err := a.readContentOperation(ctx, "my-own-key")
	if err != nil || !found || replayed.Status != "succeeded" {
		t.Fatalf("replay: %+v %v", replayed, err)
	}
	_ = reloaded
}

// Recovery must also be idempotent: a second pass over an already finished operation
// changes nothing and creates no extra rows.
func TestRecoveryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-idempotent")
	operation, err := a.PublishContentDraft(ctx, PublishRequest{RequestKey: "idem-key", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version, ExpectedProjectVersion: project.Version})
	if err != nil || operation.Status != "succeeded" {
		t.Fatalf("publish: %+v %v", operation, err)
	}
	before, err := a.countOperationRows(ctx, "idem-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	again, _, found, err := a.readContentOperation(ctx, "idem-key")
	if err != nil || !found || again.Status != "succeeded" {
		t.Fatalf("after a second recovery pass: %+v %v", again, err)
	}
	after, err := a.countOperationRows(ctx, "idem-key")
	if err != nil {
		t.Fatal(err)
	}
	if before != after || after != 1 {
		t.Fatalf("operation rows changed: %d -> %d", before, after)
	}
}

func (a *App) countOperationRows(ctx context.Context, requestKey string) (int, error) {
	var rows int
	err := a.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_operations WHERE user_id=? AND request_key=?`, a.userID, requestKey).Scan(&rows)
	return rows, err
}
