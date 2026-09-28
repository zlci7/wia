package storyapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestContentProjectAndDraftLifecycle(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	if projects, err := a.ListContentProjects(ctx); err != nil || len(projects) != 0 {
		t.Fatalf("empty catalog: %+v %v", projects, err)
	}
	for _, bad := range []struct{ id, title string }{{"", "标题"}, {"bad id", "标题"}, {"ok-id", ""}, {"ok-id", strings.Repeat("长", 121)}} {
		if _, err := a.CreateContentProject(ctx, bad.id, bad.title); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid project accepted: %+v %v", bad, err)
		}
	}
	// Official identities are not takeable.
	if _, err := a.CreateContentProject(ctx, GameID, "冒充官方"); !errors.Is(err, ErrContentInvalid) {
		t.Fatalf("official game_id accepted: %v", err)
	}
	project, err := a.CreateContentProject(ctx, "harbor-lights", "港口的灯")
	if err != nil {
		t.Fatal(err)
	}
	if project.ProjectID == "" || project.GameID != "harbor-lights" || project.CurrentRevision != "" || project.Version != 1 {
		t.Fatalf("project: %+v", project)
	}
	if _, err = a.CreateContentProject(ctx, "harbor-lights", "重复身份"); !errors.Is(err, ErrContentBusy) {
		t.Fatalf("duplicate game_id accepted: %v", err)
	}

	blank, err := a.CreateContentDraft(ctx, project.ProjectID, "")
	if err != nil {
		t.Fatal(err)
	}
	if blank.Version != 1 || blank.Status != draftStatusEditing || blank.Payload.GameID != "harbor-lights" || blank.Payload.Mode != "open" {
		t.Fatalf("blank draft: %+v", blank)
	}
	if !blank.Payload.Player.Editable {
		t.Fatal("new content must start with a lead the player may adjust")
	}
	if draft, err := a.ReadContentDraft(ctx, blank.DraftID); err != nil || draft.Version != 1 || draft.Payload.Title != "港口的灯" {
		t.Fatalf("read draft: %+v %v", draft, err)
	}
	_, drafts, err := a.ReadContentProject(ctx, project.ProjectID)
	if err != nil || len(drafts) != 1 || drafts[0].DraftID != blank.DraftID {
		t.Fatalf("project drafts: %+v %v", drafts, err)
	}

	payload := blank.Payload
	payload.Title = "港口的灯 · 修订"
	payload.Description = "一处靠潮汐生活的港口。"
	payload.Background = "港口在夜里退潮。"
	payload.Opening = "潮水退去，灯还亮着。"
	payload.NPCs = []ContentDraftNPC{{DefinitionID: "keeper", Revision: "v1", EntityID: "npc:keeper", Name: "看灯人", Role: "港口看灯人", Profile: "守着潮汐表。", InitialLocation: "harbor"}}
	if len(payload.Locations) == 0 {
		payload.Locations = []PackLocation{{ID: "harbor", Name: "港口", Connections: []string{}}}
	}
	if payload.InitialLocation == "" {
		payload.InitialLocation = payload.Locations[0].ID
	}
	saved, err := a.SaveContentDraft(ctx, blank.DraftID, payload, 1)
	if err != nil || saved.Version != 2 || saved.Payload.Title != "港口的灯 · 修订" {
		t.Fatalf("save draft: %+v %v", saved, err)
	}
	if _, err = a.SaveContentDraft(ctx, blank.DraftID, payload, 1); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale save: %v", err)
	}
	current, err := a.ReadContentDraft(ctx, blank.DraftID)
	if err != nil || current.Version != 2 || current.Payload.Title != "港口的灯 · 修订" {
		t.Fatalf("stale save changed the draft: %+v %v", current, err)
	}
	if _, err = a.SaveContentDraft(ctx, blank.DraftID, payload, 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing version accepted: %v", err)
	}
	invalid := payload
	invalid.Mode = "sandbox"
	if _, err = a.SaveContentDraft(ctx, blank.DraftID, invalid, 2); !errors.Is(err, ErrContentInvalid) {
		t.Fatalf("invalid mode accepted: %v", err)
	}
	invalid = payload
	invalid.Locations = []PackLocation{{ID: "inn", Name: "客栈"}, {ID: "inn", Name: "客栈"}}
	if _, err = a.SaveContentDraft(ctx, blank.DraftID, invalid, 2); !errors.Is(err, ErrContentInvalid) {
		t.Fatalf("duplicate location accepted: %v", err)
	}

	if err = a.DeleteContentDraft(ctx, blank.DraftID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ReadContentDraft(ctx, blank.DraftID); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("deleted draft readable: %v", err)
	}
	if err = a.DeleteContentDraft(ctx, blank.DraftID); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	other := &App{appDB: a.appDB, userID: "other"}
	if list, err := other.ListContentProjects(ctx); err != nil || len(list) != 0 {
		t.Fatalf("owner leak: %+v %v", list, err)
	}
	if _, err := other.ReadContentDraft(ctx, blank.DraftID); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("draft leaked to another owner: %v", err)
	}
}

