package storyapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gameagent/backend/internal/wire"
)

func assetDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Draft assets are staged beside the draft before publication, so a referenced
// image is validated once and then copied into the package unchanged.
const (
	assetImageLimit = 4 * 1024 * 1024
	assetMaxPixels  = 4096
)

type ContentDraftAsset struct {
	AssetID      string `json:"asset_id"`
	RelativeName string `json:"relative_name"`
	MediaType    string `json:"media_type"`
	ByteSize     int64  `json:"byte_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

// UploadContentDraftAsset stores one image for a draft. The declared name only has
// to be a package-relative assets/ path; the bytes decide the media type, and an
// image that cannot be decoded is rejected.
func (a *App) UploadContentDraftAsset(ctx context.Context, draftID, relativeName string, body []byte) (ContentDraftAsset, error) {
	draft, err := a.ReadContentDraft(ctx, draftID)
	if err != nil {
		return ContentDraftAsset{}, err
	}
	relativeName = wire.Clean(relativeName)
	if !strings.HasPrefix(relativeName, "assets/") || strings.Contains(relativeName, "\\") || strings.Contains(relativeName, "..") || len(relativeName) > 240 {
		return ContentDraftAsset{}, fmt.Errorf("%w: asset path", ErrContentInvalid)
	}
	if len(body) == 0 || len(body) > assetImageLimit {
		return ContentDraftAsset{}, fmt.Errorf("%w: asset size", ErrContentInvalid)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return ContentDraftAsset{}, fmt.Errorf("%w: asset is not a decodable image", ErrContentInvalid)
	}
	mediaType := ""
	switch format {
	case "png":
		mediaType = "image/png"
	case "jpeg":
		mediaType = "image/jpeg"
	default:
		return ContentDraftAsset{}, fmt.Errorf("%w: asset must be PNG or JPEG", ErrContentInvalid)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > assetMaxPixels || config.Height > assetMaxPixels {
		return ContentDraftAsset{}, fmt.Errorf("%w: asset dimensions", ErrContentInvalid)
	}
	dir := filepath.Join(a.contentRoot(), "drafts", draft.DraftID)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return ContentDraftAsset{}, err
	}
	// Re-uploading a name replaces that asset instead of stacking duplicates: a draft
	// refers to assets by their package-relative name.
	var previousID, previousPath string
	err = a.appDB.QueryRowContext(ctx, `SELECT asset_id,staged_path FROM content_draft_assets WHERE user_id=? AND draft_id=? AND relative_name=?`, a.userID, draft.DraftID, relativeName).Scan(&previousID, &previousPath)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ContentDraftAsset{}, err
	}
	asset := ContentDraftAsset{AssetID: wire.NewID("asset"), RelativeName: relativeName, MediaType: mediaType, ByteSize: int64(len(body)), Width: config.Width, Height: config.Height}
	if previousID != "" {
		asset.AssetID = previousID
	}
	staged := filepath.Join(dir, asset.AssetID)
	if err = os.WriteFile(staged, body, 0o644); err != nil {
		return ContentDraftAsset{}, err
	}
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_draft_assets(user_id,draft_id,asset_id,relative_name,media_type,byte_size,width,height,digest,staged_path,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,draft_id,asset_id) DO UPDATE SET relative_name=excluded.relative_name,media_type=excluded.media_type,byte_size=excluded.byte_size,width=excluded.width,height=excluded.height,digest=excluded.digest,staged_path=excluded.staged_path`,
		a.userID, draft.DraftID, asset.AssetID, asset.RelativeName, asset.MediaType, asset.ByteSize, asset.Width, asset.Height, assetDigest(body), staged, wire.NowText()); err != nil {
		_ = os.Remove(staged)
		return ContentDraftAsset{}, err
	}
	if previousPath != "" && previousPath != staged {
		_ = os.Remove(previousPath)
	}
	return asset, nil
}

// ListContentDraftAssets returns the assets a draft already staged.
func (a *App) ListContentDraftAssets(ctx context.Context, draftID string) ([]ContentDraftAsset, error) {
	if _, err := a.ReadContentDraft(ctx, draftID); err != nil {
		return nil, err
	}
	rows, err := a.appDB.QueryContext(ctx, `SELECT asset_id,relative_name,media_type,byte_size,width,height FROM content_draft_assets WHERE user_id=? AND draft_id=? ORDER BY relative_name`, a.userID, draftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContentDraftAsset{}
	for rows.Next() {
		var asset ContentDraftAsset
		if err = rows.Scan(&asset.AssetID, &asset.RelativeName, &asset.MediaType, &asset.ByteSize, &asset.Width, &asset.Height); err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	return out, rows.Err()
}

// RemoveContentDraftAsset deletes one staged asset of a draft.
func (a *App) RemoveContentDraftAsset(ctx context.Context, draftID, assetID string) error {
	if _, err := a.ReadContentDraft(ctx, draftID); err != nil {
		return err
	}
	var staged string
	err := a.appDB.QueryRowContext(ctx, `SELECT staged_path FROM content_draft_assets WHERE user_id=? AND draft_id=? AND asset_id=?`, a.userID, draftID, assetID).Scan(&staged)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrContentNotFound
	}
	if err != nil {
		return err
	}
	if _, err = a.appDB.ExecContext(ctx, `DELETE FROM content_draft_assets WHERE user_id=? AND draft_id=? AND asset_id=?`, a.userID, draftID, assetID); err != nil {
		return err
	}
	_ = os.Remove(staged)
	return nil
}

// ReadContentDraftAsset returns one staged asset's bytes, which is what publication
// copies into the package.
func (a *App) ReadContentDraftAsset(ctx context.Context, draftID, relativeName string) ([]byte, error) {
	if _, err := a.ReadContentDraft(ctx, draftID); err != nil {
		return nil, err
	}
	var staged string
	err := a.appDB.QueryRowContext(ctx, `SELECT staged_path FROM content_draft_assets WHERE user_id=? AND draft_id=? AND relative_name=?`, a.userID, draftID, relativeName).Scan(&staged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrContentNotFound
	}
	if err != nil {
		return nil, err
	}
	return os.ReadFile(staged)
}

// ReadUploadedAsset enforces the image limit before the service sees the bytes.
func ReadUploadedAsset(reader io.Reader) ([]byte, error) {
	return ReadUploadedFile(reader, assetImageLimit)
}

// AssetUploadLimit is the largest request an image upload may be, including the form
// wrapper, so the boundary can refuse it before parsing.
func AssetUploadLimit() int64 { return assetImageLimit + (1 << 20) }

// ReadUploadedFile reads one upload up to an explicit limit. Each import format has
// its own ceiling, so the entry point must say which one applies instead of assuming
// the image limit.
func ReadUploadedFile(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: upload exceeds the size limit", ErrContentInvalid)
	}
	return body, nil
}
