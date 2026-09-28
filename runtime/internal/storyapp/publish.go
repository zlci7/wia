package storyapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Publication turns an edited draft into one immutable package revision. SQLite and
// the file system cannot share a transaction, so the operation records a stage and
// startup recovery finishes or discards it instead of claiming atomicity.
const (
	publishPrepared     = "prepared"
	publishFilesWritten = "files_written"
	publishRenamed      = "renamed"
	publishReady        = "ready"
	publishFailed       = "failed"

	assetLimit = 4 * 1024 * 1024
)

type PublishRequest struct {
	RequestKey             string `json:"request_key"`
	DraftID                string `json:"draft_id"`
	ExpectedDraftVersion   int64  `json:"expected_draft_version"`
	ExpectedProjectVersion int64  `json:"expected_project_version"`
}

type ContentOperation struct {
	OperationID string `json:"operation_id"`
	Kind        string `json:"kind"`
	TargetID    string `json:"target_id"`
	Stage       string `json:"stage"`
	Status      string `json:"status"`
	SafeError   string `json:"safe_error,omitempty"`
}

// PublishContentDraft validates the draft, writes a complete package into a staging
// directory, renames it to its immutable revision directory and only then registers
// it. A repeated request key returns the original result; a different payload
// conflicts.
func (a *App) PublishContentDraft(ctx context.Context, request PublishRequest) (ContentOperation, error) {
	if cleanText(request.RequestKey) == "" || len(request.RequestKey) > 200 || request.ExpectedDraftVersion < 1 {
		return ContentOperation{}, ErrInvalidRequest
	}
	draft, err := a.ReadContentDraft(ctx, request.DraftID)
	if err != nil {
		return ContentOperation{}, err
	}
	hash := hashJSON(request)
	operation, priorHash, found, err := a.readContentOperation(ctx, request.RequestKey)
	if err != nil {
		return ContentOperation{}, err
	}
	if found {
		if priorHash != hash {
			return ContentOperation{}, ErrIdempotencyConflict
		}
		if operation.Status == "succeeded" {
			return operation, nil
		}
		if operation.Status == "failed" {
			return operation, fmt.Errorf("%w: %s", ErrContentInvalid, operation.SafeError)
		}
		// An unfinished operation is resumed rather than duplicated.
		return a.resumePublish(ctx, operation)
	}
	if draft.Version != request.ExpectedDraftVersion {
		return ContentOperation{}, ErrVersionConflict
	}
	project, _, err := a.ReadContentProject(ctx, draft.ProjectID)
	if err != nil {
		return ContentOperation{}, err
	}
	if request.ExpectedProjectVersion > 0 && project.Version != request.ExpectedProjectVersion {
		return ContentOperation{}, ErrVersionConflict
	}
	if _, exists := a.packs[project.GameID]; exists && project.CurrentRevision == "" {
		// An official package owns this identity; user content cannot take it over.
		return ContentOperation{}, ErrContentInvalid
	}
	revision, files, _, err := a.buildPackage(ctx, draft, project)
	if err != nil {
		return ContentOperation{}, err
	}
	operation = ContentOperation{OperationID: newID("publish"), Kind: "publish", TargetID: draft.DraftID, Stage: publishPrepared, Status: "running"}
	if err = a.writeContentOperation(ctx, request.RequestKey, hash, operation, ""); err != nil {
		return ContentOperation{}, err
	}
	return a.runPublish(ctx, request.RequestKey, hash, operation, project, revision, files)
}