// The editor receives full characters, edits stay valid, and the package mapping
// writes them back one file per character.
// Replacing a referenced image must change the package identity: a world that shows the
// image would otherwise be indistinguishable from one showing the old bytes.
func TestPublishedIdentityCoversEveryReferencedImage(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-identity")
	if _, err := a.UploadContentDraftAsset(ctx, draft.DraftID, "assets/cover.png", pngBytes(t, 12, 8)); err != nil {
		t.Fatal(err)
	}
	payload := draft.Payload
	payload.Cover = "assets/cover.png"
	payload.NPCs[0].Avatar = "assets/keeper.png"
	if _, err := a.UploadContentDraftAsset(ctx, draft.DraftID, "assets/keeper.png", pngBytes(t, 16, 16)); err != nil {
		t.Fatal(err)
	}
	saved, err := a.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := a.ReadContentProject(ctx, project.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "identity-1", DraftID: saved.DraftID, ExpectedDraftVersion: saved.Version, ExpectedProjectVersion: first.Version}); err != nil {
		t.Fatal(err)
	}
	afterFirst, err := a.Game("harbor-identity")
	if err != nil {
		t.Fatal(err)
	}
	// Same name, different bytes: the identity has to move.
	if _, err = a.UploadContentDraftAsset(ctx, saved.DraftID, "assets/keeper.png", pngBytes(t, 24, 24)); err != nil {
		t.Fatal(err)
	}
	reloaded, err := a.ReadContentDraft(ctx, saved.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := a.ReadContentProject(ctx, project.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "identity-2", DraftID: reloaded.DraftID, ExpectedDraftVersion: reloaded.Version, ExpectedProjectVersion: second.Version}); err != nil {
		t.Fatal(err)
	}
	afterSecond, err := a.Game("harbor-identity")
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst.Revision == afterSecond.Revision {
		t.Fatalf("replacing an avatar kept the same revision identity: %s", afterFirst.Revision)
	}
	if afterSecond.Revision == "" {
		t.Fatal("the second publication has no revision")
	}
}

func TestContentDraftNPCFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	pack := a.packs[GameID]
	project, err := a.CreateContentProject(ctx, "lantern-npc", "人物往返")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := a.CreateContentDraft(ctx, project.ProjectID, pack.Definition.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Payload.NPCs) != len(pack.Definition.Characters) {
		t.Fatalf("characters missing from the draft: %+v", draft.Payload.NPCs)
	}
	for _, npc := range draft.Payload.NPCs {
		location := pack.Definition.InitialLocations[npc.EntityID]
		if npc.Profile == "" || npc.Role == "" || npc.Knowledge == "" || npc.InitialConcerns == "" || npc.InitialLocation != location || npc.Revision == "" {
			t.Fatalf("draft character is incomplete: %+v", npc)
		}
	}
	payload := draft.Payload
	payload.NPCs[0].SpeakingExamples = []string{"灯要按时点。", "潮水不会等人。"}
	payload.NPCs[0].Avatar = "assets/keeper.png"
	saved, err := a.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Payload.NPCs[0].SpeakingExamples) != 2 || saved.Payload.NPCs[0].Avatar != "assets/keeper.png" {
		t.Fatalf("character extensions lost: %+v", saved.Payload.NPCs[0])
	}
	files, err := draftNPCFiles(saved.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(saved.Payload.NPCs) {
		t.Fatalf("package mapping: %+v", files)
	}
	file, ok := files["npcs/"+saved.Payload.NPCs[0].DefinitionID+".json"]
	if !ok || file.Avatar != "assets/keeper.png" || len(file.SpeakingExamples) != 2 || file.EntityID != saved.Payload.NPCs[0].EntityID {
		t.Fatalf("character file: %+v", file)
	}

	for _, bad := range []struct {
		name   string
		mutate func(*ContentDraftPayload)
	}{
		{"external avatar", func(p *ContentDraftPayload) { p.NPCs[0].Avatar = "https://example.test/a.png" }},
		{"duplicate character", func(p *ContentDraftPayload) { p.NPCs = append(p.NPCs, p.NPCs[0]) }},
		{"missing name", func(p *ContentDraftPayload) { p.NPCs[0].Name = "" }},
	} {
		invalid := saved.Payload
		invalid.NPCs = append([]ContentDraftNPC{}, saved.Payload.NPCs...)
		bad.mutate(&invalid)
		if _, err := a.SaveContentDraft(ctx, draft.DraftID, invalid, saved.Version); !errors.Is(err, ErrContentInvalid) {
			t.Fatalf("%s accepted: %v", bad.name, err)
		}
	}
	// Internal identifiers belong to the program: a name alone is enough, and a
	// reference to a place that no longer exists is dropped instead of failing.
	friendly := saved.Payload
	friendly.Locations = []PackLocation{{Name: "栈桥", Connections: []string{}}}
	friendly.InitialLocation = ""
	friendly.NPCs = []ContentDraftNPC{{
		Name: "看灯人", Role: "港口看灯人", Profile: "资料", InitialLocation: "不再存在的地点",
	}}
	friendly.Bystanders = []PackBystander{{Name: "船夫", Description: "在栈桥等活"}}
	repaired, err := a.SaveContentDraft(ctx, draft.DraftID, friendly, saved.Version)
	if err != nil {
		t.Fatalf("a draft with only human names was rejected: %v", err)
	}
	if repaired.Payload.Locations[0].ID == "" || repaired.Payload.NPCs[0].DefinitionID == "" || !entityID.MatchString(repaired.Payload.NPCs[0].EntityID) {
		t.Fatalf("identifiers were not generated: %+v", repaired.Payload)
	}
	if repaired.Payload.InitialLocation != repaired.Payload.Locations[0].ID {
		t.Fatalf("an unspecified starting place must fall back to the first one: %+v", repaired.Payload)
	}
	if repaired.Payload.NPCs[0].InitialLocation != "" {
		t.Fatalf("a reference to a missing place must be dropped: %q", repaired.Payload.NPCs[0].InitialLocation)
	}
	if !bystanderIDPattern.MatchString(repaired.Payload.Bystanders[0].BystanderID) {
		t.Fatalf("passer-by identity: %+v", repaired.Payload.Bystanders[0])
	}
	// Generated identities stay stable across an edit, so published content keeps its
	// identity when the author keeps editing it.
	again, err := a.SaveContentDraft(ctx, draft.DraftID, repaired.Payload, repaired.Version)
	if err != nil {
		t.Fatal(err)
	}
	if again.Payload.NPCs[0].DefinitionID != repaired.Payload.NPCs[0].DefinitionID || again.Payload.Locations[0].ID != repaired.Payload.Locations[0].ID {
		t.Fatalf("generated identities changed on a plain resave: %+v", again.Payload)
	}
	if _, err := draftNPCFiles(ContentDraftPayload{NPCs: []ContentDraftNPC{{DefinitionID: "a", Revision: "v1", EntityID: "npc:a", Name: "甲", Role: "角色", Profile: "资料"}}}); err != nil {
		t.Fatalf("minimal character rejected: %v", err)
	}
}

