package storyapp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gameagent/backend/internal/content"
	"gameagent/backend/internal/plot"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
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

// ContentDraftNPC is one editable important character. Publication writes it back
// into the package's npcs/<definition_id>.json file.
type ContentDraftNPC struct {
	DefinitionID     string   `json:"definition_id"`
	Revision         string   `json:"revision"`
	EntityID         string   `json:"entity_id"`
	Name             string   `json:"name"`
	Role             string   `json:"role"`
	Appearance       string   `json:"appearance,omitempty"`
	Profile          string   `json:"profile"`
	Knowledge        string   `json:"knowledge,omitempty"`
	InitialConcerns  string   `json:"initial_concerns,omitempty"`
	InitialLocation  string   `json:"initial_location"`
	Avatar           string   `json:"avatar,omitempty"`
	SpeakingExamples []string `json:"speaking_examples,omitempty"`
}

// ContentDraftPayload is the editable draft shape. Every field is optional so the
// editor can save partial work; publish validates the strict package schema.
type ContentDraftPayload struct {
	SchemaVersion   int                         `json:"schema_version"`
	GameID          string                      `json:"game_id"`
	Mode            string                      `json:"mode"`
	Title           string                      `json:"title"`
	Description     string                      `json:"description"`
	Gameplay        string                      `json:"gameplay"`
	Background      string                      `json:"background"`
	Rules           string                      `json:"rules"`
	AuthorFacts     string                      `json:"author_facts"`
	Cover           string                      `json:"cover,omitempty"`
	CoverAlt        string                      `json:"cover_alt,omitempty"`
	Player          content.PlayerDefaults      `json:"player"`
	Opening         string                      `json:"opening"`
	InitialLocation string                      `json:"initial_location"`
	Clock           string                      `json:"clock"`
	Locations       []content.PackLocation      `json:"locations"`
	NPCs            []ContentDraftNPC           `json:"npcs"`
	Bystanders      []content.PackBystander     `json:"bystanders"`
	Plot            *plot.Definition            `json:"plot,omitempty"`
	EventGeneration *plot.EventGenerationPolicy `json:"event_generation,omitempty"`
	Defaults        *wiaworld.NarrativeSettings `json:"defaults,omitempty"`
}

const payloadTooLarge = "payload is too large"

// ContentDraftPreview is what a draft would look like. The player view can only
// carry public projections; the author view is a separate explicit request and
// never the same payload with fields hidden in the browser.
type ContentDraftPreview struct {
	View              string                      `json:"view"`
	DraftID           string                      `json:"draft_id"`
	Version           int64                       `json:"version"`
	Title             string                      `json:"title"`
	Mode              string                      `json:"mode"`
	Description       string                      `json:"description"`
	Gameplay          string                      `json:"gameplay"`
	Background        string                      `json:"background"`
	Opening           string                      `json:"opening"`
	Clock             string                      `json:"clock"`
	InitialLocation   string                      `json:"initial_location"`
	Player            content.PlayerDefaults      `json:"player"`
	Characters        []ContentPreviewNPC         `json:"characters"`
	Bystanders        []content.PackBystander     `json:"bystanders"`
	Locations         []content.PackLocation      `json:"locations"`
	SpoilerWarning    string                      `json:"spoiler_warning,omitempty"`
	AuthorRules       string                      `json:"author_rules,omitempty"`
	AuthorFacts       string                      `json:"author_facts,omitempty"`
	AuthorCharacters  []ContentPreviewAuthorNPC   `json:"author_characters,omitempty"`
	AuthorPlot        *plot.Definition            `json:"author_plot,omitempty"`
	AuthorEventPolicy *plot.EventGenerationPolicy `json:"author_event_generation,omitempty"`
}

