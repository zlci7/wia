package storyapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gameagent/runtime/internal/model"
)

func packFixture(t *testing.T, id string) string {
	t.Helper()
	root := t.TempDir()
	err := fs.WalkDir(packagedStories, "packs/"+id, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "packs/"+id)
		target := filepath.Join(root, filepath.FromSlash(rel))
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		b, e := packagedStories.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func rewritePack(t *testing.T, root string, change func(map[string]any)) {
	t.Helper()
	path := filepath.Join(root, "story.json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var p map[string]any
	if e = json.Unmarshal(b, &p); e != nil {
		t.Fatal(e)
	}
	change(p)
	b, _ = json.Marshal(p)
	if e = os.WriteFile(path, b, 0644); e != nil {
		t.Fatal(e)
	}
}

func TestPackValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"schema", func(p map[string]any) { p["schema_version"] = 2 }},
		{"mode", func(p map[string]any) { p["mode"] = "both" }},
		{"unknown capability", func(p map[string]any) { p["event_generation"] = true }},
		{"null", func(p map[string]any) { p["player"] = nil }},
		{"location", func(p map[string]any) { p["initial_location"] = "missing" }},
		{"connection", func(p map[string]any) {
			p["locations"].([]any)[0].(map[string]any)["connections"] = []string{"missing"}
		}},
		{"traversal", func(p map[string]any) { p["cover"] = "assets/../../secret.png" }},
		{"remote", func(p map[string]any) { p["cover"] = "https://example.test/cover.png" }},
		{"duplicate entity", func(p map[string]any) { p["npcs"] = []string{"npcs/engineer.json", "npcs/engineer.json"} }},
		{"invalid defaults", func(p map[string]any) { p["defaults"] = map[string]any{"perspective": "omniscient"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := packFixture(t, "orbital-repair")
			rewritePack(t, root, tc.change)
			if _, e := loadPack(root); e == nil {
				t.Fatal("invalid pack accepted")
			}
		})
	}
	var v StoryPack
	if strictPackJSON([]byte(`{"mode":"open","mode":"guided"}`), &v) == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestPackDigestAndIsolation(t *testing.T) {
	root := packFixture(t, "orbital-repair")
	first, e := loadPack(root)
	if e != nil {
		t.Fatal(e)
	}
	rewritePack(t, root, func(p map[string]any) {})
	second, e := loadPack(root)
	if e != nil || first.Digest != second.Digest {
		t.Fatal("formatting changed digest", e)
	}
	app := newTestApp(t, &packGenerator{})
	ctx := context.Background()
	if len(app.Games()) != 2 {
		t.Fatal(app.PackIssues())
	}
	if _, e = app.CreateStoryWorld(ctx, CreateWorldRequest{GameID: "orbital-repair", ExpectedRevision: "wrong", RequestKey: "wrong"}); !errors.Is(e, ErrVersionConflict) {
		t.Fatal(e)
	}
	req := CreateWorldRequest{GameID: "orbital-repair", ExpectedRevision: "orbital-repair.pack.v1", RequestKey: "unique"}
	w, e := app.CreateStoryWorld(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := app.CreateStoryWorld(ctx, req)
	if e != nil || replay.WorldID != w.WorldID {
		t.Fatal("duplicate creation", e)
	}
	req.Name = "different"
	if _, e = app.CreateStoryWorld(ctx, req); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	s := readContextSnapshot(t, app, w.WorldID)
	if s.Summary.Mode != "open" || s.Characters[0].Name != "周宁" || s.PlayerName != "林舟" {
		t.Fatalf("wrong snapshot: %+v", s.Summary)
	}
	public, _ := app.WorldGame(ctx, w.WorldID)
	b, _ := json.Marshal(public)
	if strings.Contains(string(b), "漏签") || strings.Contains(string(b), "author_facts") {
		t.Fatal("private definition exposed")
	}
}

func TestPackSnapshotSurvivesReplacementAndRemoval(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	app, e := Open(ctx, Options{DataRoot: root, Generator: &packGenerator{}})
	if e != nil {
		t.Fatal(e)
	}
	packRoot := app.packRoot
	// Use a real encoded image, then reload its revised package.
	var cover bytes.Buffer
	if e = png.Encode(&cover, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	assetDir := filepath.Join(packRoot, "orbital-repair", "assets")
	if e = os.MkdirAll(assetDir, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(assetDir, "cover.png"), cover.Bytes(), 0644); e != nil {
		t.Fatal(e)
	}
	rewritePack(t, filepath.Join(packRoot, "orbital-repair"), func(p map[string]any) { p["cover"] = "assets/cover.png"; p["revision"] = "covered.v1" })
	app.Close()
	app, e = Open(ctx, Options{DataRoot: root, Generator: &packGenerator{}})
	if e != nil {
		t.Fatal(e)
	}
	req := CreateWorldRequest{GameID: "orbital-repair", ExpectedRevision: "covered.v1", RequestKey: "persist", Activate: true}
	w, e := app.CreateStoryWorld(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	app.Close()
	rewritePack(t, filepath.Join(packRoot, "orbital-repair"), func(p map[string]any) { p["title"] = "changed without revision" })
	app, e = Open(ctx, Options{DataRoot: root, Generator: &packGenerator{}})
	if e != nil {
		t.Fatal(e)
	}
	if len(app.Games()) != 1 || len(app.PackIssues()) != 1 {
		t.Fatal("bad revision not isolated")
	}
	app.Close()
	// Point the catalog at an empty directory; the original files are not consulted.
	app, e = Open(ctx, Options{DataRoot: root, StoryPacksPath: t.TempDir(), Generator: &packGenerator{}})
	if e != nil {
		t.Fatal(e)
	}
	defer app.Close()
	replay, e := app.CreateStoryWorld(ctx, req)
	if e != nil || replay.WorldID != w.WorldID {
		t.Fatal("replay needs current pack", e)
	}
	run, e := app.SubmitRun(ctx, w.WorldID, RunRequest{Input: "周宁，今天值班有什么需要了解的？", RequestKey: "after-remove"})
	if e != nil {
		t.Fatal(e)
	}
	if r := waitRun(t, app, w.WorldID, run.RunID); r.Status != "completed" {
		t.Fatalf("frozen world failed: %+v", r)
	}
	status, _ := app.Status(ctx)
	op, e := app.SaveAs(ctx, w.WorldID, "", "snapshot-copy", status.ActiveRevision)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		op, e = app.CopyOperation(ctx, op.OperationID)
		if e != nil {
			t.Fatal(e)
		}
		if op.Status == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.Status != "ready" {
		t.Fatalf("copy: %+v", op)
	}
	if e = app.DeleteWorld(ctx, w.WorldID, status.ActiveRevision); e != nil {
		t.Fatal(e)
	}
	got, mime, e := app.WorldCover(ctx, op.TargetWorldID)
	if e != nil || mime != "image/png" || !bytes.Equal(got, cover.Bytes()) {
		t.Fatal("cover not independent", e)
	}
	g, e := app.WorldGame(ctx, op.TargetWorldID)
	if e != nil || g.Title != "远星维修站" || g.Revision != "covered.v1" {
		t.Fatal("snapshot changed", g, e)
	}
}

type packGenerator struct {
	mu       sync.Mutex
	requests []model.TextRequest
}

func (g *packGenerator) GenerateText(ctx context.Context, r model.TextRequest) (model.TextResponse, error) {
	g.mu.Lock()
	g.requests = append(g.requests, r)
	g.mu.Unlock()
	text := ""
	switch {
	case strings.Contains(r.System, "结构化回合意图"):
		text = `{"intent_type":"speak","addressee_id":"npc:innkeeper","visibility":"public"}`
	case strings.Contains(r.System, "重要 NPC"):
		text = `{"speech":"你好，先了解一下值班情况吧。","action_intent":"","silent":false,"memory":"新来的人向我问好。"}`
	case strings.Contains(r.System, "场景协调 Agent"):
		start := strings.Index(r.Input, "待裁定行动(JSON)：") + len("待裁定行动(JSON)：")
		end := strings.Index(r.Input[start:], "\n")
		var candidates []Event
		_ = json.Unmarshal([]byte(r.Input[start:start+end]), &candidates)
		outcomes := []hostActionResult{}
		for _, c := range candidates {
			outcomes = append(outcomes, hostActionResult{ActionID: c.EventID, Status: "succeeded", Content: c.Content, Recipients: []string{"player", "npc:innkeeper", "npc:mercenary"}})
		}
		scene := "维修站值班室"
		if strings.Contains(r.Input, "旧渡口客栈") {
			scene = "旧渡口客栈"
		}
		b, _ := json.Marshal(hostResult{Scene: scene, SceneCharacters: []string{"npc:innkeeper", "npc:mercenary"}, Outcomes: outcomes, SceneUpdates: []sceneUpdate{}})
		text = string(b)
	case strings.Contains(r.System, "玩家正文 Agent"):
		text = "你向面前的人问好，对方抬头回应了你。"
	default:
		return model.TextResponse{}, errors.New("unexpected pack test stage")
	}
	return model.TextResponse{Text: text}, nil
}

func TestPackRequestsUseFrozenDefinitionAndScopedKnowledge(t *testing.T) {
	g := &packGenerator{}
	app := newTestApp(t, g)
	ctx := context.Background()
	for _, id := range []string{"lantern-dusk", "orbital-repair"} {
		game, _ := app.Game(id)
		w, e := app.CreateStoryWorld(ctx, CreateWorldRequest{GameID: id, ExpectedRevision: game.Revision, RequestKey: id, Activate: true})
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 2; i++ {
			run, e := app.SubmitRun(ctx, w.WorldID, RunRequest{Input: "你好，介绍一下这里吧。", RequestKey: newID("test")})
			if e != nil {
				t.Fatal(e)
			}
			if r := waitRun(t, app, w.WorldID, run.RunID); r.Status != "completed" {
				t.Fatalf("%s: %+v", id, r)
			}
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	purposes := map[string]bool{}
	for _, r := range g.requests {
		if !strings.Contains(r.Input, "orbital-repair") {
			continue
		}
		if strings.Contains(r.Input, "沈岚") || strings.Contains(r.Input, "旧渡口") || strings.Contains(r.Input, "铁杉") {
			t.Fatal("cross-story context")
		}
		for _, purpose := range []string{"结构化回合意图", "重要 NPC", "场景协调 Agent", "玩家正文 Agent"} {
			if strings.Contains(r.System, purpose) {
				purposes[purpose] = true
			}
		}
		if strings.Contains(r.Input, "你的身份：许遥") && strings.Contains(r.Input, "漏签") {
			t.Fatal("private knowledge leaked to dispatcher")
		}
	}
	if len(purposes) != 4 {
		t.Fatal("not all stages captured", purposes)
	}
}

func TestPackCorrectionAndLegacyIsolation(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &packGenerator{})
	worlds := map[string]WorldSummary{}
	for _, id := range []string{"lantern-dusk", "orbital-repair"} {
		g, _ := a.Game(id)
		w, err := a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: id, ExpectedRevision: g.Revision, RequestKey: id})
		if err != nil {
			t.Fatal(err)
		}
		worlds[id] = w
	}
	w := worlds["orbital-repair"]
	_, err := a.Correct(ctx, w.WorldID, CorrectionRequest{RequestKey: "engineer-profile", ExpectedEpoch: w.ContextEpoch, Kind: "character", Scope: "npc:innkeeper", TargetID: "profile", Replacement: "谨慎核对每份检修记录的工程师。"})
	if err != nil {
		t.Fatal(err)
	}
	if job := waitMemory(t, a, w.WorldID); job.Status != "completed" {
		t.Fatal(job)
	}
	station := readContextSnapshot(t, a, w.WorldID)
	inn := readContextSnapshot(t, a, worlds["lantern-dusk"].WorldID)
	if station.Characters[0].Profile == inn.Characters[0].Profile || inn.Summary.ContextEpoch != worlds["lantern-dusk"].ContextEpoch {
		t.Fatal("cross-game correction")
	}
	if strings.Contains(station.Definition.Characters[0].Profile, "每份检修") {
		t.Fatal("correction mutated initial template")
	}
	path, _, _ := a.worldRecord(ctx, w.WorldID)
	s, err := openWorldDB(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`DELETE FROM meta WHERE key='definition_snapshot'`)
	s.db.Close()
	if err != nil {
		t.Fatal(err)
	}
	legacy := readContextSnapshot(t, a, w.WorldID)
	if legacy.Characters[0].Profile != station.Characters[0].Profile || legacy.Summary.Mode != "open" {
		t.Fatal("legacy world reset to installed template")
	}
}
