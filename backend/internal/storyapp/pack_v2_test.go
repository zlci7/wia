package storyapp

import (
	"context"
	"encoding/json"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	"strings"
	"testing"
)

func packLocationSet(ids ...string) map[string]PackLocation {
	out := map[string]PackLocation{}
	for _, id := range ids {
		out[id] = PackLocation{ID: id}
	}
	return out
}

// Pack loading keeps v1 string bystanders working while normalizing them to the
// v2 identity shape, and reads v2 objects unchanged.
func TestPackBystandersNormalizeBothSchemaVersions(t *testing.T) {
	locations := packLocationSet("inn", "dock")
	legacy := []PackBystander{{Name: "打瞌睡的船夫"}, {Name: "卖花的老人"}}
	normalized, err := normalizePackBystanders(legacy, "lantern-dusk.pack.v2", locations)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || normalized[0].BystanderID == "" || normalized[0].BystanderID == normalized[1].BystanderID {
		t.Fatalf("legacy ids: %+v", normalized)
	}
	if !bystanderIDPattern.MatchString(normalized[0].BystanderID) {
		t.Fatalf("legacy id is not a stable bystander id: %q", normalized[0].BystanderID)
	}
	stable, err := normalizePackBystanders(legacy, "lantern-dusk.pack.v2", locations)
	if err != nil || stable[0].BystanderID != normalized[0].BystanderID {
		t.Fatalf("legacy id is not deterministic: %+v %v", stable, err)
	}
	other, err := normalizePackBystanders(legacy, "lantern-dusk.pack.v3", locations)
	if err != nil || other[0].BystanderID == normalized[0].BystanderID {
		t.Fatal("legacy id ignores the definition revision")
	}

	v2 := []PackBystander{{BystanderID: "bystander:boatman", Name: "船夫", Description: "门边条凳上打瞌睡的船夫", InitialLocation: "dock", Avatar: "assets/boatman.png"}}
	kept, err := normalizePackBystanders(v2, "story.pack.v1", locations)
	if err != nil || kept[0].BystanderID != "bystander:boatman" || kept[0].InitialLocation != "dock" {
		t.Fatalf("v2 identity was rewritten: %+v %v", kept, err)
	}

	for _, tc := range []struct {
		name  string
		items []PackBystander
	}{
		{"duplicate id", []PackBystander{{BystanderID: "bystander:a", Name: "甲"}, {BystanderID: "bystander:a", Name: "乙"}}},
		{"duplicate name", []PackBystander{{Name: "甲"}, {Name: "甲"}}},
		{"blank name", []PackBystander{{BystanderID: "bystander:a", Name: "  "}}},
		{"unknown location", []PackBystander{{Name: "甲", InitialLocation: "cellar"}}},
		{"external avatar", []PackBystander{{Name: "甲", Avatar: "../secret.png"}}},
		{"invalid id", []PackBystander{{BystanderID: "boatman", Name: "甲"}}},
	} {
		if _, err := normalizePackBystanders(tc.items, "story.pack.v1", locations); err == nil {
			t.Fatalf("%s accepted", tc.name)
		}
	}
}

// The decoder accepts both item shapes and always writes the v2 object, so the
// editor and export paths have a single representation.
func TestPackBystanderJSONRoundTrip(t *testing.T) {
	var decoded struct {
		Bystanders []PackBystander `json:"bystanders"`
	}
	body := `{"bystanders":["打瞌睡的船夫",{"bystander_id":"bystander:boatman","name":"船夫","initial_location":"dock"}]}`
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Bystanders) != 2 || decoded.Bystanders[0].Name != "打瞌睡的船夫" || decoded.Bystanders[0].BystanderID != "" || decoded.Bystanders[1].BystanderID != "bystander:boatman" || decoded.Bystanders[1].InitialLocation != "dock" {
		t.Fatalf("decode: %+v", decoded.Bystanders)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	// Object form only: every entry must be an object with a name field, never a
	// bare string. A decoded legacy entry keeps an empty id until the pack loader's
	// normalization fills it in.
	var reencoded struct {
		Bystanders []map[string]any `json:"bystanders"`
	}
	if err := json.Unmarshal(encoded, &reencoded); err != nil {
		t.Fatal(err)
	}
	if len(reencoded.Bystanders) != 2 || reencoded.Bystanders[0]["name"] != "打瞌睡的船夫" || reencoded.Bystanders[0]["bystander_id"] != "" {
		t.Fatalf("encode is not the v2 object form: %s", encoded)
	}
	var unknown struct {
		Bystanders []PackBystander `json:"bystanders"`
	}
	if err := json.Unmarshal([]byte(`{"bystanders":[{"name":"甲","secret_field":"x"}]}`), &unknown); err == nil {
		t.Fatal("unknown bystander field accepted")
	}
}