func (a *App) runPublish(ctx context.Context, requestKey, hash string, operation ContentOperation, project ContentProject, revision string, files map[string][]byte) (ContentOperation, error) {
	root := a.contentRoot()
	staging := filepath.Join(root, operation.OperationID+".staging")
	final := filepath.Join(root, project.GameID, revision)
	cleanup := func(reason string) (ContentOperation, error) {
		_ = os.RemoveAll(staging)
		operation.Stage, operation.Status = publishFailed, "failed"
		_ = a.writeContentOperation(ctx, requestKey, hash, operation, reason)
		return operation, fmt.Errorf("%w: %s", ErrContentInvalid, reason)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return cleanup("staging directory unavailable")
	}
	for name, body := range files {
		target := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return cleanup("staging directory unavailable")
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return cleanup("package file could not be written")
		}
	}
	// The staged package must load with the same strict rules future worlds use, and
	// the loader's own digest is the published identity.
	staged, err := loadPack(staging)
	if err != nil {
		return cleanup("package validation failed: " + err.Error())
	}
	if staged.Definition.Revision != revision {
		return cleanup("published revision identity does not match")
	}
	digest := staged.Digest
	operation.Stage = publishFilesWritten
	if err = a.writeContentOperation(ctx, requestKey, hash, operation, ""); err != nil {
		return ContentOperation{}, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return cleanup("revision directory unavailable")
	}
	if _, statErr := os.Stat(final); statErr == nil {
		return cleanup("revision already exists")
	}
	if err := os.Rename(staging, final); err != nil {
		return cleanup("revision could not be published")
	}
	operation.Stage = publishRenamed
	if err = a.writeContentOperation(ctx, requestKey, hash, operation, ""); err != nil {
		return ContentOperation{}, err
	}
	return a.completePublish(ctx, requestKey, hash, operation, project, revision, digest, final)
}

// completePublish registers the revision and moves the project forward in one
// application transaction; the directory is already in place.
func (a *App) completePublish(ctx context.Context, requestKey, hash string, operation ContentOperation, project ContentProject, revision, digest, path string) (ContentOperation, error) {
	tx, err := a.appDB.BeginTx(ctx, nil)
	if err != nil {
		return ContentOperation{}, err
	}
	defer tx.Rollback()
	var currentVersion int64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM content_projects WHERE user_id=? AND project_id=? AND deleted_at=''`, a.userID, project.ProjectID).Scan(&currentVersion); err != nil {
		return ContentOperation{}, err
	}
	if currentVersion != project.Version {
		return ContentOperation{}, ErrVersionConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO content_revisions(user_id,game_id,revision,digest,path,status,created_at) VALUES(?,?,?,?,?,?,?)`,
		a.userID, project.GameID, revision, digest, path, publishReady, nowText()); err != nil {
		return ContentOperation{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE content_projects SET current_revision=?,version=version+1,updated_at=? WHERE user_id=? AND project_id=?`,
		revision, nowText(), a.userID, project.ProjectID); err != nil {
		return ContentOperation{}, err
	}
	operation.Stage, operation.Status = publishReady, "succeeded"
	body, err := json.Marshal(map[string]string{"revision": revision, "digest": digest, "game_id": project.GameID})
	if err != nil {
		return ContentOperation{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO content_operations(user_id,request_key,request_hash,operation_id,kind,target_id,stage,status,result_json,safe_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,'',?,?) ON CONFLICT(user_id,request_key) DO UPDATE SET stage=excluded.stage,status=excluded.status,result_json=excluded.result_json,updated_at=excluded.updated_at`,
		a.userID, requestKey, hash, operation.OperationID, operation.Kind, operation.TargetID, operation.Stage, operation.Status, string(body), nowText(), nowText()); err != nil {
		return ContentOperation{}, err
	}
	if err = tx.Commit(); err != nil {
		return ContentOperation{}, err
	}
	a.installPublishedRevision(project.GameID, revision, path)
	return operation, nil
}

// installPublishedRevision loads the finished package once and swaps the in-memory
// catalog entry. A failed load keeps the database record; a later start rebuilds it.
func (a *App) installPublishedRevision(gameID, revision, path string) {
	pack, err := loadPack(path)
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("story content publish: catalog refresh deferred game_id=%q revision=%q", gameID, revision)
		}
		return
	}
	a.packs[gameID] = pack
}

