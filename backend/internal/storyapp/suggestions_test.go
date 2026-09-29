package storyapp

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

type suggestionProbe struct {
	requests chan model.TextRequest
	release  chan struct{}
	calls    atomic.Int32
	invalid  bool
}

func (g *suggestionProbe) GenerateText(ctx context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.calls.Add(1)
	select {
	case g.requests <- r:
	default:
	}
	if g.release != nil {
		select {
		case <-g.release:
		case <-ctx.Done():
			return model.TextResponse{}, ctx.Err()
		}
	}
	if g.invalid {
		return model.TextResponse{Text: `{"items":["one"]}`}, nil
	}
	return model.TextResponse{Text: `{"items":["我向老板打招呼。","我看看窗外。","我在一旁稍作停留。"]}`}, nil
}

func suggestionWorldFixture(t *testing.T, g model.TextGenerator) (*App, wiaworld.WorldSummary, SuggestionRequest) {
	t.Helper()
	a := newTestApp(t, g)
	w, err := a.CreateWorld(context.Background(), "suggestions", "guided", "旅人", "寻找答案", true)
	if err != nil {
		t.Fatal(err)
	}
	status, err := a.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return a, w, SuggestionRequest{Basis: suggestionBasis(w), ExpectedActiveRevision: status.ActiveRevision}
}
func awaitSuggestion(t *testing.T, a *App, id string) SuggestionSet {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		set, err := a.ReadSuggestions(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if set.Status != "generating" {
			return set
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("suggestions did not finish")
	return SuggestionSet{}
}

func TestSuggestionsIdempotentScopedAndNotHistory(t *testing.T) {
	g := &suggestionProbe{requests: make(chan model.TextRequest, 2), release: make(chan struct{})}
	a, w, req := suggestionWorldFixture(t, g)
	ctx := context.Background()
	set, err := a.RequestSuggestions(ctx, w.WorldID, req)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := a.RequestSuggestions(ctx, w.WorldID, req)
	if err != nil || duplicate.ID != set.ID {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	request := <-g.requests
	if !strings.Contains(request.System, "玩家行动建议助手") || strings.Contains(request.Input, "作者事实") || strings.Contains(request.Input, "初始关切") || strings.Contains(request.Input, "knowledge") {
		t.Fatal("wrong projection")
	}
	close(g.release)
	set = awaitSuggestion(t, a, w.WorldID)
	if set.Status != "ready" || len(set.Items) != 3 || g.calls.Load() != 1 {
		t.Fatalf("result: %+v calls=%d", set, g.calls.Load())
	}
	after, err := a.ReadWorld(ctx, w.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if after.Summary.MessageHead != w.MessageHead || after.Summary.EventHead != w.EventHead || len(after.Messages) != 1 {
		t.Fatal("suggestions became history")
	}
	set, err = a.RequestSuggestions(ctx, w.WorldID, req)
	if err != nil || set.Status != "ready" || g.calls.Load() != 1 {
		t.Fatal("repeated paid generation")
	}
	off := false
	req.Enabled = &off
	set, err = a.RequestSuggestions(ctx, w.WorldID, req)
	if err != nil || set.Status != "disabled" {
		t.Fatal(err, set)
	}
	set, err = a.ReadSuggestions(ctx, w.WorldID)
	if err != nil || set.Enabled {
		t.Fatal("preference lost")
	}
}

func TestSuggestionsDiscardLateEpochAndWorldResponses(t *testing.T) {
	for _, mode := range []string{"epoch", "switch", "delete", "disable"} {
		t.Run(mode, func(t *testing.T) {
			g := &suggestionProbe{requests: make(chan model.TextRequest, 2), release: make(chan struct{})}
			a, w, req := suggestionWorldFixture(t, g)
			ctx := context.Background()
			if _, err := a.RequestSuggestions(ctx, w.WorldID, req); err != nil {
				t.Fatal(err)
			}
			<-g.requests
			switch mode {
			case "epoch":
				path, _, _ := a.worldRecord(ctx, w.WorldID)
				store, err := storage.OpenWorldDB(path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.Database().Exec(`UPDATE meta SET value=CAST(value AS INTEGER)+1 WHERE key='context_epoch'`)
				store.Database().Close()
				if err != nil {
					t.Fatal(err)
				}
			case "switch":
				other, err := a.CreateWorld(ctx, "other", "guided", "旅人", "", false)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.ActivateWorld(ctx, other.WorldID, req.ExpectedActiveRevision); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := a.DeleteWorld(ctx, w.WorldID, req.ExpectedActiveRevision); err != nil {
					t.Fatal(err)
				}
			case "disable":
				off := false
				req.Enabled = &off
				if _, err := a.RequestSuggestions(ctx, w.WorldID, req); err != nil {
					t.Fatal(err)
				}
			}
			close(g.release)
			a.copyWG.Wait()
			if mode == "delete" {
				if _, err := a.ReadSuggestions(ctx, w.WorldID); err == nil {
					t.Fatal("deleted world resurrected")
				}
				return
			}
			set, err := a.ReadSuggestions(ctx, w.WorldID)
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Items) > 0 || set.Status == "ready" {
				t.Fatalf("late result published: %+v", set)
			}
		})
	}
}

func TestSuggestionsFailedInputExcludedAndFailuresBounded(t *testing.T) {
	g := &suggestionProbe{requests: make(chan model.TextRequest, 5), invalid: true}
	a, w, req := suggestionWorldFixture(t, g)
	ctx := context.Background()
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	// A failed displayed input is not a narrative or a personal-memory source.
	_, err = store.Database().Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(2,'failed-input','player','FAILED_SECRET','failed-run',?)`, wire.NowText())
	if err != nil {
		t.Fatal(err)
	}
	store.Database().Close()
	if _, err := a.RequestSuggestions(ctx, w.WorldID, req); err != nil {
		t.Fatal(err)
	}
	if g.calls.Load() != 0 {
		t.Fatal("generated against uncommitted input")
	}
	store, err = storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Database().Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(3,'new-narrative','narrative','公开观察','',?)`, wire.NowText())
	store.Database().Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RequestSuggestions(ctx, w.WorldID, req); err != nil {
		t.Fatal(err)
	}
	set := awaitSuggestion(t, a, w.WorldID)
	if set.Status != "failed" || g.calls.Load() != 2 {
		t.Fatalf("bounded failure: %+v calls=%d", set, g.calls.Load())
	}
	for len(g.requests) > 0 {
		r := <-g.requests
		if strings.Contains(r.Input, "FAILED_SECRET") {
			t.Fatal("failed input leaked")
		}
	}
	if _, err := a.RequestSuggestions(ctx, w.WorldID, req); err != nil {
		t.Fatal(err)
	}
	if g.calls.Load() != 2 {
		t.Fatal("failure automatically retried")
	}
}

func TestSuggestionComposerHidesOtherPeoplesKnowledge(t *testing.T) {
	s := worldSnapshot{PlayerName: "你", Definition: gameDefinition{Background: "公开背景", Secret: "AUTHOR_SECRET"}, Characters: []wiaworld.Character{{EntityID: "npc:a", Name: "甲", Knowledge: "NPC_SECRET", Profile: "PRIVATE_PROFILE", InScene: true}}, LongMemory: map[string]memoryContext{"npc:a": {Digest: memorymodel.MemoryDigest{Content: "PRIVATE_DIGEST"}}, "player": {Digest: memorymodel.MemoryDigest{Content: "玩家回顾"}, Tail: []memorymodel.MemorySource{{ID: "visible", Content: "公开信息"}}}}}
	m := composeSuggestions(s)
	raw, _ := json.Marshal(m)
	for _, secret := range []string{"AUTHOR_SECRET", "NPC_SECRET", "PRIVATE_PROFILE", "PRIVATE_DIGEST"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret exposed", secret)
		}
	}
}

func TestSuggestionsPreferenceCopyAndRestart(t *testing.T) {
	g := &suggestionProbe{requests: make(chan model.TextRequest, 2)}
	a, w, req := suggestionWorldFixture(t, g)
	ctx := context.Background()
	off := false
	req.Enabled = &off
	if _, err := a.RequestSuggestions(ctx, w.WorldID, req); err != nil {
		t.Fatal(err)
	}
	op, err := a.SaveAs(ctx, w.WorldID, "branch", "suggestion-copy", req.ExpectedActiveRevision)
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
		t.Fatal(op.Status)
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
	for _, id := range []string{w.WorldID, op.TargetWorldID} {
		set, err := reopened.ReadSuggestions(ctx, id)
		if err != nil || set.Enabled || set.Status != "disabled" || set.Basis.WorldID != id {
			t.Fatal(set, err)
		}
	}
	if g.calls.Load() != 0 {
		t.Fatal("copy or restart generated suggestions")
	}
}