// The player view is a separate projection: author facts, private character
// knowledge and plot conditions never appear in it.
func TestContentDraftPreviewViews(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	pack := a.packs[GameID]
	project, err := a.CreateContentProject(ctx, "lantern-preview", "预览隔离")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := a.CreateContentDraft(ctx, project.ProjectID, pack.Definition.Revision)
	if err != nil {
		t.Fatal(err)
	}
	player, err := a.PreviewContentDraft(ctx, draft.DraftID, false)
	if err != nil {
		t.Fatal(err)
	}
	if player.View != "player" || player.AuthorFacts != "" || player.AuthorRules != "" || player.AuthorPlot != nil || player.AuthorEventPolicy != nil || len(player.AuthorCharacters) != 0 {
		t.Fatalf("player view carries author material: %+v", player)
	}
	if player.Title == "" || len(player.Characters) == 0 || player.Opening == "" {
		t.Fatalf("player view is too thin to be useful: %+v", player)
	}
	body, err := json.Marshal(player)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{pack.Definition.Secret, pack.Definition.Characters[0].Knowledge, pack.Definition.Characters[0].Profile, pack.Definition.Characters[0].InitialConcerns} {
		if secret != "" && strings.Contains(string(body), secret) {
			t.Fatalf("player view leaked %q", secret)
		}
	}

	author, err := a.PreviewContentDraft(ctx, draft.DraftID, true)
	if err != nil {
		t.Fatal(err)
	}
	if author.View != "author" || author.AuthorFacts == "" || author.AuthorPlot == nil || len(author.AuthorCharacters) != len(draft.Payload.NPCs) || author.SpoilerWarning == "" {
		t.Fatalf("author view is incomplete: %+v", author)
	}
	if author.AuthorCharacters[0].Knowledge == "" || author.AuthorCharacters[0].Profile == "" {
		t.Fatalf("author view lost private fields: %+v", author.AuthorCharacters[0])
	}
	if _, err := a.PreviewContentDraft(ctx, "draft_missing", false); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("missing draft previewed: %v", err)
	}
	// Preview is read-only: it neither bumps the draft version nor touches a world.
	after, err := a.ReadContentDraft(ctx, draft.DraftID)
	if err != nil || after.Version != draft.Version {
		t.Fatalf("preview changed the draft: %+v %v", after.Version, err)
	}
}

// Starting a draft from a published revision copies its authoring fields, and the
// draft stays out of the world until publication.
func TestContentDraftFromRevisionStaysOutOfWorlds(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	pack := a.packs[GameID]
	project, err := a.CreateContentProject(ctx, "lantern-copy", "暮灯镇副本")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := a.CreateContentDraft(ctx, project.ProjectID, pack.Definition.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Payload.Mode != pack.Definition.Summary.Mode || draft.Payload.Title != pack.Definition.Summary.Title || draft.Payload.Background != pack.Definition.Background {
		t.Fatalf("copy lost authoring fields: %+v", draft.Payload)
	}
	if draft.Payload.InitialLocation == "" || len(draft.Payload.Locations) == 0 || len(draft.Payload.NPCs) == 0 {
		t.Fatalf("copy lost structure: %+v", draft.Payload)
	}
	if _, err = a.CreateContentDraft(ctx, project.ProjectID, "unknown.revision"); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("unknown revision accepted: %v", err)
	}

	world, err := a.CreateWorld(ctx, "草稿隔离", "guided", "旅人", "", true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	payload := draft.Payload
	payload.Title = "改过的标题"
	payload.Background = "改过的背景"
	payload.Opening = "改过的开场"
	if _, err = a.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version); err != nil {
		t.Fatal(err)
	}
	after, err := a.ReadWorld(ctx, world.WorldID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if after.Definition.Summary.Title != before.Definition.Summary.Title || after.Definition.Background != before.Definition.Background || after.Summary.Revision != before.Summary.Revision {
		t.Fatal("draft edit reached an existing world")
	}
	if after.Summary.MessageHead != before.Summary.MessageHead || after.Summary.ContextEpoch != before.Summary.ContextEpoch {
		t.Fatal("draft edit changed world progress")
	}
}