// resumePublish finishes an operation interrupted between stages.
func (a *App) resumePublish(ctx context.Context, operation ContentOperation) (ContentOperation, error) {
	staging := filepath.Join(a.contentRoot(), operation.OperationID+".staging")
	var published struct {
		Revision string `json:"revision"`
		GameID   string `json:"game_id"`
	}
	if _, result, found, err := a.readContentOperationByID(ctx, operation.OperationID); err != nil {
		return ContentOperation{}, err
	} else if found && result != "" {
		_ = json.Unmarshal([]byte(result), &published)
	}
	if published.Revision != "" {
		var path, digest string
		if err := a.appDB.QueryRowContext(ctx, `SELECT path,digest FROM content_revisions WHERE user_id=? AND revision=?`, a.userID, published.Revision).Scan(&path, &digest); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ContentOperation{}, err
		} else if err == nil {
			// The revision is already registered; finish the bookkeeping.
			operation.Stage, operation.Status = publishReady, "succeeded"
			if writeErr := a.writeContentOperation(ctx, "", "", operation, ""); writeErr != nil {
				return ContentOperation{}, writeErr
			}
			a.installPublishedRevision(published.GameID, published.Revision, path)
			return operation, nil
		}
	}
	// The staging directory belongs to this operation, so an interrupted write is
	// discarded once no stored revision claims it.
	operation.Stage, operation.Status = publishFailed, "failed"
	if writeErr := a.writeContentOperation(ctx, "", "", operation, "publication was interrupted; publish the draft again"); writeErr != nil {
		return ContentOperation{}, writeErr
	}
	_ = os.RemoveAll(staging)
	return operation, fmt.Errorf("%w: publication was interrupted; publish the draft again", ErrContentInvalid)
}

func (a *App) readContentOperationByID(ctx context.Context, operationID string) (ContentOperation, string, bool, error) {
	var operation ContentOperation
	var result string
	err := a.appDB.QueryRowContext(ctx, `SELECT operation_id,kind,target_id,stage,status,result_json FROM content_operations WHERE user_id=? AND operation_id=?`, a.userID, operationID).
		Scan(&operation.OperationID, &operation.Kind, &operation.TargetID, &operation.Stage, &operation.Status, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return ContentOperation{}, "", false, nil
	}
	if err != nil {
		return ContentOperation{}, "", false, err
	}
	return operation, result, true, nil
}