// ContentPreviewNPC is the public projection of an important character. Private
// knowledge, profile and concerns stay out of this shape entirely.
type ContentPreviewNPC struct {
	EntityID        string `json:"entity_id"`
	DefinitionID    string `json:"definition_id"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	Appearance      string `json:"appearance,omitempty"`
	Avatar          string `json:"avatar,omitempty"`
	InitialLocation string `json:"initial_location,omitempty"`
}

// ContentPreviewAuthorNPC is the author-only projection, requested explicitly.
type ContentPreviewAuthorNPC struct {
	ContentPreviewNPC
	Revision         string   `json:"revision"`
	Profile          string   `json:"profile"`
	Knowledge        string   `json:"knowledge"`
	InitialConcerns  string   `json:"initial_concerns"`
	SpeakingExamples []string `json:"speaking_examples,omitempty"`
}

func (a *App) PreviewContentDraft(ctx context.Context, draftID string, author bool) (ContentDraftPreview, error) {
	draft, err := a.ReadContentDraft(ctx, draftID)
	if err != nil {
		return ContentDraftPreview{}, err
	}
	payload := draft.Payload
	preview := ContentDraftPreview{
		View: "player", DraftID: draft.DraftID, Version: draft.Version, Title: payload.Title, Mode: payload.Mode,
		Description: payload.Description, Gameplay: payload.Gameplay, Background: payload.Background,
		Opening: payload.Opening, Clock: payload.Clock, InitialLocation: payload.InitialLocation, Player: payload.Player,
		Locations: payload.Locations, Bystanders: payload.Bystanders, Characters: []ContentPreviewNPC{},
	}
	for _, npc := range payload.NPCs {
		preview.Characters = append(preview.Characters, ContentPreviewNPC{
			EntityID: npc.EntityID, DefinitionID: npc.DefinitionID, Name: npc.Name, Role: npc.Role,
			Appearance: npc.Appearance, Avatar: npc.Avatar, InitialLocation: npc.InitialLocation,
		})
	}
	if !author {
		return preview, nil
	}
	preview.View = "author"
	preview.SpoilerWarning = "作者视图包含剧透与人物私密设定，仅用于创作检查。"
	preview.AuthorRules = payload.Rules
	preview.AuthorFacts = payload.AuthorFacts
	preview.AuthorPlot = payload.Plot
	preview.AuthorEventPolicy = payload.EventGeneration
	preview.AuthorCharacters = []ContentPreviewAuthorNPC{}
	for _, npc := range payload.NPCs {
		preview.AuthorCharacters = append(preview.AuthorCharacters, ContentPreviewAuthorNPC{
			ContentPreviewNPC: ContentPreviewNPC{
				EntityID: npc.EntityID, DefinitionID: npc.DefinitionID, Name: npc.Name, Role: npc.Role,
				Appearance: npc.Appearance, Avatar: npc.Avatar, InitialLocation: npc.InitialLocation,
			},
			Revision: npc.Revision, Profile: npc.Profile, Knowledge: npc.Knowledge,
			InitialConcerns: npc.InitialConcerns, SpeakingExamples: npc.SpeakingExamples,
		})
	}
	return preview, nil
}

// CreateContentProject reserves a stable game identity for one author project. Official
// packages and other projects of the same account cannot be taken over. An author who
// only supplies a title gets an identifier derived from it, fixed at creation.
func (a *App) CreateContentProject(ctx context.Context, gameID, title string) (ContentProject, error) {
	gameID, title = wire.Clean(gameID), wire.Clean(title)
	if title == "" || len([]rune(title)) > 120 {
		return ContentProject{}, ErrInvalidRequest
	}
	if gameID == "" {
		derived, err := a.availableGameID(ctx, title)
		if err != nil {
			return ContentProject{}, err
		}
		gameID = derived
	}
	if !packID.MatchString(gameID) {
		return ContentProject{}, ErrInvalidRequest
	}
	if _, official := a.pack(gameID); official {
		return ContentProject{}, ErrContentInvalid
	}
	var existing int
	if err := a.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_projects WHERE user_id=? AND game_id=?`, a.userID, gameID).Scan(&existing); err != nil {
		return ContentProject{}, err
	}
	if existing > 0 {
		return ContentProject{}, ErrContentBusy
	}
	now := wire.NowText()
	project := ContentProject{ProjectID: wire.NewID("project"), GameID: gameID, Title: title, Version: 1, CreatedAt: now, UpdatedAt: now}
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
	baseRevision = wire.Clean(baseRevision)
	payload := ContentDraftPayload{
		SchemaVersion: content.SchemaV2, GameID: project.GameID, Mode: "open", Title: project.Title, InitialLocation: "",
		NPCs: []ContentDraftNPC{}, Locations: []content.PackLocation{}, Bystanders: []content.PackBystander{},
		// New content starts with a lead the player may adjust; the editor can lock it.
		Player: content.PlayerDefaults{Editable: true},
	}
	status := draftStatusEditing
	draftID := wire.NewID("draft")
	var inherited map[string][]byte
	if baseRevision != "" {
		copied, err := a.draftPayloadFromRevision(baseRevision, project.GameID)
		if err != nil {
			return ContentDraft{}, err
		}
		payload = copied
		if pack, packErr := a.publishedPack(baseRevision); packErr == nil {
			inherited = pack.Assets
		}
	}
	if err = validateDraftPayload(payload); err != nil {
		return ContentDraft{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ContentDraft{}, err
	}
	draft := ContentDraft{ContentDraftSummary: ContentDraftSummary{DraftID: draftID, ProjectID: project.ProjectID, BaseRevision: baseRevision, Version: 1, Status: status, UpdatedAt: wire.NowText()}, Payload: payload}
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_drafts(user_id,draft_id,project_id,base_revision,version,status,payload_json,source_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'',?,?)`,
		a.userID, draft.DraftID, draft.ProjectID, draft.BaseRevision, draft.Version, draft.Status, string(body), draft.UpdatedAt, draft.UpdatedAt); err != nil {
		return ContentDraft{}, err
	}
	// The new draft owns its own copies of the images its source used, so editing and
	// publishing it does not depend on files belonging to another revision.
	if err = a.copyPackAssets(ctx, draft.DraftID, inherited); err != nil {
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
	// The author names things; the program owns the internal identifiers.
	if err := normalizeDraftIdentities(&payload); err != nil {
		return ContentDraft{}, err
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
		string(body), wire.NowText(), a.userID, strings.TrimSpace(draftID), expectedVersion, draftStatusEditing, draftStatusPreview)
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
		if _, err := wiaworld.ValidateNarrativeSettings(*payload.Defaults); err != nil {
			return fmt.Errorf("%w: defaults", ErrContentInvalid)
		}
	}
	entityIDs := map[string]bool{}
	for index, npc := range payload.NPCs {
		if !packID.MatchString(npc.DefinitionID) || !packID.MatchString(npc.Revision) || !entityID.MatchString(npc.EntityID) {
			return fmt.Errorf("%w: npc %d identity", ErrContentInvalid, index)
		}
		if entityIDs[npc.EntityID] {
			return fmt.Errorf("%w: npc %d is duplicated", ErrContentInvalid, index)
		}
		entityIDs[npc.EntityID] = true
		if npc.InitialLocation != "" && !seen[npc.InitialLocation] {
			return fmt.Errorf("%w: npc %d location", ErrContentInvalid, index)
		}
		if npc.Avatar != "" && !strings.HasPrefix(npc.Avatar, "assets/") {
			return fmt.Errorf("%w: npc %d avatar", ErrContentInvalid, index)
		}
		if len(npc.SpeakingExamples) > 12 {
			return fmt.Errorf("%w: npc %d speaking examples", ErrContentInvalid, index)
		}
	}
	return nil
}

// normalizeDraftIdentities fills in the internal identifiers the package format needs,
// so an author names a place or a person and the program derives the rest. Anything the
// author or a source package already set is kept, which is what makes published content
// keep its identity across edits. A nameless entry is refused here rather than at
// publication, because it is the one thing the author must supply.
func normalizeDraftIdentities(payload *ContentDraftPayload) error {
	if payload == nil {
		return nil
	}
	usedLocations := map[string]bool{}
	for index := range payload.Locations {
		location := &payload.Locations[index]
		location.Name = wire.Clean(location.Name)
		if location.Name == "" {
			return fmt.Errorf("%w: location %d has no name", ErrContentInvalid, index+1)
		}
		if location.ID == "" || !packID.MatchString(location.ID) {
			location.ID = generatedIdentifier(location.Name, "place", usedLocations, len(payload.Locations), index)
		}
		usedLocations[location.ID] = true
	}
	seenLocations := map[string]bool{}
	for _, location := range payload.Locations {
		seenLocations[location.ID] = true
	}
	usedEntities := map[string]bool{}
	for index := range payload.NPCs {
		npc := &payload.NPCs[index]
		npc.Name, npc.Role = wire.Clean(npc.Name), wire.Clean(npc.Role)
		if npc.Name == "" {
			return fmt.Errorf("%w: character %d has no name", ErrContentInvalid, index+1)
		}
		if npc.DefinitionID == "" || !packID.MatchString(npc.DefinitionID) {
			npc.DefinitionID = generatedIdentifier(npc.Name, "npc", map[string]bool{}, len(payload.NPCs), index)
		}
		if npc.Revision == "" || !packID.MatchString(npc.Revision) {
			npc.Revision = "v1"
		}
		if npc.EntityID == "" || !entityID.MatchString(npc.EntityID) {
			npc.EntityID = generatedEntityID(npc.Name, npc.DefinitionID, usedEntities)
		}
		usedEntities[npc.EntityID] = true
		if npc.InitialLocation != "" && !seenLocations[npc.InitialLocation] {
			npc.InitialLocation = ""
		}
	}
	for index := range payload.Bystanders {
		bystander := &payload.Bystanders[index]
		bystander.Name, bystander.Description = wire.Clean(bystander.Name), wire.Clean(bystander.Description)
		if bystander.Name == "" {
			return fmt.Errorf("%w: passer-by %d has no name", ErrContentInvalid, index+1)
		}
		if bystander.BystanderID == "" || !bystanderIDPattern.MatchString(bystander.BystanderID) {
			bystander.BystanderID = generatedBystanderID(bystander.Name, index, usedEntities)
		}
		usedEntities[bystander.BystanderID] = true
		if bystander.InitialLocation != "" && !seenLocations[bystander.InitialLocation] {
			bystander.InitialLocation = ""
		}
	}
	if payload.InitialLocation != "" && !seenLocations[payload.InitialLocation] {
		payload.InitialLocation = ""
	}
	if payload.InitialLocation == "" && len(payload.Locations) > 0 {
		payload.InitialLocation = payload.Locations[0].ID
	}
	return nil
}

// identifierSlug reduces a human name to the identifier alphabet.
func identifierSlug(name string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r > 127:
			builder.WriteString(fmt.Sprintf("%x", r))
		}
	}
	slug := strings.Trim(builder.String(), "_")
	if slug == "" {
		return ""
	}
	if len(slug) > 48 {
		slug = slug[:48]
	}
	if slug[0] < 'a' || slug[0] > 'z' {
		slug = "x" + slug
	}
	return slug
}

func generatedIdentifier(name, fallback string, used map[string]bool, total, index int) string {
	base := identifierSlug(name)
	if base == "" {
		base = fmt.Sprintf("%s%d", fallback, index+1)
	}
	candidate := base
	for suffix := 2; used[candidate]; suffix++ {
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
	if !packID.MatchString(candidate) {
		return fmt.Sprintf("%s%d", fallback, total+index+1)
	}
	return candidate
}

func generatedEntityID(name, definitionID string, used map[string]bool) string {
	base := identifierSlug(name)
	if base == "" {
		base = definitionID
	}
	candidate := "npc:" + base
	for suffix := 2; used[candidate] || !entityID.MatchString(candidate); suffix++ {
		candidate = fmt.Sprintf("npc:%s-%d", base, suffix)
	}
	return candidate
}

func generatedBystanderID(name string, index int, used map[string]bool) string {
	base := identifierSlug(name)
	if base == "" {
		base = fmt.Sprintf("person%d", index+1)
	}
	candidate := "bystander:" + base
	for suffix := 2; used[candidate]; suffix++ {
		candidate = fmt.Sprintf("bystander:%s-%d", base, suffix)
	}
	return candidate
}

// availableGameID derives an unused story identity from a title.
func (a *App) availableGameID(ctx context.Context, title string) (string, error) {
	base := identifierSlug(title)
	if base == "" {
		base = "story"
	}
	if len(base) > 40 {
		base = base[:40]
	}
	candidate := base
	for suffix := 2; ; suffix++ {
		if !packID.MatchString(candidate) {
			candidate = fmt.Sprintf("story-%d", suffix)
		}
		if _, official := a.pack(candidate); official {
			candidate = fmt.Sprintf("%s-%d", base, suffix)
			continue
		}
		var taken int
		if err := a.appDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_projects WHERE user_id=? AND game_id=?`, a.userID, candidate).Scan(&taken); err != nil {
			return "", err
		}
		if taken == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
		if suffix > 200 {
			return "", ErrContentBusy
		}
	}
}

