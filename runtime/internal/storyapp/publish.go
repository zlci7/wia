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

// publishPlan is the frozen decision a publication follows: the identity it will
// create and the digest it must match. It is persisted before any file is written so
// an interruption after the rename can still be finished instead of discarded.
type publishPlan struct {
	GameID         string `json:"game_id"`
	ProjectID      string `json:"project_id"`
	ProjectVersion int64  `json:"project_version"`
	Revision       string `json:"revision"`
	Digest         string `json:"digest"`
	FinalPath      string `json:"final_path"`
	// Digest before the rename is unknown, so validation re-derives it from staging.
}

// PublishContentDraft validates the draft, writes a complete package into a staging
// directory, renames it to its immutable revision directory and only then registers
// it. A repeated request key returns the original result; a different payload
// conflicts; the same key while the original request is still running returns that
// live operation instead of treating it as a crash to recover.
func (a *App) PublishContentDraft(ctx context.Context, request PublishRequest) (ContentOperation, error) {
	if cleanText(request.RequestKey) == "" || len(request.RequestKey) > 200 || request.ExpectedDraftVersion < 1 {
		return ContentOperation{}, ErrInvalidRequest
	}
	if live, ok := a.liveOperation(request.RequestKey); ok {
		return live, nil
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
	if _, exists := a.pack(project.GameID); exists && project.CurrentRevision == "" {
		// An official package owns this identity; user content cannot take it over.
		return ContentOperation{}, ErrContentInvalid
	}
	return a.startPublish(ctx, request, draft, project, hash)
}

// existingRevision reports a publication of this exact content, including one that a
// previous interruption registered but did not point the project at.
func (a *App) existingRevision(ctx context.Context, revision string) (ContentRevisionRef, bool, error) {
	var ref ContentRevisionRef
	ref.Revision = revision
	err := a.appDB.QueryRowContext(ctx, `SELECT path,digest FROM content_revisions WHERE user_id=? AND revision=? AND status=?`, a.userID, revision, publishReady).Scan(&ref.Path, &ref.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return ContentRevisionRef{}, false, nil
	}
	if err != nil {
		return ContentRevisionRef{}, false, err
	}
	return ref, true, nil
}

// ContentRevisionRef identifies one already registered publication.
type ContentRevisionRef struct {
	Revision string
	Digest   string
	Path     string
}

// registerRevision points the project at an already published revision.
func (a *App) registerRevision(ctx context.Context, requestKey, hash string, operation ContentOperation, project ContentProject, revision, digest, path string) error {
	_, err := a.completePublish(ctx, requestKey, hash, operation, publishPlan{
		GameID: project.GameID, ProjectID: project.ProjectID, ProjectVersion: project.Version,
		Revision: revision, Digest: digest, FinalPath: path,
	}, digest)
	return err
}

func (a *App) startPublish(ctx context.Context, request PublishRequest, draft ContentDraft, project ContentProject, hash string) (ContentOperation, error) {
	revision, files, _, err := a.buildPackage(ctx, draft, project)
	if err != nil {
		return ContentOperation{}, err
	}
	// A revision is content-addressed, so publishing the same content again names the
	// same immutable directory. That is the same publication, not a conflict: point the
	// project at it and report success.
	if existing, found, err := a.existingRevision(ctx, revision); err != nil {
		return ContentOperation{}, err
	} else if found {
		operation := ContentOperation{OperationID: newID("publish"), Kind: "publish", TargetID: draft.DraftID, Stage: publishReady, Status: "succeeded"}
		if err = a.registerRevision(ctx, request.RequestKey, hash, operation, project, existing.Revision, existing.Digest, existing.Path); err != nil {
			return ContentOperation{}, err
		}
		return operation, nil
	}
	operation := ContentOperation{OperationID: newID("publish"), Kind: "publish", TargetID: draft.DraftID, Stage: publishPrepared, Status: "running"}
	plan := publishPlan{
		GameID: project.GameID, ProjectID: project.ProjectID, ProjectVersion: project.Version,
		Revision: revision, FinalPath: filepath.Join(a.contentRoot(), project.GameID, revision),
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return ContentOperation{}, err
	}
	// The plan is durably recorded before any file is written, so an interruption after
	// the rename is recoverable instead of leaving an unclaimed directory behind.
	if err = a.writeContentOperationPlan(ctx, request.RequestKey, hash, operation, string(body)); err != nil {
		return ContentOperation{}, err
	}
	a.beginLiveOperation(request.RequestKey, operation)
	defer a.endLiveOperation(request.RequestKey)
	return a.runPublish(ctx, request.RequestKey, hash, operation, plan, files)
}

func (a *App) runPublish(ctx context.Context, requestKey, hash string, operation ContentOperation, plan publishPlan, files map[string][]byte) (ContentOperation, error) {
	root := a.contentRoot()
	staging := filepath.Join(root, operation.OperationID+".staging")
	final := plan.FinalPath
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
	if staged.Definition.Revision != plan.Revision {
		return cleanup("published revision identity does not match")
	}
	digest := staged.Digest
	// Record the digest the plan will verify before anything is renamed, so recovery
	// checks the frozen content rather than only the revision name.
	plan.Digest = digest
	if body, marshalErr := json.Marshal(plan); marshalErr == nil {
		if err = a.writeContentOperationPlanDigest(ctx, operation.OperationID, string(body)); err != nil {
			return ContentOperation{}, err
		}
	}
	operation.Stage = publishFilesWritten
	if err = a.writeContentOperation(ctx, requestKey, hash, operation, ""); err != nil {
		return ContentOperation{}, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return cleanup("revision directory unavailable")
	}
	if _, statErr := os.Stat(final); statErr == nil {
		// The directory already exists. It is only acceptable when it is this exact
		// package, which makes an interrupted rename idempotent instead of a dead end.
		existing, loadErr := loadPack(final)
		if loadErr != nil || existing.Digest != digest {
			return cleanup("revision already exists")
		}
		_ = os.RemoveAll(staging)
		operation.Stage = publishRenamed
		if err = a.writeContentOperation(ctx, requestKey, hash, operation, ""); err != nil {
			return ContentOperation{}, err
		}
		return a.completePublish(ctx, requestKey, hash, operation, plan, digest)
	}
	if err := os.Rename(staging, final); err != nil {
		return cleanup("revision could not be published")
	}
	operation.Stage = publishRenamed
	if err = a.writeContentOperation(ctx, requestKey, hash, operation, ""); err != nil {
		return ContentOperation{}, err
	}
	return a.completePublish(ctx, requestKey, hash, operation, plan, digest)
}

// completePublish registers the revision and moves the project forward in one
// application transaction; the directory is already in place.
func (a *App) completePublish(ctx context.Context, requestKey, hash string, operation ContentOperation, plan publishPlan, digest string) (ContentOperation, error) {
	revision, project := plan.Revision, ContentProject{ProjectID: plan.ProjectID, GameID: plan.GameID, Version: plan.ProjectVersion}
	path := plan.FinalPath
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
	if _, err = tx.ExecContext(ctx, `INSERT INTO content_revisions(user_id,game_id,revision,digest,path,status,created_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(user_id,game_id,revision) DO UPDATE SET digest=excluded.digest,path=excluded.path,status=excluded.status`,
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
	// The operation is addressed by its own id, so finishing an interrupted publication
	// updates the row that recorded it. Inserting under an empty request key would leave
	// the original key stuck in `running` and make a retry look like a conflict.
	if _, err = tx.ExecContext(ctx, `UPDATE content_operations SET stage=?,status=?,result_json=?,safe_error='',updated_at=? WHERE user_id=? AND operation_id=?`,
		operation.Stage, operation.Status, string(body), nowText(), a.userID, operation.OperationID); err != nil {
		return ContentOperation{}, err
	}
	if requestKey != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE content_operations SET request_hash=?,updated_at=? WHERE user_id=? AND request_key=?`, hash, nowText(), a.userID, requestKey); err != nil {
			return ContentOperation{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return ContentOperation{}, err
	}
	a.installPublishedRevision(project.GameID, revision, path)
	return operation, nil
}

// installPublishedRevision loads the finished package once and swaps the in-memory
// catalog entry. Loading and verifying happen outside the directory lock; only the
// entry swap is inside it. A failed load keeps the database record; a later start
// rebuilds it.
func (a *App) installPublishedRevision(gameID, revision, path string) {
	pack, err := loadPack(path)
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("story content publish: catalog refresh deferred game_id=%q revision=%q", gameID, revision)
		}
		return
	}
	a.setPack(gameID, pack)
}

// resumePublish finishes an operation interrupted between stages. The frozen plan
// says which directory and digest the publication was creating, so a rename that
// completed before the interruption is finished rather than discarded.
func (a *App) resumePublish(ctx context.Context, operation ContentOperation) (ContentOperation, error) {
	if live, ok := a.liveOperation(operation.OperationID); ok {
		return live, nil
	}
	raw, err := a.readContentOperationPlan(ctx, operation.OperationID)
	if err != nil {
		return ContentOperation{}, err
	}
	plan := publishPlan{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &plan)
	}
	staging := filepath.Join(a.contentRoot(), operation.OperationID+".staging")
	// Recover the original request identity from the row that recorded this operation, so
	// finishing it updates that row instead of creating an unrelated one.
	requestKey, requestHash, err := a.operationRequestIdentity(ctx, operation.OperationID)
	if err != nil {
		return ContentOperation{}, err
	}
	if plan.Revision != "" {
		if _, statErr := os.Stat(plan.FinalPath); statErr == nil {
			// The directory is already in place; only registration may be missing.
			pack, loadErr := loadPack(plan.FinalPath)
			if loadErr == nil && pack.Definition.Revision == plan.Revision && (plan.Digest == "" || plan.Digest == pack.Digest) {
				operation.Stage = publishRenamed
				return a.completePublish(ctx, requestKey, requestHash, operation, plan, pack.Digest)
			}
		}
		// The staging directory exists only for this operation, so it can be finished
		// here instead of thrown away.
		if _, statErr := os.Stat(staging); statErr == nil {
			staged, loadErr := loadPack(staging)
			if loadErr == nil && staged.Definition.Revision == plan.Revision && (plan.Digest == "" || plan.Digest == staged.Digest) {
				if err = os.MkdirAll(filepath.Dir(plan.FinalPath), 0o755); err == nil {
					if err = os.Rename(staging, plan.FinalPath); err == nil {
						operation.Stage = publishRenamed
						return a.completePublish(ctx, requestKey, requestHash, operation, plan, staged.Digest)
					}
				}
			}
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

// writeContentOperationPlanDigest updates only the frozen plan, keeping the operation's
// recorded request identity untouched.
func (a *App) writeContentOperationPlanDigest(ctx context.Context, operationID, plan string) error {
	_, err := a.appDB.ExecContext(ctx, `UPDATE content_operations SET plan_json=?,updated_at=? WHERE user_id=? AND operation_id=?`, plan, nowText(), a.userID, operationID)
	return err
}

// operationRequestIdentity finds the request key and hash that recorded an operation, so
// finishing it updates that same row.
func (a *App) operationRequestIdentity(ctx context.Context, operationID string) (string, string, error) {
	var requestKey, requestHash string
	err := a.appDB.QueryRowContext(ctx, `SELECT request_key,request_hash FROM content_operations WHERE user_id=? AND operation_id=? AND request_key<>''`, a.userID, operationID).Scan(&requestKey, &requestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return requestKey, requestHash, nil
}

func (a *App) readContentOperationPlan(ctx context.Context, operationID string) (string, error) {
	var plan string
	err := a.appDB.QueryRowContext(ctx, `SELECT plan_json FROM content_operations WHERE user_id=? AND operation_id=?`, a.userID, operationID).Scan(&plan)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return plan, err
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
	revision, err := newContentRevision(story, npcFiles, assets)
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

// newContentRevision derives an immutable revision identity from the frozen content,
// excluding the revision field itself but including every referenced image, so two
// publications can never share an identifier while differing in a byte a world shows.
func newContentRevision(story StoryPack, npcs map[string]PackNPC, assets map[string][]byte) (string, error) {
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
	assetNames := make([]string, 0, len(assets))
	for name := range assets {
		assetNames = append(assetNames, name)
	}
	sort.Strings(assetNames)
	type assetEntry struct {
		Name string
		Body []byte
	}
	assetList := make([]assetEntry, 0, len(assetNames))
	for _, name := range assetNames {
		assetList = append(assetList, assetEntry{Name: name, Body: assets[name]})
	}
	canonical, err := json.Marshal(struct {
		Story  StoryPack
		NPCs   []json.RawMessage
		Assets []assetEntry
	}{story, bodies, assetList})
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

// writeContentOperationPlan records the frozen plan together with the operation, so
// recovery knows exactly which directory and digest it is finishing.
func (a *App) writeContentOperationPlan(ctx context.Context, requestKey, hash string, operation ContentOperation, plan string) error {
	_, err := a.appDB.ExecContext(ctx, `INSERT INTO content_operations(user_id,request_key,request_hash,operation_id,kind,target_id,stage,status,result_json,safe_error,plan_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,'','',?,?,?) ON CONFLICT(user_id,request_key) DO UPDATE SET stage=excluded.stage,status=excluded.status,plan_json=excluded.plan_json,updated_at=excluded.updated_at`,
		a.userID, requestKey, hash, operation.OperationID, operation.Kind, operation.TargetID, operation.Stage, operation.Status, plan, nowText(), nowText())
	return err
}

// liveOperations tracks publications running in this process. A repeated request for
// one of them returns that operation; it is never treated as an interruption to
// recover, because the original writer is still working.
type liveOperation struct {
	operation ContentOperation
}

func (a *App) beginLiveOperation(requestKey string, operation ContentOperation) {
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	if a.liveOps == nil {
		a.liveOps = map[string]ContentOperation{}
	}
	a.liveOps[requestKey] = operation
}

func (a *App) endLiveOperation(requestKey string) {
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	delete(a.liveOps, requestKey)
}

func (a *App) liveOperation(requestKey string) (ContentOperation, bool) {
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	operation, ok := a.liveOps[requestKey]
	return operation, ok
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
// restart keeps published content available without re-publishing it. The project's
// own current revision is the authority: revision identifiers order by their digest,
// which says nothing about which publish came last, so the catalog must not guess.
func (a *App) loadPublishedRevisions(ctx context.Context) error {
	current, err := a.projectRevisions(ctx)
	if err != nil {
		return err
	}
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
			a.addPackIssue(PackIssue{item.gameID, "已发布修订无法读取，请重新发布"})
			continue
		}
		if wanted, known := current[item.gameID]; known && wanted != "" && wanted != item.revision {
			// An older revision of the same story stays on disk and stays exportable,
			// but the catalog serves what the project currently points at.
			continue
		}
		a.setPack(item.gameID, pack)
	}
	return nil
}

// projectRevisions maps each story to the revision its project currently publishes.
func (a *App) projectRevisions(ctx context.Context) (map[string]string, error) {
	rows, err := a.appDB.QueryContext(ctx, `SELECT game_id,current_revision FROM content_projects WHERE user_id=?`, a.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var gameID, revision string
		if err = rows.Scan(&gameID, &revision); err != nil {
			return nil, err
		}
		if revision != "" {
			out[gameID] = revision
		}
	}
	return out, rows.Err()
}

func (a *App) addPackIssue(issue PackIssue) {
	a.packsMu.Lock()
	defer a.packsMu.Unlock()
	a.packErrors = append(a.packErrors, issue)
}

func hashJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