// recoverContentOperations runs at startup and only touches operations this
// application recorded; unknown directories are never scanned or deleted.
func (a *App) recoverContentOperations(ctx context.Context) error {
	rows, err := a.appDB.QueryContext(ctx, `SELECT operation_id,target_id,stage FROM content_operations WHERE user_id=? AND status='running'`, a.userID)
	if err != nil {
		return err
	}
	type pending struct{ id, target, stage string }
	var list []pending
	for rows.Next() {
		var item pending
		if err = rows.Scan(&item.id, &item.target, &item.stage); err != nil {
			rows.Close()
			return err
		}
		list = append(list, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, item := range list {
		operation := ContentOperation{OperationID: item.id, Kind: "publish", TargetID: item.target, Stage: item.stage, Status: "running"}
		if _, err = a.resumePublish(ctx, operation); err != nil {
			if a.logger != nil {
				a.logger.Printf("story content recovery: operation_id=%q stage=%q", item.id, item.stage)
			}
		}
	}
	return nil
}

// buildPackage assembles the complete package for one draft. Validation and the
// package digest both come from the real loader, so a published revision is exactly
// what a later world would read.
func (a *App) buildPackage(ctx context.Context, draft ContentDraft, project ContentProject) (string, map[string][]byte, string, error) {
	payload := draft.Payload
	if err := validateDraftPayload(payload); err != nil {
		return "", nil, "", err
	}
	npcFiles, err := draftNPCFiles(payload)
	if err != nil {
		return "", nil, "", err
	}
	assets := map[string][]byte{}
	for _, asset := range referencedAssets(payload) {
		body, err := a.draftAsset(ctx, draft.DraftID, asset)
		if err != nil {
			return "", nil, "", err
		}
		assets[asset] = body
	}
	names := make([]string, 0, len(npcFiles))
	for name := range npcFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	story := StoryPack{
		SchemaVersion: packSchemaV2, GameID: project.GameID, Revision: "", Mode: payload.Mode, Title: payload.Title,
		Description: payload.Description, Gameplay: payload.Gameplay, Background: payload.Background, Rules: payload.Rules,
		AuthorFacts: payload.AuthorFacts, Cover: payload.Cover, CoverAlt: payload.CoverAlt, Player: payload.Player,
		Opening: payload.Opening, InitialLocation: payload.InitialLocation, Clock: payload.Clock, Locations: payload.Locations,
		NPCs: names, Bystanders: payload.Bystanders, Plot: payload.Plot, EventGeneration: payload.EventGeneration,
		Defaults: payload.Defaults,
	}
	revision, err := newContentRevision(story, npcFiles)
	if err != nil {
		return "", nil, "", err
	}
	story.Revision = revision
	files := map[string][]byte{}
	body, err := json.Marshal(story)
	if err != nil {
		return "", nil, "", err
	}
	files["story.json"] = body
	for name, npc := range npcFiles {
		npcBody, err := json.Marshal(npc)
		if err != nil {
			return "", nil, "", err
		}
		files[name] = npcBody
	}
	for name, asset := range assets {
		files[name] = asset
	}
	return revision, files, "", nil
}

// newContentRevision derives an immutable revision identity from the frozen
// content, excluding the revision field itself.
func newContentRevision(story StoryPack, npcs map[string]PackNPC) (string, error) {
	names := make([]string, 0, len(npcs))
	for name := range npcs {
		names = append(names, name)
	}
	sort.Strings(names)
	bodies := make([]json.RawMessage, 0, len(names))
	for _, name := range names {
		body, err := json.Marshal(npcs[name])
		if err != nil {
			return "", err
		}
		bodies = append(bodies, body)
	}
	canonical, err := json.Marshal(struct {
		Story StoryPack
		NPCs  []json.RawMessage
	}{story, bodies})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	revision := "r-" + nowText()[:10] + "-" + hex.EncodeToString(sum[:])[:8]
	if !packID.MatchString(revision) {
		return "", fmt.Errorf("%w: revision identity", ErrContentInvalid)
	}
	return revision, nil
}

// referencedAssets lists the package-relative assets a draft refers to.
func referencedAssets(payload ContentDraftPayload) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(path string) {
		path = cleanText(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	add(payload.Cover)
	for _, npc := range payload.NPCs {
		add(npc.Avatar)
	}
	for _, bystander := range payload.Bystanders {
		add(bystander.Avatar)
	}
	sort.Strings(out)
	return out
}

// draftAsset resolves one staged asset of the draft. Uploads arrive in a later unit;
// until then a referenced asset that is absent fails the publication explicitly.
func (a *App) draftAsset(ctx context.Context, draftID, relative string) ([]byte, error) {
	var staged string
	err := a.appDB.QueryRowContext(ctx, `SELECT staged_path FROM content_draft_assets WHERE user_id=? AND draft_id=? AND relative_name=?`, a.userID, draftID, relative).Scan(&staged)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: asset %s has not been uploaded", ErrContentInvalid, relative)
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(staged)
	if err != nil || !info.Mode().IsRegular() || info.Size() > assetLimit {
		return nil, fmt.Errorf("%w: asset %s is unavailable", ErrContentInvalid, relative)
	}
	body, err := os.ReadFile(staged)
	if err != nil {
		return nil, fmt.Errorf("%w: asset %s is unavailable", ErrContentInvalid, relative)
	}
	return body, nil
}

func (a *App) contentRoot() string {
	if a.contentDir != "" {
		return a.contentDir
	}
	return filepath.Join(a.root, "content")
}

func (a *App) readContentOperation(ctx context.Context, requestKey string) (ContentOperation, string, bool, error) {
	var operation ContentOperation
	var hash, result string
	err := a.appDB.QueryRowContext(ctx, `SELECT request_hash,operation_id,kind,target_id,stage,status,result_json FROM content_operations WHERE user_id=? AND request_key=?`, a.userID, requestKey).
		Scan(&hash, &operation.OperationID, &operation.Kind, &operation.TargetID, &operation.Stage, &operation.Status, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return ContentOperation{}, "", false, nil
	}
	if err != nil {
		return ContentOperation{}, "", false, err
	}
	if operation.Status == "succeeded" && operation.Stage != publishReady {
		operation.Stage = publishReady
	}
	return operation, hash, true, nil
}

func (a *App) writeContentOperation(ctx context.Context, requestKey, hash string, operation ContentOperation, safeError string) error {
	if requestKey == "" {
		_, err := a.appDB.ExecContext(ctx, `UPDATE content_operations SET stage=?,status=?,safe_error=?,updated_at=? WHERE user_id=? AND operation_id=?`,
			operation.Stage, operation.Status, safeError, nowText(), a.userID, operation.OperationID)
		return err
	}
	_, err := a.appDB.ExecContext(ctx, `INSERT INTO content_operations(user_id,request_key,request_hash,operation_id,kind,target_id,stage,status,result_json,safe_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,'',?,?,?) ON CONFLICT(user_id,request_key) DO UPDATE SET stage=excluded.stage,status=excluded.status,safe_error=excluded.safe_error,updated_at=excluded.updated_at`,
		a.userID, requestKey, hash, operation.OperationID, operation.Kind, operation.TargetID, operation.Stage, operation.Status, safeError, nowText(), nowText())
	if err != nil {
		return err
	}
	// An operation can also be addressed by its own id, which is how recovery
	// finishes bookkeeping without the original request key.
	_, err = a.appDB.ExecContext(ctx, `UPDATE content_operations SET stage=?,status=?,safe_error=?,updated_at=? WHERE user_id=? AND operation_id=?`,
		operation.Stage, operation.Status, safeError, nowText(), a.userID, operation.OperationID)
	return err
}

func (a *App) ReadContentOperation(ctx context.Context, operationID string) (ContentOperation, error) {
	var operation ContentOperation
	var result string
	err := a.appDB.QueryRowContext(ctx, `SELECT operation_id,kind,target_id,stage,status,result_json FROM content_operations WHERE user_id=? AND operation_id=?`, a.userID, strings.TrimSpace(operationID)).
		Scan(&operation.OperationID, &operation.Kind, &operation.TargetID, &operation.Stage, &operation.Status, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return ContentOperation{}, ErrContentNotFound
	}
	if err != nil {
		return ContentOperation{}, err
	}
	return operation, nil
}

// loadPublishedRevisions restores user revisions into the in-memory catalog so a
// restart keeps published content available without re-publishing it.
func (a *App) loadPublishedRevisions(ctx context.Context) error {
	rows, err := a.appDB.QueryContext(ctx, `SELECT game_id,revision,path FROM content_revisions WHERE user_id=? AND status=?`, a.userID, publishReady)
	if err != nil {
		return err
	}
	type record struct{ gameID, revision, path string }
	var list []record
	for rows.Next() {
		var item record
		if err = rows.Scan(&item.gameID, &item.revision, &item.path); err != nil {
			rows.Close()
			return err
		}
		list = append(list, item)
	}
	rows.Close()
	for _, item := range list {
		pack, err := loadPack(item.path)
		if err != nil || pack.Definition.Revision != item.revision {
			a.packErrors = append(a.packErrors, PackIssue{item.gameID, "已发布修订无法读取，请重新发布"})
			continue
		}
		current, exists := a.packs[item.gameID]
		if exists && current.Definition.Revision != "" {
			// The newest published revision wins the catalog entry.
			if current.Definition.Revision > item.revision {
				continue
			}
		}
		a.packs[item.gameID] = pack
	}
	return nil
}

func hashJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