// publishedPack resolves one immutable revision by identity. Official packages are
// already loaded; user revisions resolve through the registered content path.
func (a *App) publishedPack(revision string) (loadedPack, error) {
	revision = wire.Clean(revision)
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
	return a.draftPayloadFromLoadedPack(pack, gameID)
}

// draftPayloadFromLoadedPack projects a loaded package into the editable draft shape.
// It reads the package's own files instead of guessing paths, and carries the fields
// the editor can change, so a round trip through a draft is lossless for the content
// the product supports.
func (a *App) draftPayloadFromLoadedPack(pack loadedPack, gameID string) (ContentDraftPayload, error) {
	definition := pack.Definition
	// The draft is the pack's own view, read from the pack's JSON rather than
	// re-serialised from the running definition, so editing a package cannot quietly
	// drop a field the runtime does not use.
	story := pack.Story
	payload := ContentDraftPayload{
		SchemaVersion: content.SchemaV2, GameID: gameID, Mode: story.Mode, Title: story.Title,
		Description: story.Description, Gameplay: story.Gameplay, Background: story.Background,
		Rules: story.Rules, AuthorFacts: story.AuthorFacts, Player: story.Player, Opening: story.Opening,
		Cover: story.Cover, CoverAlt: story.CoverAlt, EventGeneration: story.EventGeneration,
		InitialLocation: story.InitialLocation,
		Clock:           story.Clock, Locations: story.Locations, Plot: story.Plot, Bystanders: story.Bystanders,
		NPCs: []ContentDraftNPC{}, Defaults: story.Defaults,
	}
	// Each character's own file travels with the package under its recorded path, so
	// the editor gets the real avatar and examples whatever the file is called.
	byDefinition := map[string]PackNPC{}
	for name, body := range pack.NPCFiles {
		var file PackNPC
		if json.Unmarshal(body, &file) != nil {
			continue
		}
		key := file.DefinitionID
		if key == "" {
			key = strings.TrimSuffix(strings.TrimPrefix(name, "npcs/"), ".json")
		}
		byDefinition[key] = file
	}
	for _, character := range definition.Characters {
		npc := ContentDraftNPC{
			DefinitionID: character.DefinitionID, Revision: character.DefinitionRevision, EntityID: character.EntityID,
			Name: character.Name, Role: character.Role, Appearance: character.Appearance, Profile: character.Profile,
			Knowledge: character.Knowledge, InitialConcerns: character.InitialConcerns,
			InitialLocation: definition.InitialLocations[character.EntityID],
		}
		if file, ok := byDefinition[character.DefinitionID]; ok {
			npc.Avatar, npc.SpeakingExamples = file.Avatar, file.SpeakingExamples
			if file.InitialLocation != "" {
				npc.InitialLocation = file.InitialLocation
			}
		}
		payload.NPCs = append(payload.NPCs, npc)
	}
	return payload, nil
}

