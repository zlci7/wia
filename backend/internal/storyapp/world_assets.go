package storyapp

import (
	"context"
	"database/sql"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
)

// Worlds keep the images they started with. Creation copies every referenced cover
// and character avatar into the world directory, so editing, republishing or
// deleting the template cannot change or remove what an existing world shows.
const worldAssetLimit = 4 * 1024 * 1024

// snapshotWorldAssets copies a package's referenced images into the world directory
// and returns the cover file name plus each character's avatar file name.
func (a *App) snapshotWorldAssets(pack loadedPack, worldDir string) (string, map[string]string, error) {
	avatars := map[string]string{}
	references := map[string]bool{}
	if pack.CoverRelative != "" {
		references[pack.CoverRelative] = true
	}
	for _, character := range pack.Definition.Characters {
		if character.Avatar != "" {
			references[character.Avatar] = true
		}
	}
	if len(references) == 0 {
		return "", avatars, nil
	}
	for asset := range references {
		if !strings.HasPrefix(asset, "assets/") || strings.Contains(asset, "\\") || strings.Contains(asset, "..") || !fs.ValidPath(asset) {
			return "", nil, ErrContentInvalid
		}
	}
	if err := os.MkdirAll(filepath.Join(worldDir, "assets"), 0o755); err != nil {
		return "", nil, err
	}
	cover := ""
	for asset := range references {
		body, err := packFile(pack.Root, asset, worldAssetLimit)
		if err != nil {
			return "", nil, err
		}
		name := strings.ReplaceAll(strings.TrimPrefix(asset, "assets/"), "/", "-")
		if err = os.WriteFile(filepath.Join(worldDir, "assets", name), body, 0o644); err != nil {
			return "", nil, err
		}
		if asset == pack.CoverRelative {
			cover = name
		}
		for _, character := range pack.Definition.Characters {
			if character.Avatar == asset {
				avatars[character.EntityID] = name
			}
		}
	}
	return cover, avatars, nil
}

// worldAsset resolves a copied world resource by its world-relative file name. The
// file must still decode as an image; the stored name is not trusted.
func (a *App) worldAsset(worldPath, name string) ([]byte, error) {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return nil, ErrInvalidRequest
	}
	target := filepath.Join(filepath.Dir(worldPath), "assets", name)
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() || info.Size() > worldAssetLimit {
		return nil, ErrContentNotFound
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, ErrContentNotFound
	}
	defer file.Close()
	if _, _, err := image.DecodeConfig(file); err != nil {
		return nil, ErrStorageUnavailable
	}
	return os.ReadFile(target)
}

// WorldCharacterAsset serves one character's world copy. Only a file the world's own
// snapshot recorded is reachable, never a client-supplied path.
func (a *App) WorldCharacterAsset(ctx context.Context, worldID, entityID string) ([]byte, string, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return nil, "", err
	}
	if status != "ready" {
		return nil, "", ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return nil, "", err
	}
	defer store.Close()
	name, err := store.MetaGet(ctx, "avatar:"+wire.Clean(entityID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrContentNotFound
		}
		return nil, "", err
	}
	body, err := a.worldAsset(path, name)
	if err != nil {
		return nil, "", err
	}
	mime := "image/png"
	if len(body) > 2 && body[0] == 0xFF && body[1] == 0xD8 {
		mime = "image/jpeg"
	}
	return body, mime, nil
}
