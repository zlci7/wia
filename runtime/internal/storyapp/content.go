package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// M3 content workspace: an author edits a draft of a story package and publishes
// it as an immutable revision. The draft is application-owned data; it never
// touches a world until it is published and a new world starts from it.
const (
	draftPayloadLimit  = 2 * 1024 * 1024
	draftStatusEditing = "editing"
	draftStatusPreview = "import_preview"
)

type ContentProject struct {
	ProjectID       string `json:"project_id"`
	GameID          string `json:"game_id"`
	Title           string `json:"title"`
	CurrentRevision string `json:"current_revision"`
	Version         int64  `json:"version"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type ContentDraftSummary struct {
	DraftID      string `json:"draft_id"`
	ProjectID    string `json:"project_id"`
	BaseRevision string `json:"base_revision"`
	Version      int64  `json:"version"`
	Status       string `json:"status"`
	UpdatedAt    string `json:"updated_at"`
}

type ContentDraft struct {
	ContentDraftSummary
	Payload ContentDraftPayload `json:"payload"`
}

// ContentDraftPayload is the editable draft shape. Every field is optional so the
// editor can save partial work; publish validates the strict package schema.
type ContentDraftPayload struct {
	SchemaVersion   int                    `json:"schema_version"`
	GameID          string                 `json:"game_id"`
	Mode            string                 `json:"mode"`
	Title           string                 `json:"title"`
	Description     string                 `json:"description"`
	Gameplay        string                 `json:"gameplay"`
	Background      string                 `json:"background"`
	Rules           string                 `json:"rules"`
	AuthorFacts     string                 `json:"author_facts"`
	Cover           string                 `json:"cover,omitempty"`
	CoverAlt        string                 `json:"cover_alt,omitempty"`
	Player          PlayerDefaults         `json:"player"`
	Opening         string                 `json:"opening"`
	InitialLocation string                 `json:"initial_location"`
	Clock           string                 `json:"clock"`
	Locations       []PackLocation         `json:"locations"`
	NPCs            []string               `json:"npcs"`
	Bystanders      []PackBystander        `json:"bystanders"`
	Plot            *PlotDefinition        `json:"plot,omitempty"`
	EventGeneration *EventGenerationPolicy `json:"event_generation,omitempty"`
	Defaults        *NarrativeSettings     `json:"defaults,omitempty"`
}

const payloadTooLarge = "payload is too large"

// CreateContentProject reserves a stable game identity for one author project.
// Official packages and other projects of the same account cannot be taken over.
func (a *App) CreateContentProject(ctx context.Context, gameID, title string) (ContentProject, error) {
	gameID, title = cleanText(gameID), cleanText(title)
	if !packID.MatchString(gameID) || title == "" || len([]rune(title)) > 120 {
		return ContentProject{}, ErrInvalidRequest
	}
	if _, official := a.packs[gameID]; official {
		return ContentProject{}, ErrContentInvalid
	}
	var existing int
	if err := a.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_projects WHERE user_id=? AND game_id=?`, a.userID, gameID).Scan(&existing); err != nil {
		return ContentProject{}, err
	}
	if existing > 0 {
		return ContentProject{}, ErrContentBusy
	}
	now := nowText()
	project := ContentProject{ProjectID: newID("project"), GameID: gameID, Title: title, Version: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := a.appDB.ExecContext(ctx, `INSERT INTO content_projects(user_id,project_id,game_id,title,current_revision,version,deleted_at,created_at,updated_at) VALUES(?,?,?,?,'',1,'',?,?)`,
		a.userID, project.ProjectID, project.GameID, project.Title, now, now); err != nil {
		return ContentProject{}, err
	}
	return project, nil
}

