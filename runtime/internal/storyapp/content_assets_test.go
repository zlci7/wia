package storyapp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func readFileIfExists(path string) ([]byte, error) { return os.ReadFile(path) }

// Assets are staged per draft, validated by decoding, and only then usable by a
// publication.
func TestDraftAssetUploadAndRemoval(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-upload")
	if _, err := a.ListContentDraftAssets(ctx, draft.DraftID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UploadContentDraftAsset(ctx, "draft_missing", "assets/a.png", pngBytes(t, 4, 4)); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("unknown draft accepted: %v", err)
	}
	for _, tc := range []struct {
		name string
		path string
		body []byte
	}{
		{"external path", "../secret.png", pngBytes(t, 4, 4)},
		{"not an asset path", "cover.png", pngBytes(t, 4, 4)},
		{"backslash", `assets\a.png`, pngBytes(t, 4, 4)},
		{"traversal", "assets/../../secret.png", pngBytes(t, 4, 4)},
		{"not an image", "assets/a.png", []byte("not an image")},
		{"empty", "assets/a.png", nil},
	} {
		if _, err := a.UploadContentDraftAsset(ctx, draft.DraftID, tc.path, tc.body); !errors.Is(err, ErrContentInvalid) {
			t.Fatalf("%s accepted: %v", tc.name, err)
		}
	}
	asset, err := a.UploadContentDraftAsset(ctx, draft.DraftID, "assets/cover.png", pngBytes(t, 20, 10))
	if err != nil {
		t.Fatal(err)
	}
	if asset.MediaType != "image/png" || asset.Width != 20 || asset.Height != 10 || asset.ByteSize == 0 || asset.AssetID == "" {
		t.Fatalf("asset metadata: %+v", asset)
	}
	list, err := a.ListContentDraftAssets(ctx, draft.DraftID)
	if err != nil || len(list) != 1 || list[0].AssetID != asset.AssetID {
		t.Fatalf("asset list: %+v %v", list, err)
	}
	// Re-uploading the same name replaces that asset instead of stacking duplicates.
	replaced, err := a.UploadContentDraftAsset(ctx, draft.DraftID, "assets/cover.png", pngBytes(t, 30, 12))
	if err != nil || replaced.AssetID != asset.AssetID || replaced.Width != 30 {
		t.Fatalf("replacement: %+v %v", replaced, err)
	}
	if list, err = a.ListContentDraftAssets(ctx, draft.DraftID); err != nil || len(list) != 1 || list[0].Width != 30 {
		t.Fatalf("duplicate asset rows: %+v %v", list, err)
	}
	// The staged asset is exactly what publication copies.
	payload := draft.Payload
	payload.Cover = "assets/cover.png"
	saved, err := a.SaveContentDraft(ctx, draft.DraftID, payload, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: "upload-publish", DraftID: saved.DraftID, ExpectedDraftVersion: saved.Version, ExpectedProjectVersion: project.Version}); err != nil {
		t.Fatal(err)
	}
	var path string
	if err = a.appDB.QueryRowContext(ctx, `SELECT path FROM content_revisions WHERE user_id=? AND game_id='harbor-upload'`, a.userID).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if _, err = readFileIfExists(path + "/assets/cover.png"); err != nil {
		t.Fatalf("published asset missing: %v", err)
	}

	if err = a.RemoveContentDraftAsset(ctx, draft.DraftID, asset.AssetID); err != nil {
		t.Fatal(err)
	}
	if list, err = a.ListContentDraftAssets(ctx, draft.DraftID); err != nil || len(list) != 0 {
		t.Fatalf("asset not removed: %+v %v", list, err)
	}
	if err = a.RemoveContentDraftAsset(ctx, draft.DraftID, asset.AssetID); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("second removal: %v", err)
	}
}

// The shared upload reader enforces the endpoint limit before the service sees the
// bytes.
func TestReadUploadedAssetLimit(t *testing.T) {
	if _, err := ReadUploadedAsset(strings.NewReader("ok")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadUploadedAsset(strings.NewReader(strings.Repeat("x", assetImageLimit+1))); !errors.Is(err, ErrContentInvalid) {
		t.Fatalf("oversized upload accepted: %v", err)
	}
}
