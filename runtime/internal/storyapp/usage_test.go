package storyapp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gameagent/runtime/internal/model"
)

func TestUsagePersistencePaginationAndOwnership(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, err := Open(ctx, Options{DataRoot: root, Generator: &scriptedGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	world, err := a.CreateWorld(ctx, "usage", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	scope := ContextScope{World: world.WorldID, Run: "run-test", Purpose: "npc", Attempt: 1}
	for n := 0; n < 102; n++ {
		id, err := a.beginUsage(ctx, scope, ContextBuildReport{InputTokens: 100, TotalOutputTokens: 50})
		if err != nil {
			t.Fatal(err)
		}
		if n == 101 {
			continue
		}
		d := model.TextDiagnostic{Provider: "deepseek", Model: "test"}
		if n < 100 {
			i, o, r, h, m := 100, 20, 5, 60, 40
			d.SetUsage(&i, &o, &r, &h, &m)
		}
		var failure error
		if n == 100 {
			failure = context.DeadlineExceeded
		}
		if err := a.finishUsage(id, d, time.Second, failure); err != nil {
			t.Fatal(err)
		}
	}
	page, err := a.ReadUsage(ctx, world.WorldID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Totals.Calls != 102 || page.Totals.CacheKnown != 100 || page.Totals.Failed != 1 || page.Totals.Unconfirmed != 1 || page.Totals.CacheHitRate == nil || *page.Totals.CacheHitRate != .6 || len(page.Calls) != 100 || page.NextBeforeID == 0 {
		t.Fatalf("page %+v", page.Totals)
	}
	if page.Calls[0].InputTokens != nil || page.Calls[1].InputTokens != nil {
		t.Fatal("unknown converted to zero")
	}
	next, err := a.ReadUsage(ctx, world.WorldID, page.NextBeforeID)
	if err != nil || len(next.Calls) != 2 || next.NextBeforeID != 0 || next.Calls[0].ID >= page.NextBeforeID {
		t.Fatalf("next: %+v %v", next, err)
	}
	if _, err := a.ReadUsage(ctx, "missing-world", 0); !errors.Is(err, ErrWorldNotFound) {
		t.Fatalf("world authorization: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Open(ctx, Options{DataRoot: root, Generator: &scriptedGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	page, err = a.ReadUsage(ctx, "", 0)
	if err != nil || page.Totals.Calls != 102 {
		t.Fatalf("restart: %v %+v", err, page.Totals)
	}
	other := &App{appDB: a.appDB, userID: "other"}
	page, err = other.ReadUsage(ctx, "", 0)
	if err != nil || page.Totals.Calls != 0 {
		t.Fatalf("owner leaked: %v %+v", err, page.Totals)
	}
}

type usageGenerator struct{ fail bool }

func (g usageGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	d := model.TextDiagnostic{Provider: "test", Model: "test"}
	i, o, r, h, m := 10, 5, 2, 4, 6
	d.SetUsage(&i, &o, &r, &h, &m)
	if g.fail {
		return model.TextResponse{}, &model.TextCallError{Diagnostic: d, Cause: context.Canceled}
	}
	return model.TextResponse{Text: "ok", Diagnostic: d}, nil
}

func TestUsageConcurrentActualCallsAndFailure(t *testing.T) {
	a := newTestApp(t, &scriptedGenerator{})
	var wg sync.WaitGroup
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			g := a.contextGenerator(usageGenerator{fail: n%2 == 0}, contextMaterial{Required: "facts"}, worldSnapshot{}, Run{}, "npc", "npc:test", 1, "test")
			_, _ = g.GenerateText(context.Background(), model.TextRequest{System: "system", MaxOutputTokens: 20})
		}(n)
	}
	wg.Wait()
	page, err := a.ReadUsage(context.Background(), "", 0)
	if err != nil || page.Totals.Calls != 12 || page.Totals.Failed != 6 || page.Totals.InputTokens != 120 || page.Totals.OutputTokens != 60 {
		t.Fatalf("usage: %+v %v", page.Totals, err)
	}
}