func (a *App) ListContentProjects(ctx context.Context) ([]ContentProject, error) {
	rows, err := a.appDB.QueryContext(ctx, `SELECT project_id,game_id,title,current_revision,version,created_at,updated_at FROM content_projects WHERE user_id=? AND deleted_at='' ORDER BY updated_at DESC, project_id`, a.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContentProject{}
	for rows.Next() {
		var p ContentProject
		if err = rows.Scan(&p.ProjectID, &p.GameID, &p.Title, &p.CurrentRevision, &p.Version, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// onlyListProjectSummary keeps the catalog cheap: full draft payloads load when an
// editor opens, never while listing.
func (a *App) ReadContentProject(ctx context.Context, projectID string) (ContentProject, []ContentDraftSummary, error) {
	var p ContentProject
	err := a.appDB.QueryRowContext(ctx, `SELECT project_id,game_id,title,current_revision,version,created_at,updated_at FROM content_projects WHERE user_id=? AND project_id=? AND deleted_at=''`, a.userID, strings.TrimSpace(projectID)).
		Scan(&p.ProjectID, &p.GameID, &p.Title, &p.CurrentRevision, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ContentProject{}, nil, ErrContentNotFound
	}
	if err != nil {
		return ContentProject{}, nil, err
	}
	rows, err := a.appDB.QueryContext(ctx, `SELECT draft_id,project_id,base_revision,version,status,updated_at FROM content_drafts WHERE user_id=? AND project_id=? ORDER BY updated_at DESC, draft_id`, a.userID, p.ProjectID)
	if err != nil {
		return ContentProject{}, nil, err
	}
	defer rows.Close()
	drafts := []ContentDraftSummary{}
	for rows.Next() {
		var d ContentDraftSummary
		if err = rows.Scan(&d.DraftID, &d.ProjectID, &d.BaseRevision, &d.Version, &d.Status, &d.UpdatedAt); err != nil {
			return ContentProject{}, nil, err
		}
		drafts = append(drafts, d)
	}
	return p, drafts, rows.Err()
}

// CreateContentDraft starts from blank or from an existing package revision. The
// project identity is fixed at creation and cannot be renamed into another one.
func (a *App) CreateContentDraft(ctx context.Context, projectID, baseRevision string) (ContentDraft, error) {
	project, _, err := a.ReadContentProject(ctx, projectID)
	if err != nil {
		return ContentDraft{}, err
	}
	baseRevision = cleanText(baseRevision)
	payload := ContentDraftPayload{SchemaVersion: packSchemaV2, GameID: project.GameID, Mode: "open", Title: project.Title, InitialLocation: "", NPCs: []string{}, Locations: []PackLocation{}, Bystanders: []PackBystander{}}
	status := draftStatusEditing
	if baseRevision != "" {
		copied, err := a.draftPayloadFromRevision(baseRevision, project.GameID)
		if err != nil {
			return ContentDraft{}, err
		}
		payload = copied
	}
	if err = validateDraftPayload(payload); err != nil {
		return ContentDraft{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ContentDraft{}, err
	}
	draft := ContentDraft{ContentDraftSummary: ContentDraftSummary{DraftID: newID("draft"), ProjectID: project.ProjectID, BaseRevision: baseRevision, Version: 1, Status: status, UpdatedAt: nowText()}, Payload: payload}
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_drafts(user_id,draft_id,project_id,base_revision,version,status,payload_json,source_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'',?,?)`,
		a.userID, draft.DraftID, draft.ProjectID, draft.BaseRevision, draft.Version, draft.Status, string(body), draft.UpdatedAt, draft.UpdatedAt); err != nil {
		return ContentDraft{}, err
	}
	return draft, nil
}

func (a *App) ReadContentDraft(ctx context.Context, draftID string) (ContentDraft, error) {
	var draft ContentDraft
	var raw string
	err := a.appDB.QueryRowContext(ctx, `SELECT draft_id,project_id,base_revision,version,status,payload_json,updated_at FROM content_drafts WHERE user_id=? AND draft_id=?`, a.userID, strings.TrimSpace(draftID)).
		Scan(&draft.DraftID, &draft.ProjectID, &draft.BaseRevision, &draft.Version, &draft.Status, &raw, &draft.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ContentDraft{}, ErrContentNotFound
	}
	if err != nil {
		return ContentDraft{}, err
	}
	if err = json.Unmarshal([]byte(raw), &draft.Payload); err != nil {
		return ContentDraft{}, ErrStorageUnavailable
	}
	return draft, nil
}

// SaveContentDraft writes one versioned draft. A stale expected version keeps the
// stored draft intact so the author can reload or copy instead of losing work.
func (a *App) SaveContentDraft(ctx context.Context, draftID string, payload ContentDraftPayload, expectedVersion int64) (ContentDraft, error) {
	if expectedVersion < 1 {
		return ContentDraft{}, ErrInvalidRequest
	}
	if err := validateDraftPayload(payload); err != nil {
		return ContentDraft{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ContentDraft{}, err
	}
	if len(body) > draftPayloadLimit {
		return ContentDraft{}, fmt.Errorf("%w: %s", ErrContentInvalid, payloadTooLarge)
	}
	result, err := a.appDB.ExecContext(ctx, `UPDATE content_drafts SET payload_json=?,version=version+1,updated_at=? WHERE user_id=? AND draft_id=? AND version=? AND status IN (?,?)`,
		string(body), nowText(), a.userID, strings.TrimSpace(draftID), expectedVersion, draftStatusEditing, draftStatusPreview)
	if err != nil {
		return ContentDraft{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ContentDraft{}, err
	}
	if affected == 0 {
		if _, readErr := a.ReadContentDraft(ctx, draftID); errors.Is(readErr, ErrContentNotFound) {
			return ContentDraft{}, ErrContentNotFound
		} else if readErr != nil {
			return ContentDraft{}, readErr
		}
		return ContentDraft{}, ErrVersionConflict
	}
	return a.ReadContentDraft(ctx, draftID)
}

func (a *App) DeleteContentDraft(ctx context.Context, draftID string) error {
	result, err := a.appDB.ExecContext(ctx, `DELETE FROM content_drafts WHERE user_id=? AND draft_id=?`, a.userID, strings.TrimSpace(draftID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrContentNotFound
	}
	return nil
}

// The draft stays a package candidate: identity and required fields are checked
// here, while publication applies the full strict package schema.
func validateDraftPayload(payload ContentDraftPayload) error {
	if payload.GameID != "" && !packID.MatchString(payload.GameID) {
		return fmt.Errorf("%w: game_id", ErrContentInvalid)
	}
	if payload.Mode != "" && payload.Mode != "open" && payload.Mode != "guided" {
		return fmt.Errorf("%w: mode", ErrContentInvalid)
	}
	if len([]rune(payload.Title)) > 120 || len([]rune(payload.Description)) > 8000 || len([]rune(payload.Background)) > 8000 || len([]rune(payload.Rules)) > 8000 || len([]rune(payload.AuthorFacts)) > 8000 {
		return fmt.Errorf("%w: text field length", ErrContentInvalid)
	}
	if len([]rune(payload.Player.Name)) > 80 || len([]rune(payload.Player.Profile)) > 2000 {
		return fmt.Errorf("%w: player defaults", ErrContentInvalid)
	}
	if len(payload.Locations) > 32 || len(payload.NPCs) > 16 || len(payload.Bystanders) > 40 {
		return fmt.Errorf("%w: collection size", ErrContentInvalid)
	}
	seen := map[string]bool{}
	for _, location := range payload.Locations {
		if !packID.MatchString(location.ID) || strings.TrimSpace(location.Name) == "" || seen[location.ID] {
			return fmt.Errorf("%w: locations", ErrContentInvalid)
		}
		seen[location.ID] = true
	}
	if payload.Defaults != nil {
		if _, err := validateNarrativeSettings(*payload.Defaults); err != nil {
			return fmt.Errorf("%w: defaults", ErrContentInvalid)
		}
	}
	return nil
}

// publishedPack resolves one immutable revision by identity. Official packages are
// already loaded; user revisions resolve through the registered content path.
func (a *App) publishedPack(revision string) (loadedPack, error) {
	revision = cleanText(revision)
	for _, pack := range a.packs {
		if pack.Definition.Revision == revision {
			return pack, nil
		}
	}
	var path string
	err := a.appDB.QueryRowContext(context.Background(), `SELECT path FROM content_revisions WHERE user_id=? AND revision=? AND status='ready'`, a.userID, revision).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return loadedPack{}, ErrContentNotFound
	}
	if err != nil {
		return loadedPack{}, err
	}
	pack, err := loadPack(path)
	if err != nil {
		return loadedPack{}, fmt.Errorf("%w: %v", ErrContentInvalid, err)
	}
	return pack, nil
}

// draftPayloadFromRevision copies a published revision back into an editable draft.
func (a *App) draftPayloadFromRevision(revision, gameID string) (ContentDraftPayload, error) {
	pack, err := a.publishedPack(revision)
	if err != nil {
		return ContentDraftPayload{}, err
	}
	definition := pack.Definition
	payload := ContentDraftPayload{
		SchemaVersion: packSchemaV2, GameID: gameID, Mode: definition.Summary.Mode, Title: definition.Summary.Title,
		Description: definition.Summary.Description, Gameplay: definition.Summary.Gameplay, Background: definition.Background,
		Rules: definition.Rules, AuthorFacts: definition.Secret, Player: definition.Summary.Player, Opening: definition.Opening,
		Clock: definition.Clock, Locations: definition.Locations, Plot: definition.Plot, Bystanders: definition.BystanderRefs,
		NPCs: []string{}, Defaults: &definition.Settings,
	}
	for _, character := range definition.Characters {
		payload.NPCs = append(payload.NPCs, "npcs/"+character.DefinitionID+".json")
	}
	for _, location := range definition.Locations {
		if location.Name == definition.Scene {
			payload.InitialLocation = location.ID
		}
	}
	return payload, nil
}