// A package that declares schema v2 loads, and the bystander objects keep their
// identity instead of being reduced to display names.
func TestPackSchemaV2LoadsWithBystanderIdentity(t *testing.T) {
	root := packFixture(t, "orbital-repair")
	rewritePack(t, root, func(p map[string]any) {
		p["schema_version"] = 2
		p["bystanders"] = []any{
			map[string]any{"bystander_id": "bystander:dockhand", "name": "搬运工", "description": "在栈桥上等活", "initial_location": "workshop"},
		}
	})
	pack, err := loadPack(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Definition.BystanderRefs) != 1 || pack.Definition.BystanderRefs[0].BystanderID != "bystander:dockhand" {
		t.Fatalf("v2 identity lost: %+v", pack.Definition.BystanderRefs)
	}
	if len(pack.Definition.Bystanders) != 1 || pack.Definition.Bystanders[0] != "搬运工" {
		t.Fatalf("display names: %+v", pack.Definition.Bystanders)
	}
	if body := wire.MarshalJSON(pack.Definition); !strings.Contains(body, "bystander:dockhand") {
		t.Fatalf("snapshot dropped bystander identity: %s", body)
	}
}

// Important characters carry their optional avatar and speaking examples, while
// packs that omit them keep loading.
// Authored dialogue samples must reach the NPC prompt as style material.
func TestSpeakingExamplesReachTheNPCPrompt(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-examples")
	payload := draft.Payload
	payload.NPCs[0].SpeakingExamples = []string{"灯要按时点。", "潮水不会等人。"}
	saved, err := a.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := a.ReadContentProject(ctx, project.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "examples-publish", DraftID: saved.DraftID, ExpectedDraftVersion: saved.Version, ExpectedProjectVersion: fresh.Version}); err != nil {
		t.Fatal(err)
	}
	game, err := a.Game("harbor-examples")
	if err != nil {
		t.Fatal(err)
	}
	w, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: "harbor-examples", ExpectedRevision: game.Revision, Name: "示例世界", RequestKey: "examples-world", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.ReadWorld(ctx, w.WorldID, 1)
	if err != nil {
		t.Fatal(err)
	}
	def := snapshot.Definition
	frozen := def.Characters
	def.Characters = snapshot.Characters
	for index := range def.Characters {
		if samples, ok := characterSpeakingExamples(frozen, def.Characters[index].EntityID); ok {
			def.Characters[index].SpeakingExamples = samples
		}
	}
	if len(def.Characters) == 0 || len(def.Characters[0].SpeakingExamples) != 2 {
		t.Fatalf("published samples did not reach the world roster: %+v", def.Characters)
	}
	prompt := buildNPCPrompt(snapshot, def, def.Characters[0], "player", "speak", turn.StageInput{PlayerPerception: "我问他灯的事。"}, "", 1)
	for _, example := range def.Characters[0].SpeakingExamples {
		if !strings.Contains(prompt, example) {
			t.Fatalf("the prompt dropped a speaking example %q", example)
		}
	}
	if !strings.Contains(prompt, "只作语气与用词参考") {
		t.Fatal("the prompt must state that samples are style, not events")
	}

	// A later revision must not change how an existing world's characters speak.
	reloaded, err := a.ReadContentDraft(ctx, saved.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	edited := reloaded.Payload
	edited.NPCs[0].SpeakingExamples = []string{"换过的示例，旧存档不该看到。"}
	next, err := a.SaveContentDraft(ctx, saved.DraftID, edited, reloaded.Version)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := a.ReadContentProject(ctx, project.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "examples-publish-2", DraftID: next.DraftID, ExpectedDraftVersion: next.Version, ExpectedProjectVersion: current.Version}); err != nil {
		t.Fatal(err)
	}
	after, err := a.ReadWorld(ctx, w.WorldID, 1)
	if err != nil {
		t.Fatal(err)
	}
	samples, ok := characterSpeakingExamples(after.Definition.Characters, after.Characters[0].EntityID)
	if !ok || len(samples) != 2 || samples[0] != "灯要按时点。" {
		t.Fatalf("a newer revision changed an existing world's samples: %+v", samples)
	}
}

func TestPackNPCAvatarAndSpeakingExamples(t *testing.T) {
	var npc PackNPC
	body := `{"definition_id":"innkeeper","revision":"v1","entity_id":"npc:innkeeper","name":"沈岚","role":"客栈老板","profile":"谨慎。","initial_location":"inn","avatar":"assets/innkeeper.png","speaking_examples":["先别惊动客人。","柜台上的东西别动。"]}`
	if err := json.Unmarshal([]byte(body), &npc); err != nil {
		t.Fatal(err)
	}
	if npc.Avatar != "assets/innkeeper.png" || len(npc.SpeakingExamples) != 2 {
		t.Fatalf("npc extensions: %+v", npc)
	}
	var plain PackNPC
	if err := json.Unmarshal([]byte(`{"definition_id":"a","revision":"v1","entity_id":"npc:a","name":"甲","role":"角色","profile":"资料","initial_location":"inn"}`), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.Avatar != "" || plain.SpeakingExamples != nil {
		t.Fatalf("omitted extensions must stay empty: %+v", plain)
	}
}
