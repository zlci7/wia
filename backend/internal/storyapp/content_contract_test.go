package storyapp

import (
	"archive/zip"
	"bytes"
	"io"
	"path/filepath"
	"regexp"

	"gameagent/backend/internal/content"
)

type ContentProject = content.ContentProject
type ContentDraft = content.ContentDraft
type ContentDraftNPC = content.ContentDraftNPC
type ContentDraftPayload = content.ContentDraftPayload
type ContentOperation = content.ContentOperation
type PublishRequest = content.PublishRequest
type StoryPack = content.StoryPack
type PackNPC = content.PackNPC
type loadedPack = content.LoadedPack

var packID = runtimeID
var entityID = regexp.MustCompile(`^npc:[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)
var loadPack = content.Load
var ReadUploadedAsset = content.ReadUploadedAsset
var draftNPCFiles = content.DraftNPCFiles
var normalizePackBystanders = content.NormalizePackBystanders
var bystanderIDPattern = regexp.MustCompile(`^bystander:[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

const (
	assetImageLimit    = 4 * 1024 * 1024
	importTextLimit    = 1 * 1024 * 1024
	importUnzipLimit   = 16 * 1024 * 1024
	draftStatusPreview = "import_preview"
	draftStatusEditing = "editing"
	packDigestVersion  = 2
	publishReady       = "ready"
)

func testPack(a *App, id string) content.LoadedPack {
	pack, _ := a.Pack(id)
	return pack
}

func testImportSourcePath(a *App, draftID, fileName string) string {
	return filepath.Join(a.root, "content", "imports", draftID+"-"+fileName)
}

func testContentRoot(a *App) string { return filepath.Join(a.root, "content") }

func readPackageZip(body []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			return nil, err
		}
		files[entry.Name], err = io.ReadAll(file)
		_ = file.Close()
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}
