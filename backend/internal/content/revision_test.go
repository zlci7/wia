package content

import "testing"

func TestPackageRevisionIdentity(t *testing.T) {
	story := StoryPack{SchemaVersion: SchemaV2, GameID: "harbor", Mode: "open", Title: "港口的灯", Description: "d", Gameplay: "g", Opening: "o", Clock: "第 1 日 19:00", InitialLocation: "harbor", Locations: []PackLocation{{ID: "harbor", Name: "港口"}}, Bystanders: []PackBystander{}}
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
