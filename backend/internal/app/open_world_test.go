package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gameagent/backend/internal/memory"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func deferredOpenWorldResponse(request model.TextRequest) (model.TextResponse, error) {
	match := regexp.MustCompile(`来源 (material:[a-zA-Z0-9._:-]+)`).FindStringSubmatch(request.Input)
	if len(match) != 2 {
		return model.TextResponse{}, errors.New("provided world material source missing")
	}
	body, err := json.Marshal(map[string]any{"status": "deferred", "content": "当前事实尚未形成新的外部变化。", "source_ids": []string{match[1]}, "projections": []any{}, "decision_requests": []string{}, "ending": ""})
	return model.TextResponse{Text: string(body)}, err
}

type planReviewGenerator struct{ base stageBItemGenerator }

func (g planReviewGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	if strings.Contains(request.System, "重要 NPC") && strings.Contains(request.Input, "阶段：5") {
		marker := "你自己的存档当前计划（状态包括 active/paused/completed/cancelled；检查时间只是重估时间）："
		start := strings.Index(request.Input, marker)
		if start < 0 {
			return model.TextResponse{}, errors.New("current owned plan missing")
		}
		var plans []wiaworld.PersonalPlan
		text := strings.SplitN(request.Input[start+len(marker):], "\n", 2)[0]
		if json.Unmarshal([]byte(text), &plans) != nil || len(plans) == 0 {
			return model.TextResponse{}, errors.New("current owned plan invalid")
		}
		match := regexp.MustCompile(`本阶段输入来源ID\(JSON\)：(\[[^\n]+\])`).FindStringSubmatch(request.Input)
		var sources []string
		if len(match) != 2 || json.Unmarshal([]byte(match[1]), &sources) != nil {
			return model.TextResponse{}, errors.New("owned review stimulus missing")
		}
		body, err := json.Marshal(map[string]any{"speech": "", "action_intent": "", "silent": true, "memory": "依据当前线索暂停这项安排。", "relationship_proposals": []any{}, "plan_updates": []map[string]any{{"id": plans[0].ID, "content": "已经重估；本人取消原安排，等待新的实际线索。", "source_ids": sources, "status": "cancelled", "review_after_minutes": 0}}})
		return model.TextResponse{Text: string(body)}, err
	}
	return g.base.GenerateText(ctx, request)
}

func TestV4PlansAndMaterialsFreezeCopyRestartAndProtectConsumedSources(t *testing.T) {
	ctx := context.Background()
	packRoot := packFixture(t, "mist-embers")
	path := filepath.Join(packRoot, "narrative", "progression.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var progression map[string]any
	if err = json.Unmarshal(body, &progression); err != nil {
		t.Fatal(err)
	}
	for _, plan := range progression["initial_plans"].([]any) {
		plan.(map[string]any)["review_after_minutes"] = 1
	}
	body, _ = json.Marshal(progression)
	if err = os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
	rewritePack(t, packRoot, func(p map[string]any) { p["revision"] = "mist-plans-test.v1" })
	dataRoot := t.TempDir()
	a, err := Open(ctx, Options{DataRoot: dataRoot, UserID: LocalUserID, StoryPacksPath: filepath.Dir(packRoot), Generator: planReviewGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	game, err := a.Game("mist-embers")
	if err != nil {
		t.Fatal(err)
	}
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: game.ID, ExpectedRevision: game.Revision, RequestKey: "create-plans", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.SubmitRun(ctx, world.WorldID, RunRequest{Input: "我赶到废弃诊所并拾起银镜。", RequestKey: "change-site"})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a, world.WorldID, run.RunID); done.Status != "completed" {
		t.Fatalf("due review failed: %s", done.Reason)
	}
	snapshot := readContextSnapshot(t, a, world.WorldID)
	if snapshot.OpenProgress == nil || len(snapshot.OpenProgress.Plans) != 2 || len(snapshot.Definition.Materials) != 19 {
		t.Fatal("frozen material or plan state missing")
	}
	for _, plan := range snapshot.OpenProgress.Plans {
		if plan.Status != "cancelled" || plan.Version != 2 || len(plan.SourceIDs) != 1 || !strings.Contains(plan.SourceIDs[0], ":plan-review:projection:") {
			t.Fatalf("owned plan update was not committed: %+v", plan)
		}
		_, err = a.Correct(ctx, world.WorldID, memory.CorrectionRequest{RequestKey: "correct-" + plan.ID, ExpectedEpoch: snapshot.Summary.ContextEpoch, Kind: "event", Scope: "author", TargetID: plan.SourceIDs[0], Replacement: "删除原有检查依据。"})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("consumed plan source correction accepted: %v", err)
		}
		foundPerception := false
		view, readErr := a.ReadMemory(ctx, world.WorldID, plan.OwnerID, true, 0)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, record := range view.Sources {
			if record.EventID == plan.SourceIDs[0] && strings.HasPrefix(record.Kind, "perception:") {
				foundPerception = true
				_, err = a.Correct(ctx, world.WorldID, memory.CorrectionRequest{RequestKey: "correct-personal-" + plan.ID, ExpectedEpoch: snapshot.Summary.ContextEpoch, Kind: "perception", Scope: plan.OwnerID, TargetID: record.ID, Replacement: "我没有收到这项检查依据。"})
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("consumed personal basis corrected: %v", err)
				}
			}
		}
		if !foundPerception {
			t.Fatal("plan owner perception source missing")
		}
	}
	status, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := a.SaveAs(ctx, world.WorldID, "计划分支", wire.NewID("copy"), status.ActiveRevision)
	if err != nil || copy.Status != "ready" {
		t.Fatalf("copy=%+v err=%v", copy, err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	// The source package can disappear after freezing; neither save consults it.
	if err = os.RemoveAll(packRoot); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: dataRoot, UserID: LocalUserID, Generator: planReviewGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, id := range []string{world.WorldID, copy.TargetWorldID} {
		saved := readContextSnapshot(t, reopened, id)
		if saved.Definition.Revision != "mist-plans-test.v1" || len(saved.Definition.Materials) != 19 || saved.OpenProgress.Plans[0].Status != "cancelled" || saved.Items["mirror-3-917"].HolderID != "player" {
			t.Fatal("copy/restart lost frozen materials, current plan or item placement")
		}
	}
}
