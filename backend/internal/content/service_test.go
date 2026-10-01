package content

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/story"
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

func TestV3DraftPackageRoundTripPreservesSpatialContract(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	project, draft := publishableTestDraft(t, service, "spatial-round-trip")
	public := true
	payload := draft.Payload
	payload.SchemaVersion = SchemaV3
	payload.Requires = map[string]int{"spatial": 1, "state": 1, "relations": 1, "items": 1}
	payload.InitialLocation = "office"
	payload.Locations = []PackLocation{
		{ID: "district", Kind: "region", Name: "调查区", Connections: []string{}, Public: &public},
		{ID: "office", Kind: "place", Parent: "district", Name: "事务所", Connections: []string{"cafe"}, Public: &public},
		{ID: "cafe", Kind: "place", Parent: "district", Name: "咖啡馆", Connections: []string{"office"}, Public: &public},
	}
	payload.NPCs[0].InitialLocation = "cafe"
	payload.Player.InitialState = map[string]json.RawMessage{"focus": json.RawMessage("61")}
	payload.NPCs[0].InitialState = map[string]json.RawMessage{"focus": json.RawMessage("44")}
	minimum, maximum := 0, 100
	payload.StateDefinitions = []PackStateDefinition{{ID: "focus", Name: "专注", Type: "integer", Minimum: &minimum, Maximum: &maximum, Default: json.RawMessage("50"), Scope: "all", Projection: "self", Knowledge: "owner", UpdatePolicy: story.StateUpdatePolicy{Kind: "bounded_proposal", MaxChangePerTurn: 8}}}
	payload.RelationDefinitions = []PackRelationDefinition{{ID: "confidence", Name: "确信", Minimum: -10, Maximum: 10, Default: 0, MaxChangePerTurn: 2, Projection: "hidden"}}
	payload.InitialRelations = []PackInitialRelation{{SubjectID: "npc:keeper", TargetID: "player", RelationType: "confidence", Value: 1}}
	payload.ItemDefinitions = []PackItemDefinition{{ID: "lamp-key", Name: "灯钥匙", Projection: "holder"}}
	payload.ItemInstances = []PackItemInstance{{InstanceID: "lamp-key-1", DefinitionID: "lamp-key", HolderID: "player"}}
	payload.Bystanders[0].InitialLocation = "office"
	saved, err := service.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	_, files, _, err := service.buildPackage(ctx, saved, project)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, body := range files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pack, err := loadPack(root)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := service.draftPayloadFromLoadedPack(pack, project.GameID)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.SchemaVersion != SchemaV3 || roundTrip.Requires["spatial"] != 1 || roundTrip.InitialLocation != "office" {
		t.Fatalf("v3 identity changed: %+v", roundTrip)
	}
	if len(roundTrip.Locations) != 3 || roundTrip.Locations[0].Kind != "region" || roundTrip.Locations[1].Parent != "district" || roundTrip.Locations[1].Public == nil || !*roundTrip.Locations[1].Public {
		t.Fatalf("v3 locations changed: %+v", roundTrip.Locations)
	}
	if len(roundTrip.NPCs) != 1 || roundTrip.NPCs[0].InitialLocation != "cafe" || len(roundTrip.Bystanders) != 1 || roundTrip.Bystanders[0].InitialLocation != "office" {
		t.Fatalf("v3 entity positions changed: npcs=%+v bystanders=%+v", roundTrip.NPCs, roundTrip.Bystanders)
	}
	if string(roundTrip.Player.InitialState["focus"]) != "61" || string(roundTrip.NPCs[0].InitialState["focus"]) != "44" || len(roundTrip.StateDefinitions) != 1 || len(roundTrip.RelationDefinitions) != 1 || len(roundTrip.ItemInstances) != 1 {
		t.Fatalf("v3 mechanics changed: %+v", roundTrip)
	}
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
