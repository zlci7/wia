package content

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gameagent/backend/internal/story"
)

func v4TemplateRoot(t *testing.T) string {
	t.Helper()
	source := filepath.Join("..", "..", "..", "docs", "phase12", "templates", "story-pack-v4")
	root := t.TempDir()
	err := filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		target := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func changeV4JSON(t *testing.T, root, file string, change func(map[string]any)) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(file))
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	change(value)
	body, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestV4TemplateFreezesReferencedTextAndPlan(t *testing.T) {
	root := v4TemplateRoot(t)
	pack, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Definition.SchemaVersion != 4 || len(pack.Definition.Materials) != 8 || pack.Definition.Progression == nil || len(pack.Definition.Progression.InitialPlans) != 1 {
		t.Fatalf("missing v4 definition: %+v", pack.Definition)
	}
	material, ok := story.MaterialByID(pack.Definition, "contact-knowledge")
	if !ok || material.OwnerID != "npc:contact" || !strings.Contains(material.Body, "开场时") || len(pack.PackageFiles) != 13 {
		t.Fatalf("unfrozen material or files: %+v / %d", material, len(pack.PackageFiles))
	}
	if pack.Definition.Background != "" || pack.Definition.Secret != "" || pack.Definition.Characters[0].Knowledge != "" {
		t.Fatal("v4 text was flattened into unconditional context")
	}
	encoded, err := json.Marshal(pack.Definition)
	if err != nil {
		t.Fatal(err)
	}
	var frozen story.Definition
	if err := json.Unmarshal(encoded, &frozen); err != nil {
		t.Fatal(err)
	}
	copy, ok := story.MaterialByID(frozen, material.ID)
	if !ok || copy.Body != material.Body {
		t.Fatal("frozen definition dropped text")
	}
	name := filepath.Join(root, "npcs", "contact", "knowledge.md")
	if err := os.WriteFile(name, []byte(material.Body+"\n新的亲历。"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Digest == pack.Digest || copy.Body != material.Body {
		t.Fatal("text changes must change package digest without changing frozen text")
	}
}

func TestV4RejectsUnusableOrUnauthorizedReferences(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string)
	}{
		{"path escape", func(t *testing.T, root string) {
			changeV4JSON(t, root, "materials.json", func(v map[string]any) { v["materials"].([]any)[0].(map[string]any)["file"] = "../outside.md" })
		}},
		{"unknown owner", func(t *testing.T, root string) {
			changeV4JSON(t, root, "materials.json", func(v map[string]any) { v["materials"].([]any)[6].(map[string]any)["owner_id"] = "npc:unknown" })
		}},
		{"secret declared public", func(t *testing.T, root string) {
			changeV4JSON(t, root, "materials.json", func(v map[string]any) { v["materials"].([]any)[3].(map[string]any)["visibility"] = "public" })
		}},
		{"both text and file", func(t *testing.T, root string) {
			changeV4JSON(t, root, "materials.json", func(v map[string]any) { v["materials"].([]any)[0].(map[string]any)["text"] = "duplicate" })
		}},
		{"unknown knowledge recipient", func(t *testing.T, root string) {
			changeV4JSON(t, root, "materials.json", func(v map[string]any) {
				v["materials"].([]any)[1].(map[string]any)["known_to"] = []string{"npc:unknown"}
			})
		}},
		{"wrong plan owner", func(t *testing.T, root string) {
			changeV4JSON(t, root, "narrative/progression.json", func(v map[string]any) { v["initial_plans"].([]any)[0].(map[string]any)["owner_id"] = "player" })
		}},
		{"inline v4 background", func(t *testing.T, root string) {
			changeV4JSON(t, root, "story.json", func(v map[string]any) { v["background"] = "inline" })
		}},
		{"unknown data field", func(t *testing.T, root string) {
			changeV4JSON(t, root, "world/locations.json", func(v map[string]any) { v["script"] = "run" })
		}},
		{"invalid UTF-8", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "world", "background", "core.md"), []byte{0xff}, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversized core", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "world", "background", "core.md"), []byte(strings.Repeat("长", 801)), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := v4TemplateRoot(t)
			test.change(t, root)
			if _, err := Load(root); err == nil {
				t.Fatal("invalid v4 package accepted")
			}
		})
	}
}

func TestV4ImportPublishExportPreservesFilesAndRefusesLossyEdits(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	project, err := service.CreateContentProject(ctx, "v4-round-trip", "文件剧本")
	if err != nil {
		t.Fatal(err)
	}
	original, err := Load(v4TemplateRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, body := range exportPackageFiles(original) {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewContentImport(ctx, project.ProjectID, "world.wia-story.zip", archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	draft, err := service.ConfirmContentImport(ctx, preview.DraftID, "confirm-v4", preview.Version)
	if err != nil {
		t.Fatal(err)
	}
	lossy := draft.Payload
	lossy.SchemaVersion = SchemaV3
	lossy.PackageFiles = nil
	if _, err := service.SaveContentDraft(ctx, draft.DraftID, lossy, draft.Version); !errors.Is(err, ErrContentInvalid) {
		t.Fatalf("lossy edit should be refused: %v", err)
	}
	retained, err := service.ReadContentDraft(ctx, draft.DraftID)
	if err != nil || len(retained.Payload.PackageFiles) != len(original.PackageFiles) || retained.Version != draft.Version {
		t.Fatalf("failed edit changed v4 draft: %v", err)
	}
	operation, err := service.PublishContentDraft(ctx, PublishRequest{RequestKey: "publish-v4", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version, ExpectedProjectVersion: project.Version})
	if err != nil || operation.Status != "succeeded" {
		t.Fatalf("publish: %+v / %v", operation, err)
	}
	published, _, err := service.ReadContentProject(ctx, project.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := service.ExportContentRevision(ctx, project.GameID, published.CurrentRevision)
	if err != nil {
		t.Fatal(err)
	}
	files, err := readPackageZip(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(original.PackageFiles) {
		t.Fatalf("export lost files: %d / %d", len(files), len(original.PackageFiles))
	}
	for name, body := range original.PackageFiles {
		if name != "story.json" && !bytes.Equal(files[name], body) {
			t.Fatalf("referenced file changed: %s", name)
		}
	}
	root := t.TempDir()
	for name, body := range files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Definition.Summary.ID != project.GameID || loaded.Definition.Revision != published.CurrentRevision || len(loaded.Definition.Materials) != len(original.Definition.Materials) {
		t.Fatal("published package lost identity or material definition")
	}
}
