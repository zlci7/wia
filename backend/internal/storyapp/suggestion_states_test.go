package storyapp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
)

// Reading suggestions never calls the model, and each state is reported without
// inventing work: a fresh world waits for committed narrative, an ended guided
// story stops offering next steps, and a disabled preference stays disabled.
func TestSuggestionStatesWithoutModelCalls(t *testing.T) {
	ctx := context.Background()
	g := &suggestionProbe{requests: make(chan model.TextRequest, 2)}
	a := newTestApp(t, g)
	w, err := a.CreateWorld(ctx, "states", "guided", "旅人", "寻找答案", true)
	if err != nil {
		t.Fatal(err)
	}
	set, err := a.ReadSuggestions(ctx, w.WorldID)
	if err != nil || set.Status != "empty" || !set.Enabled || len(set.Items) != 0 {
		t.Fatalf("fresh world read: %+v %v", set, err)
	}
	status, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := SuggestionRequest{Basis: suggestionBasis(w), ExpectedActiveRevision: status.ActiveRevision}
	if set, err = a.ReadSuggestions(ctx, w.WorldID); err != nil || set.Status != "empty" {
		t.Fatalf("reading must not start generation: %+v %v", set, err)
	}
	if g.calls.Load() != 0 {
		t.Fatal("reading suggestions called the model")
	}

	// A committed narrative followed by an uncommitted input is not a basis for
	// auxiliary generation: the request stays waiting until the turn commits.
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Database().Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(?,?,?,?,?,?)`, 2, "narrative-1", "narrative", "雨声敲着窗沿。", "", wire.NowText()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Database().Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(?,?,?,?,?,?)`, 3, "pending-input", "player", "我在等待天亮。", "", wire.NowText()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Database().Exec(`UPDATE meta SET value='3' WHERE key='message_head'`); err != nil {
		t.Fatal(err)
	}
	store.Database().Close()
	refreshed, err := a.ReadWorld(ctx, w.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Summary.MessageHead != 3 {
		t.Fatalf("fixture narrative was not committed: %+v", refreshed.Summary)
	}
	req = SuggestionRequest{Basis: suggestionBasis(refreshed.Summary), ExpectedActiveRevision: status.ActiveRevision}
	set, err = a.RequestSuggestions(ctx, w.WorldID, req)
	if err != nil || set.Status != "waiting" {
		t.Fatalf("uncommitted input must not be a generation basis: %+v %v", set, err)
	}
	// The waiting answer is recomputed on each request instead of being stored, so
	// a later read reports the neutral state until the turn commits.
	if set, err = a.ReadSuggestions(ctx, w.WorldID); err != nil || set.Status != "empty" || len(set.Items) != 0 {
		t.Fatalf("stored a transient waiting state: %+v %v", set, err)
	}

	// The preference path needs no model call either.
	off := false
	req.Enabled = &off
	set, err = a.RequestSuggestions(ctx, w.WorldID, req)
	if err != nil || set.Status != "disabled" || set.Enabled {
		t.Fatalf("disable failed: %+v %v", set, err)
	}
	set, err = a.ReadSuggestions(ctx, w.WorldID)
	if err != nil || set.Enabled || set.Status != "disabled" || len(set.Items) != 0 {
		t.Fatalf("disabled state lost: %+v %v", set, err)
	}
	if g.calls.Load() != 0 {
		t.Fatal("preference change called the model")
	}
}

// A guided story that reached its ending stops offering next steps without
// touching the stored preference.
func TestSuggestionStatesEndedGuidedStory(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &plotTestGenerator{})
	w, err := a.createFixtureWorld(ctx, "结局后建议", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		run, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: fmt.Sprintf("wait-%d", i), Input: "我在原地等待一个小时。"})
		if err != nil {
			t.Fatal(err)
		}
		if done := waitRun(t, a, w.WorldID, run.RunID); done.Status != "completed" {
			t.Fatalf("run=%+v", done)
		}
	}
	snapshot, err := a.ReadWorld(ctx, w.WorldID, 20)
	if err != nil || !snapshot.Summary.StoryEnded {
		t.Fatalf("fixture did not reach an ending: %+v %v", snapshot.Summary, err)
	}
	status, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set, err := a.ReadSuggestions(ctx, w.WorldID)
	if err != nil || set.Status != "ended" || len(set.Items) != 0 {
		t.Fatalf("ended world: %+v %v", set, err)
	}
	req := SuggestionRequest{Basis: suggestionBasis(snapshot.Summary), ExpectedActiveRevision: status.ActiveRevision}
	set, err = a.RequestSuggestions(ctx, w.WorldID, req)
	if err != nil || set.Status != "ended" {
		t.Fatalf("ended world generated: %+v %v", set, err)
	}
}

// Without a configured model the request fails explicitly instead of reporting
// ready suggestions, and reading stays harmless.
func TestSuggestionRequiresConfiguredModel(t *testing.T) {
	ctx := context.Background()
	a, err := Open(ctx, Options{DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w, err := a.CreateWorld(ctx, "no-model", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Database().Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(2,'narrative-1','narrative','雨声敲着窗沿。','',?)`, wire.NowText()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Database().Exec(`UPDATE meta SET value='2' WHERE key='message_head'`); err != nil {
		t.Fatal(err)
	}
	store.Database().Close()
	snapshot, err := a.ReadWorld(ctx, w.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	status, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := SuggestionRequest{Basis: suggestionBasis(snapshot.Summary), ExpectedActiveRevision: status.ActiveRevision}
	if _, err = a.RequestSuggestions(ctx, w.WorldID, req); !errors.Is(err, ErrModelNotConfigured) {
		t.Fatalf("unconfigured model: %v", err)
	}
	set, err := a.ReadSuggestions(ctx, w.WorldID)
	if err != nil || set.Status == "ready" || len(set.Items) != 0 {
		t.Fatalf("read reported ready without a model: %+v %v", set, err)
	}
}

// Suggestion calls are metered under their own purpose, and reads never add usage.
func TestSuggestionUsageIsMetered(t *testing.T) {
	ctx := context.Background()
	g := &suggestionProbe{requests: make(chan model.TextRequest, 2)}
	a, w, req := suggestionWorldFixture(t, g)
	if _, err := a.RequestSuggestions(ctx, w.WorldID, req); err != nil {
		t.Fatal(err)
	}
	set := awaitSuggestion(t, a, w.WorldID)
	if set.Status != "ready" {
		t.Fatalf("set=%+v", set)
	}
	page, err := a.ReadUsage(ctx, w.WorldID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Totals.Calls != 1 || len(page.Calls) != 1 || page.Calls[0].Purpose != "suggestions" {
		t.Fatalf("usage: %+v %+v", page.Totals, page.Calls)
	}
	if _, err := a.ReadSuggestions(ctx, w.WorldID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadWorld(ctx, w.WorldID, 20); err != nil {
		t.Fatal(err)
	}
	after, err := a.ReadUsage(ctx, w.WorldID, 0)
	if err != nil || after.Totals.Calls != 1 {
		t.Fatalf("reads added usage: %+v %v", after.Totals, err)
	}
}
