package storyapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"gameagent/backend/internal/wire"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes renders a real image so package asset validation exercises decoding.
func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 0x40, A: 0xff})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func publishableDraft(t *testing.T, a *App, gameID string) (ContentProject, ContentDraft) {
	t.Helper()
	ctx := context.Background()
	project, err := a.CreateContentProject(ctx, gameID, "可发布内容")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := a.CreateContentDraft(ctx, project.ProjectID, "")
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
	payload.NPCs = []ContentDraftNPC{{
		DefinitionID: "keeper", Revision: "v1", EntityID: "npc:keeper", Name: "看灯人", Role: "港口看灯人",
		Profile: "守着潮汐表。", Knowledge: "知道今晚谁该来。", InitialConcerns: "别让灯灭。", InitialLocation: "harbor",
		SpeakingExamples: []string{"灯要按时点。"},
	}}
	payload.Bystanders = []PackBystander{{BystanderID: "bystander:boatman", Name: "船夫", Description: "在栈桥等活", InitialLocation: "harbor"}}
	saved, err := a.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	return project, saved
}
func TestPublishContentDraftIsImmutableAndResumable(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-publish")
	request := PublishRequest{RequestKey: "publish-1", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version, ExpectedProjectVersion: project.Version}
	operation, err := a.PublishContentDraft(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != "succeeded" || operation.Stage != publishReady {
		t.Fatalf("operation: %+v", operation)
	}
	var revision, digest, path string
	if err = a.appDB.QueryRowContext(ctx, `SELECT revision,digest,path FROM content_revisions WHERE user_id=? AND game_id=?`, a.userID, "harbor-publish").Scan(&revision, &digest, &path); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(revision, "r-") || digest == "" || path == "" {
		t.Fatalf("revision record: %q %q %q", revision, digest, path)
	}
	if _, err = os.Stat(filepath.Join(path, "story.json")); err != nil {
		t.Fatal("published package is missing story.json", err)
	}
	if _, err = os.Stat(filepath.Join(path, "npcs", "keeper.json")); err != nil {
		t.Fatal("published package is missing the character file", err)
	}

	// The catalog exposes the revision and a world can start from it.
	summary, err := a.Game("harbor-publish")
	if err != nil || summary.Revision != revision || summary.Title != "港口的灯" {
		t.Fatalf("catalog: %+v %v", summary, err)
	}
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: "harbor-publish", ExpectedRevision: revision, RequestKey: "harbor-world", Name: "港口之夜", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.ReadWorld(ctx, world.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Definition.Summary.Revision != revision || snapshot.Definition.Background != "港口在夜里退潮。" {
		t.Fatalf("world did not freeze the published revision: %+v", snapshot.Definition.Summary)
	}
	if len(snapshot.Definition.Characters) != 1 || snapshot.Definition.Characters[0].Name != "看灯人" {
		t.Fatalf("world characters: %+v", snapshot.Definition.Characters)
	}
	// Author-created content keeps the lead adjustable in the worlds it starts.
	if !snapshot.Definition.Summary.Player.Editable {
		t.Fatal("published content locked the lead character")
	}
	if _, err = a.UpdatePlayerProfile(ctx, world.WorldID, UpdatePlayerProfileRequest{PlayerName: "陆舟", PlayerProfile: "夜行客", ExpectedContextEpoch: snapshot.Summary.ContextEpoch}); err != nil {
		t.Fatalf("lead profile is not editable in published content: %v", err)
	}

	// A repeated key returns the original result instead of publishing again.
	repeated, err := a.PublishContentDraft(ctx, request)
	if err != nil || repeated.OperationID != operation.OperationID || repeated.Status != "succeeded" {
		t.Fatalf("repeat: %+v %v", repeated, err)
	}
	var count int
	if err = a.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_revisions WHERE user_id=? AND game_id=?`, a.userID, "harbor-publish").Scan(&count); err != nil || count != 1 {
		t.Fatalf("second revision published: %d %v", count, err)
	}
	conflicting := request
	conflicting.ExpectedProjectVersion = project.Version + 3
	if _, err = a.PublishContentDraft(ctx, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different payload accepted for the same key: %v", err)
	}

	// Project bookkeeping moved forward exactly once.
	updated, _, err := a.ReadContentProject(ctx, project.ProjectID)
	if err != nil || updated.CurrentRevision != revision || updated.Version != project.Version+1 {
		t.Fatalf("project: %+v %v", updated, err)
	}

	// Publishing again from the same unchanged draft state is an explicit conflict.
	again := PublishRequest{RequestKey: "publish-2", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version, ExpectedProjectVersion: project.Version}
	if _, err = a.PublishContentDraft(ctx, again); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale project version accepted: %v", err)
	}

	// A restart keeps the published revision available without republishing.
	root := a.DataRoot()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{DataRoot: root, Generator: &scriptedGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if summary, err = reopened.Game("harbor-publish"); err != nil || summary.Revision != revision {
		t.Fatalf("catalog after restart: %+v %v", summary, err)
	}
}

func TestPublishRejectsInvalidDraftAndProgressesStages(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-stages")

	// Missing required package fields fail before any revision is registered.
	broken := draft.Payload
	broken.Locations = []PackLocation{}
	broken.InitialLocation = ""
	broken.NPCs = []ContentDraftNPC{}
	broken.Bystanders = []PackBystander{}
	if _, err := a.SaveContentDraft(ctx, draft.DraftID, broken, draft.Version); err != nil {
		t.Fatal(err)
	}
	reloaded, err := a.ReadContentDraft(ctx, draft.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "bad", DraftID: draft.DraftID, ExpectedDraftVersion: reloaded.Version, ExpectedProjectVersion: project.Version}); err == nil {
		t.Fatal("invalid draft published")
	}
	entries, err := os.ReadDir(a.contentRoot())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".staging") {
			t.Fatalf("failed publication left staging behind: %v", entry.Name())
		}
	}

	// A staged directory left by an interrupted process is discarded on recovery.
	stale := filepath.Join(a.contentRoot(), "publish_stale.staging")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_operations(user_id,request_key,request_hash,operation_id,kind,target_id,stage,status,result_json,safe_error,created_at,updated_at)
		VALUES(?,'interrupted','hash','publish_stale','publish',?,'files_written','running','','',?,?)`, a.userID, draft.DraftID, wire.NowText(), wire.NowText()); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverContentOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery kept an incomplete staging directory: %v", err)
	}
	var status string
	if err = a.appDB.QueryRowContext(ctx, `SELECT status FROM content_operations WHERE user_id=? AND operation_id='publish_stale'`, a.userID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("recovery status: %q %v", status, err)
	}
	var revisions int
	if err = a.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_revisions WHERE user_id=?`, a.userID).Scan(&revisions); err != nil || revisions != 0 {
		t.Fatalf("recovery registered a revision: %d %v", revisions, err)
	}

	// The same draft publishes normally afterwards and reports the ready stage.
	reloaded, err = a.ReadContentDraft(ctx, draft.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	fixed := reloaded.Payload
	fixed.Locations = []PackLocation{{ID: "harbor", Name: "港口", Connections: []string{}}}
	fixed.InitialLocation = "harbor"
	fixed.NPCs = []ContentDraftNPC{{
		DefinitionID: "keeper", Revision: "v1", EntityID: "npc:keeper", Name: "看灯人", Role: "港口看灯人",
		Profile: "守着潮汐表。", InitialLocation: "harbor",
	}}
	saved, err := a.SaveContentDraft(ctx, draft.DraftID, fixed, reloaded.Version)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := a.PublishContentDraft(ctx, PublishRequest{RequestKey: "good", DraftID: saved.DraftID, ExpectedDraftVersion: saved.Version, ExpectedProjectVersion: project.Version})
	if err != nil || operation.Stage != publishReady || operation.Status != "succeeded" {
		t.Fatalf("publish after recovery: %+v %v", operation, err)
	}
}

// A referenced asset must exist as a staged draft asset, and the published package
// carries the bytes.
func TestPublishCarriesReferencedAssets(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-assets")
	payload := draft.Payload
	payload.Cover = "assets/cover.png"
	saved, err := a.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "no-asset", DraftID: saved.DraftID, ExpectedDraftVersion: saved.Version, ExpectedProjectVersion: project.Version}); !errors.Is(err, ErrContentInvalid) {
		t.Fatalf("missing asset accepted: %v", err)
	}
	// Stage the asset the way the upload endpoint will.
	body := pngBytes(t, 8, 8)
	staged := filepath.Join(a.DataRoot(), "staged-cover.png")
	if err = os.WriteFile(staged, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_draft_assets(user_id,draft_id,asset_id,relative_name,media_type,byte_size,width,height,digest,staged_path,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, a.userID, saved.DraftID, "asset_cover", "assets/cover.png", "image/png", len(body), 1, 1, "digest", staged, wire.NowText()); err != nil {
		t.Fatal(err)
	}
	operation, err := a.PublishContentDraft(ctx, PublishRequest{RequestKey: "with-asset", DraftID: saved.DraftID, ExpectedDraftVersion: saved.Version, ExpectedProjectVersion: project.Version})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != "succeeded" {
		t.Fatalf("operation: %+v", operation)
	}
	var path string
	if err = a.appDB.QueryRowContext(ctx, `SELECT path FROM content_revisions WHERE user_id=? AND game_id='harbor-assets'`, a.userID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(path, "assets", "cover.png"))
	if err != nil || len(copied) != len(body) {
		t.Fatalf("asset not published: %v", err)
	}
	// Publishing the same content again under a fresh key conflicts on the project
	// version instead of overwriting the immutable revision.
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "again", DraftID: saved.DraftID, ExpectedDraftVersion: saved.Version, ExpectedProjectVersion: project.Version}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("overwrite attempt: %v", err)
	}
}

// A world copies the images it started with, so a later republish or a deleted
// revision does not change what the world shows.
func TestWorldSnapshotKeepsItsOwnImages(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-snapshot")
	payload := draft.Payload
	payload.Cover = "assets/cover.png"
	payload.NPCs[0].Avatar = "assets/keeper.png"
	saved, err := a.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	cover := pngBytes(t, 12, 8)
	avatar := pngBytes(t, 6, 6)
	for id, asset := range map[string]struct {
		name string
		body []byte
	}{"asset_cover": {"assets/cover.png", cover}, "asset_avatar": {"assets/keeper.png", avatar}} {
		staged := filepath.Join(a.DataRoot(), id+".png")
		if err = os.WriteFile(staged, asset.body, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_draft_assets(user_id,draft_id,asset_id,relative_name,media_type,byte_size,width,height,digest,staged_path,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, a.userID, saved.DraftID, id, asset.name, "image/png", len(asset.body), 1, 1, "digest", staged, wire.NowText()); err != nil {
			t.Fatal(err)
		}
	}
	operation, err := a.PublishContentDraft(ctx, PublishRequest{RequestKey: "snapshot", DraftID: saved.DraftID, ExpectedDraftVersion: saved.Version, ExpectedProjectVersion: project.Version})
	if err != nil || operation.Status != "succeeded" {
		t.Fatalf("publish: %+v %v", operation, err)
	}
	var revision, revisionPath string
	if err = a.appDB.QueryRowContext(ctx, `SELECT revision,path FROM content_revisions WHERE user_id=? AND game_id='harbor-snapshot'`, a.userID).Scan(&revision, &revisionPath); err != nil {
		t.Fatal(err)
	}
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: "harbor-snapshot", ExpectedRevision: revision, RequestKey: "snapshot-world", Name: "港口镜像", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	worldPath, _, err := a.worldRecord(ctx, world.WorldID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cover.png", "keeper.png"} {
		if _, err = os.Stat(filepath.Join(filepath.Dir(worldPath), "assets", name)); err != nil {
			t.Fatalf("world is missing its own copy of %s: %v", name, err)
		}
	}
	store, err := openWorldDB(worldPath)
	if err != nil {
		t.Fatal(err)
	}
	coverAsset, err := metaGet(ctx, store.db, "cover_asset")
	if err != nil || coverAsset != "cover.png" {
		t.Fatalf("cover record: %q %v", coverAsset, err)
	}
	keeperAvatar, err := metaGet(ctx, store.db, "avatar:npc:keeper")
	if err != nil || keeperAvatar != "keeper.png" {
		t.Fatalf("avatar record: %q %v", keeperAvatar, err)
	}
	store.db.Close()

	// Removing the published revision does not affect the world's own copies.
	if err = os.RemoveAll(revisionPath); err != nil {
		t.Fatal(err)
	}
	body, err := a.worldAsset(worldPath, coverAsset)
	if err != nil || len(body) != len(cover) {
		t.Fatalf("world cover after revision removal: %d %v", len(body), err)
	}
	if body, err = a.worldAsset(worldPath, keeperAvatar); err != nil || len(body) != len(avatar) {
		t.Fatalf("world avatar after revision removal: %d %v", len(body), err)
	}
	// The reader refuses names that could leave the world directory.
	if _, err = a.worldAsset(worldPath, "../cover"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("traversal accepted: %v", err)
	}
	if _, err = a.worldAsset(worldPath, "missing.png"); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("missing asset: %v", err)
	}
}

// The published identity is stable: the same content always derives the same
// revision identity, and a changed field derives a different one.
func TestPackageRevisionIdentity(t *testing.T) {
	story := StoryPack{SchemaVersion: packSchemaV2, GameID: "harbor", Mode: "open", Title: "港口的灯", Description: "d", Gameplay: "g", Opening: "o", Clock: "第 1 日 19:00", InitialLocation: "harbor", Locations: []PackLocation{{ID: "harbor", Name: "港口"}}, Bystanders: []PackBystander{}}
	npcs := map[string]PackNPC{"npcs/keeper.json": {DefinitionID: "keeper", Revision: "v1", EntityID: "npc:keeper", Name: "看灯人", Role: "看灯人", Profile: "资料", InitialLocation: "harbor"}}
	first, err := newContentRevision(story, npcs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := newContentRevision(story, npcs, nil); err != nil || again != first {
		t.Fatalf("revision identity is not stable: %q %q %v", first, again, err)
	}
	if first == "" || !packID.MatchString(first) {
		t.Fatalf("revision identity: %q", first)
	}
	changed := story
	changed.Background = "改过的背景"
	if other, err := newContentRevision(changed, npcs, nil); err != nil || other == first {
		t.Fatalf("revision identity ignored a content change: %q %v", other, err)
	}
}

// The published package JSON is the canonical v2 object form.
func TestPublishedPackageUsesV2Objects(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	_, draft := publishableDraft(t, a, "harbor-shape")
	operation, err := a.PublishContentDraft(ctx, PublishRequest{RequestKey: "shape", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version, ExpectedProjectVersion: 1})
	if err != nil || operation.Status != "succeeded" {
		t.Fatalf("publish: %+v %v", operation, err)
	}
	var path string
	if err = a.appDB.QueryRowContext(ctx, `SELECT path FROM content_revisions WHERE user_id=? AND game_id='harbor-shape'`, a.userID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(path, "story.json"))
	if err != nil {
		t.Fatal(err)
	}
	var story map[string]any
	if err = json.Unmarshal(body, &story); err != nil {
		t.Fatal(err)
	}
	if story["schema_version"] != float64(2) || story["revision"] == "" {
		t.Fatalf("story.json: %+v", story["schema_version"])
	}
	bystanders, ok := story["bystanders"].([]any)
	if !ok || len(bystanders) != 1 {
		t.Fatalf("bystanders: %+v", story["bystanders"])
	}
	entry, ok := bystanders[0].(map[string]any)
	if !ok || entry["bystander_id"] != "bystander:boatman" {
		t.Fatalf("bystander shape: %+v", bystanders[0])
	}
	npcs, ok := story["npcs"].([]any)
	if !ok || len(npcs) != 1 || npcs[0] != "npcs/keeper.json" {
		t.Fatalf("npc references: %+v", story["npcs"])
	}
}
