package content

import (
	"context"
	"path/filepath"
	"testing"

	"gameagent/backend/internal/storage"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	db, err := storage.OpenAppDB(filepath.Join(root, "app.db"), DatabaseSchema)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{Root: root, UserID: "local", DB: db})
	if err = service.Initialize(context.Background(), ""); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return service
}

func publishableTestDraft(t *testing.T, service *Service, gameID string) (ContentProject, ContentDraft) {
	t.Helper()
	ctx := context.Background()
	project, err := service.CreateContentProject(ctx, gameID, "可发布内容")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := service.CreateContentDraft(ctx, project.ProjectID, "")
	if err != nil {
		t.Fatal(err)
	}
	payload := draft.Payload
	payload.Title = "港口的灯"
	payload.Description = "潮汐小镇的夜班。"
	payload.Gameplay = "在退潮前决定帮谁。"
	payload.Background = "港口在夜里退潮。"
	payload.Opening = "潮水退去，灯还亮着。"
	payload.Player = PlayerDefaults{Name: "旅人", Profile: "来到港口的外乡人。", Editable: true}
	payload.Clock = "第 1 日 19:00"
	payload.InitialLocation = "harbor"
	payload.Locations = []PackLocation{{ID: "harbor", Name: "港口", Connections: []string{}}}
	payload.NPCs = []ContentDraftNPC{{DefinitionID: "keeper", Revision: "v1", EntityID: "npc:keeper", Name: "看灯人", Role: "港口看灯人", Profile: "守着潮汐表。", Knowledge: "知道今晚谁该来。", InitialConcerns: "别让灯灭。", InitialLocation: "harbor", SpeakingExamples: []string{"灯要按时点。"}}}
	payload.Bystanders = []PackBystander{{BystanderID: "bystander:boatman", Name: "船夫", Description: "在栈桥等活", InitialLocation: "harbor"}}
	saved, err := service.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	return project, saved
}