// copyPackAssets gives a draft its own copy of the images a source package uses, so
// publishing the draft does not depend on files it does not own.
func (a *App) copyPackAssets(ctx context.Context, draftID string, assets map[string][]byte) error {
	if len(assets) == 0 {
		return nil
	}
	dir := filepath.Join(a.contentRoot(), "drafts", draftID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := assets[name]
		config, format, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > assetMaxPixels || config.Height > assetMaxPixels {
			// Only images the product can serve are carried over; a package referencing
			// anything else already failed its own validation.
			continue
		}
		mediaType := "image/png"
		if format == "jpeg" {
			mediaType = "image/jpeg"
		}
		asset := ContentDraftAsset{
			AssetID: wire.NewID("asset"), RelativeName: name, MediaType: mediaType,
			ByteSize: int64(len(body)), Width: config.Width, Height: config.Height,
		}
		staged := filepath.Join(dir, asset.AssetID)
		if err = os.WriteFile(staged, body, 0o644); err != nil {
			return err
		}
		if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_draft_assets(user_id,draft_id,asset_id,relative_name,media_type,byte_size,width,height,digest,staged_path,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,draft_id,asset_id) DO UPDATE SET relative_name=excluded.relative_name,digest=excluded.digest,staged_path=excluded.staged_path`,
			a.userID, draftID, asset.AssetID, asset.RelativeName, asset.MediaType, asset.ByteSize, asset.Width, asset.Height, assetDigest(body), staged, wire.NowText()); err != nil {
			_ = os.Remove(staged)
			return err
		}
	}
	return nil
}

// draftNPCFiles renders the draft's characters back into package files. Publication
// uses this mapping so the editor and the package agree on one shape.
func draftNPCFiles(payload ContentDraftPayload) (map[string]PackNPC, error) {
	files := map[string]PackNPC{}
	for index, npc := range payload.NPCs {
		if !packID.MatchString(npc.DefinitionID) || !entityID.MatchString(npc.EntityID) || !packID.MatchString(npc.Revision) {
			return nil, fmt.Errorf("%w: npc identity", ErrContentInvalid)
		}
		if strings.TrimSpace(npc.Name) == "" || strings.TrimSpace(npc.Role) == "" || strings.TrimSpace(npc.Profile) == "" {
			return nil, fmt.Errorf("%w: npc %d is incomplete", ErrContentInvalid, index)
		}
		name := "npcs/" + npc.DefinitionID + ".json"
		if _, exists := files[name]; exists {
			return nil, fmt.Errorf("%w: duplicate npc", ErrContentInvalid)
		}
		files[name] = PackNPC{
			DefinitionID: npc.DefinitionID, Revision: npc.Revision, EntityID: npc.EntityID, Name: npc.Name,
			Role: npc.Role, Appearance: npc.Appearance, Profile: npc.Profile, Knowledge: npc.Knowledge,
			InitialConcerns: npc.InitialConcerns, InitialLocation: npc.InitialLocation,
			Avatar: npc.Avatar, SpeakingExamples: npc.SpeakingExamples,
		}
	}
	return files, nil
}
