package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gameagent/backend/internal/model"
	"gameagent/backend/internal/wire"
)

type waitResultGenerator struct{ host hostResult }

func (g waitResultGenerator) GenerateText(context.Context, model.TextRequest) (model.TextResponse, error) {
	return model.TextResponse{Text: wire.MarshalJSON(g.host)}, nil
}

type overflowingWaitGenerator struct{ base actionConsistencyGenerator }

func (g overflowingWaitGenerator) GenerateText(ctx context.Context, req model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(req.System, "结构化回合意图") {
		return model.TextResponse{Text: `{"intent_type":"act","addressee_id":"","visibility":"public","wait_minutes":5}`}, nil
	}
	if strings.Contains(req.System, "场景协调 Agent") {
		response, err := (actionConsistencyGenerator{status: "succeeded"}).GenerateText(ctx, req)
		if err != nil {
			return response, err
		}
		var host hostResult
		if err = json.Unmarshal([]byte(response.Text), &host); err != nil {
			return model.TextResponse{}, err
		}
		host.TimeMinutes = 30
		return model.TextResponse{Text: wire.MarshalJSON(host)}, nil
	}
	return g.base.GenerateText(ctx, req)
}

func TestExcessWaitRollsBackWholeTurnWithoutPlot(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, overflowingWaitGenerator{})
	withoutWorldEvents(app)
	w, err := app.CreateStoryWorld(ctx, CreateWorldRequest{GameID: "orbital-repair", ExpectedRevision: testPack(app, "orbital-repair").Definition.Revision, RequestKey: "wait", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	before := readContextSnapshot(t, app, w.WorldID)
	r, err := app.SubmitRun(ctx, w.WorldID, RunRequest{RequestKey: "wait-overflow", Input: "只等五分钟"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, app, w.WorldID, r.RunID); done.Status != "failed" {
		t.Fatal(done)
	}
	after := readContextSnapshot(t, app, w.WorldID)
	if before.Summary.Clock != after.Summary.Clock || before.Summary.EventHead != after.Summary.EventHead || wire.MarshalJSON(before.Messages) != wire.MarshalJSON(after.Messages) {
		t.Fatal("failed wait partially committed")
	}
}
