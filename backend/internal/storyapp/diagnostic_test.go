package storyapp

import (
	"context"
	"errors"
	"fmt"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/storage"
	wiaworld "gameagent/backend/internal/world"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestDiagnosticClassificationAndPrivateFailure(t *testing.T) {
	for _, tc := range []struct{ code, reason string }{
		{"network", "model_connection_failed"}, {"provider_http", "model_service_failed"},
		{"empty_response", "model_empty_response"}, {"output_incomplete", "model_output_incomplete"},
		{"invalid_response", "model_invalid_response"}, {"timeout", "generation_timeout"},
	} {
		err := atTurnStage(turnStageNarration, &model.TextCallError{Diagnostic: model.TextDiagnostic{Code: tc.code, HTTPStatus: 503}, Cause: errors.New("PRIVATE_PROMPT_AND_KEY")})
		_, reason, message := classifyTurnFailure(err)
		if reason != tc.reason || strings.Contains(message, "PRIVATE") {
			t.Fatalf("%s %s", reason, message)
		}
		logger := &recordingLogger{}
		a := newTestApp(t, &scriptedGenerator{})
		a.logger = logger
		a.logRunFailure("world", wiaworld.Run{RunID: "run"}, "narration", reason, err)
		if strings.Contains(logger.String(), "PRIVATE") || !strings.Contains(logger.String(), tc.code) {
			t.Fatal(logger.String())
		}
	}
}

func TestDiagnosticLogRotationAndConcurrentWrites(t *testing.T) {
	w, err := OpenDiagnosticLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w.limit = 100
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := w.Write([]byte("diagnostic line\n")); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", ".1", ".2"} {
		info, err := os.Stat(w.path + suffix)
		if err != nil || info.Size() > 100 {
			t.Fatalf("rotation %s %v", suffix, err)
		}
	}
	if _, err := w.Write([]byte("closed")); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func TestFindRunByRequestIsWorldScopedAndBeyondRecentWindow(t *testing.T) {
	a := newTestApp(t, &scriptedGenerator{})
	ctx := context.Background()
	w, _ := a.createFixtureWorld(ctx, "lookup", "guided", "旅人", "", true)
	r, err := a.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "first-key", Input: "问候"})
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, a, w.WorldID, r.RunID)
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	s, err := storage.OpenWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Database().Close()
	for i := 0; i < 55; i++ {
		_, err = s.Database().Exec(`INSERT INTO runs(run_id,request_key,request_hash,input,addressee_id,attempt,status,created_at,updated_at) VALUES(?,?,'hash','test','',1,'failed','2099-01-01T00:00:00Z','2099-01-01T00:00:00Z')`, fmt.Sprint(i), fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	found, err := a.ListRuns(ctx, w.WorldID, "first-key")
	if err != nil || len(found) != 1 || found[0].RunID != r.RunID {
		t.Fatalf("%+v %v", found, err)
	}
	other, _ := a.createFixtureWorld(ctx, "other", "guided", "旅人", "", false)
	found, err = a.ListRuns(ctx, other.WorldID, "first-key")
	if err != nil || len(found) != 0 {
		t.Fatalf("cross-world: %+v %v", found, err)
	}
}
