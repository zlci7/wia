package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
)

func TestMessagePagesRemainOrderedAndIsolated(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &scriptedGenerator{})
	world, err := app.createFixtureWorld(ctx, "", "guided", "", "", true)
	if err != nil || world.Name == "" {
		t.Fatalf("default world: %+v, %v", world, err)
	}
	store, err := storage.OpenWorldDB(app.worldPath(world.WorldID))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	appendMessages := func(start, end int) {
		t.Helper()
		tx, err := store.Database().Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for seq := start; seq <= end; seq++ {
			if _, err := tx.Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(?,?,'narrative',?,'',?)`, seq, fmt.Sprintf("message-%d", seq), fmt.Sprintf("故事 %d", seq), wire.NowText()); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	appendMessages(2, 251)
	upper := int64(201)
	exact, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{BeforeSeq: &upper, Limit: 200})
	if err != nil || len(exact.Messages) != 200 || exact.HasMore || exact.NextBeforeSeq != nil || exact.Messages[0].Seq != 1 || exact.Messages[199].Seq != 200 {
		t.Fatalf("exact full final page: %+v, %v", exact, err)
	}
	upper = 1
	beforeFirst, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{BeforeSeq: &upper})
	if err != nil || beforeFirst.Messages == nil || len(beforeFirst.Messages) != 0 || beforeFirst.HasMore {
		t.Fatalf("before first: %+v, %v", beforeFirst, err)
	}
	latest, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{})
	if err != nil || len(latest.Messages) != 100 || latest.Messages[0].Seq != 152 || latest.Messages[99].Seq != 251 || !latest.HasMore || latest.NextBeforeSeq == nil || *latest.NextBeforeSeq != 152 || latest.NextAfterSeq != nil {
		t.Fatalf("latest: %+v, %v", latest, err)
	}
	// New messages do not move an exclusive backward cursor.
	appendMessages(252, 255)
	older, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{BeforeSeq: latest.NextBeforeSeq})
	if err != nil || len(older.Messages) != 100 || older.Messages[0].Seq != 52 || older.Messages[99].Seq != 151 {
		t.Fatalf("older: %+v, %v", older, err)
	}
	first, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{BeforeSeq: older.NextBeforeSeq})
	if err != nil || len(first.Messages) != 51 || first.Messages[0].Seq != 1 || first.HasMore || first.NextBeforeSeq != nil {
		t.Fatalf("first: %+v, %v", first, err)
	}
	after := int64(0)
	var seqs []int64
	for {
		page, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{AfterSeq: &after, Limit: 70})
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range page.Messages {
			seqs = append(seqs, message.Seq)
		}
		if page.NextBeforeSeq != nil {
			t.Fatal("forward page has backward cursor")
		}
		if !page.HasMore {
			break
		}
		after = *page.NextAfterSeq
	}
	if len(seqs) != 255 {
		t.Fatalf("received %d messages", len(seqs))
	}
	for i, seq := range seqs {
		if seq != int64(i+1) {
			t.Fatalf("gap/duplicate at %d: %d", i, seq)
		}
	}
	after = 255
	empty, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{AfterSeq: &after})
	if err != nil || empty.Messages == nil || len(empty.Messages) != 0 || empty.HasMore {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	other, err := app.createFixtureWorld(ctx, "另一个世界", "guided", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := app.ReadMessagePage(ctx, other.WorldID, MessagePageRequest{})
	if err != nil || len(isolated.Messages) != 1 || isolated.Messages[0].Seq != 1 {
		t.Fatalf("isolation: %+v %v", isolated, err)
	}
	if _, err := app.ReadMessagePage(ctx, "unknown-world", MessagePageRequest{}); !errors.Is(err, ErrWorldNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	status, err := app.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteWorld(ctx, other.WorldID, status.ActiveRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadMessagePage(ctx, other.WorldID, MessagePageRequest{}); !errors.Is(err, ErrWorldNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	for _, req := range []MessagePageRequest{{Limit: -1}, {Limit: 201}, {BeforeSeq: &after, AfterSeq: &after}} {
		if _, err := app.ReadMessagePage(ctx, world.WorldID, req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid: %+v %v", req, err)
		}
	}
}

func TestMessagePageOwnershipAndReadiness(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &scriptedGenerator{})
	world, err := app.createFixtureWorld(ctx, "隔离测试", "guided", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		column, value, original string
		want                    error
	}{
		{"user_id", "other-owner", app.userID, ErrWorldNotFound},
		{"game_id", "other-game", GameID, ErrWorldNotFound},
		{"status", "copying", "ready", ErrWorldNotReady},
	} {
		t.Run(check.column, func(t *testing.T) {
			if _, err := app.appDB.Exec(`UPDATE worlds SET `+check.column+`=? WHERE world_id=?`, check.value, world.WorldID); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := app.appDB.Exec(`UPDATE worlds SET `+check.column+`=? WHERE world_id=?`, check.original, world.WorldID); err != nil {
					t.Error(err)
				}
			}()
			if _, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{}); !errors.Is(err, check.want) {
				t.Fatalf("read: %v, want %v", err, check.want)
			}
		})
	}
}

func TestMessagePagesConcurrentAppendAndDelete(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &scriptedGenerator{})
	world, err := app.createFixtureWorld(ctx, "并发测试", "guided", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.OpenWorldDB(app.worldPath(world.WorldID))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Database().Close()
	written := make(chan error, 1)
	go func() {
		for seq := 2; seq <= 51; seq++ {
			_, err := store.Database().Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(?,?,'narrative','故事','',?)`, seq, fmt.Sprintf("parallel-%d", seq), wire.NowText())
			if err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()
	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for after < 51 {
		if time.Now().After(deadline) {
			t.Fatalf("concurrent reader stopped at %d", after)
		}
		page, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{AfterSeq: &after, Limit: 7})
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range page.Messages {
			if message.Seq != after+1 {
				t.Fatalf("gap after %d: %d", after, message.Seq)
			}
			after = message.Seq
		}
		if len(page.Messages) == 0 {
			select {
			case err := <-written:
				if err != nil {
					t.Fatal(err)
				}
				written <- nil
			default:
			}
		}
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	store.Database().Close()
	status, err := app.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- app.DeleteWorld(ctx, world.WorldID, status.ActiveRevision) }()
	for i := 0; i < 10; i++ {
		_, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{})
		if err != nil && !errors.Is(err, ErrWorldNotFound) {
			t.Fatal(err)
		}
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadMessagePage(ctx, world.WorldID, MessagePageRequest{}); !errors.Is(err, ErrWorldNotFound) {
		t.Fatalf("deleted read: %v", err)
	}
	if _, err := os.Stat(app.worldPath(world.WorldID)); !os.IsNotExist(err) {
		t.Fatalf("deleted database must stay absent: %v", err)
	}
}

func TestSaveAsUsesDefaultNameAndCopiesHistory(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, &scriptedGenerator{})
	world, err := app.createFixtureWorld(ctx, "", "guided", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	status, err := app.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op, err := app.SaveAs(ctx, world.WorldID, "", "default-name-copy", status.ActiveRevision)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for op.Status != "ready" && op.Status != "failed" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		op, err = app.CopyOperation(ctx, op.OperationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if op.Status != "ready" || op.TargetName == "" {
		t.Fatalf("copy: %+v", op)
	}
	page, err := app.ReadMessagePage(ctx, op.TargetWorldID, MessagePageRequest{})
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("copied history: %+v %v", page, err)
	}
}
