package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gameagent/backend/internal/story"
)

func TestPersonaCRUDAndVersionConflicts(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	list, err := a.ListPersonas(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("empty list: %+v %v", list, err)
	}
	for _, bad := range []PersonaRequest{{Name: "", Profile: "资料"}, {Name: strings.Repeat("长", 81), Profile: ""}, {Name: "旅人", Profile: strings.Repeat("长", 2001)}} {
		if _, err := a.CreatePersona(ctx, bad); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid template accepted: %+v %v", bad, err)
		}
	}
	created, err := a.CreatePersona(ctx, PersonaRequest{Name: "  旅人  ", Profile: "  寻找答案的调查者  "})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "旅人" || created.Profile != "寻找答案的调查者" || created.Version != 1 || created.PersonaID == "" {
		t.Fatalf("created: %+v", created)
	}
	updated, err := a.UpdatePersona(ctx, created.PersonaID, PersonaRequest{Name: "旅人", Profile: "改过的简介", ExpectedVersion: 1})
	if err != nil || updated.Version != 2 || updated.Profile != "改过的简介" {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if _, err = a.UpdatePersona(ctx, created.PersonaID, PersonaRequest{Name: "旅人", Profile: "过期写入", ExpectedVersion: 1}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale write: %v", err)
	}
	if _, err = a.UpdatePersona(ctx, "persona_missing", PersonaRequest{Name: "旅人", Profile: "x", ExpectedVersion: 1}); !errors.Is(err, ErrPersonaNotFound) {
		t.Fatalf("missing template: %v", err)
	}
	if err = a.DeletePersona(ctx, created.PersonaID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ReadPersona(ctx, created.PersonaID); !errors.Is(err, ErrPersonaNotFound) {
		t.Fatalf("deleted template readable: %v", err)
	}
	if err = a.DeletePersona(ctx, created.PersonaID); !errors.Is(err, ErrPersonaNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	other := &App{appDB: a.appDB, userID: "other"}
	if list, err = other.ListPersonas(ctx); err != nil || len(list) != 0 {
		t.Fatalf("owner leak: %+v %v", list, err)
	}
}

// A template is copied into a new world, and later template edits do not change
// a world that already started.
func TestPersonaCopiedIntoNewWorld(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	persona, err := a.CreatePersona(ctx, PersonaRequest{Name: "陆舟", Profile: "替人送信的夜行客"})
	if err != nil {
		t.Fatal(err)
	}
	pack := testPack(a, GameID)
	world, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: GameID, ExpectedRevision: pack.Definition.Revision, RequestKey: "persona-world", Name: "模板开局", PersonaID: persona.PersonaID, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	started, err := a.ReadWorld(ctx, world.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if started.PlayerName != "陆舟" || started.PlayerProfile != "替人送信的夜行客" {
		t.Fatalf("template not copied: %q %q", started.PlayerName, started.PlayerProfile)
	}
	if _, err = a.UpdatePersona(ctx, persona.PersonaID, PersonaRequest{Name: "陆舟", Profile: "后来改的简介", ExpectedVersion: persona.Version}); err != nil {
		t.Fatal(err)
	}
	if err = a.DeletePersona(ctx, persona.PersonaID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := a.ReadWorld(ctx, world.WorldID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PlayerName != "陆舟" || reloaded.PlayerProfile != "替人送信的夜行客" {
		t.Fatalf("world followed the template: %q %q", reloaded.PlayerName, reloaded.PlayerProfile)
	}
	// An unknown template is rejected instead of silently starting a default world.
	if _, err = a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: GameID, ExpectedRevision: pack.Definition.Revision, RequestKey: "missing-persona", Name: "无效模板", PersonaID: "persona_missing"}); !errors.Is(err, ErrPersonaNotFound) {
		t.Fatalf("unknown template: %v", err)
	}
}

// Applies the pack's editable flag before a template is used.
func TestPersonaRejectedWhenPlayerIsFixed(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	persona, err := a.CreatePersona(ctx, PersonaRequest{Name: "陆舟", Profile: "夜行客"})
	if err != nil {
		t.Fatal(err)
	}
	fixed := story.Definition{Summary: story.Summary{Player: story.Player{Name: "固定主角", Profile: "固定简介", Editable: false}}}
	if _, _, err := a.personaDefaults(ctx, persona.PersonaID, fixed); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("fixed player accepted a template: %v", err)
	}
	editable := story.Definition{Summary: story.Summary{Player: story.Player{Editable: true}}}
	name, profile, err := a.personaDefaults(ctx, persona.PersonaID, editable)
	if err != nil || name != "陆舟" || profile != "夜行客" {
		t.Fatalf("editable player: %q %q %v", name, profile, err)
	}
	if name, profile, err = a.personaDefaults(ctx, "", editable); err != nil || name != "" || profile != "" {
		t.Fatalf("no template must keep pack defaults: %q %q %v", name, profile, err)
	}
}
