package storyapp

import (
	"context"
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
	payload.NPCs = []string{"npcs/keeper.json"}
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
