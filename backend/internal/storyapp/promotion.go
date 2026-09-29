package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
	"strconv"
	"strings"
)

// A passers-by with a stable identity can be promoted to an important character.
// Promotion carries three separate ideas: what the person actually experienced, what
// the player already knows, and where the person currently is. Only the first becomes
// memory, only the second is shown in the ordinary preview, and the third comes from
// the world's current state rather than the definition's starting location.
type PromotionPreview struct {
	BystanderID   string            `json:"bystander_id"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Avatar        string            `json:"avatar,omitempty"`
	Location      string            `json:"location"`
	InScene       bool              `json:"in_scene"`
	Experience    int               `json:"experience_count"`
	PlayerVisible []PromotionSource `json:"player_visible_experiences"`
	Draft         PromotionDraft    `json:"draft"`
}

// PromotionSource is one authorized experience of the person, reduced to what the
// caller is allowed to read.
type PromotionSource struct {
	SourceID string `json:"source_id"`
	Kind     string `json:"kind"`
	Content  string `json:"content"`
	// PlayerVisible marks an experience the player already knows about.
	PlayerVisible bool `json:"player_visible"`
}

type PromotionDraft struct {
	Role             string   `json:"role"`
	Appearance       string   `json:"appearance"`
	Profile          string   `json:"profile"`
	Knowledge        string   `json:"knowledge"`
	InitialConcerns  string   `json:"initial_concerns"`
	SpeakingExamples []string `json:"speaking_examples,omitempty"`
}

type PromotionRequest struct {
	RequestKey           string         `json:"request_key"`
	ExpectedContextEpoch int64          `json:"expected_context_epoch"`
	BystanderID          string         `json:"bystander_id"`
	SourceIDs            []string       `json:"source_ids"`
	Draft                PromotionDraft `json:"draft"`
}

// PreviewCharacterPromotion reads the person's authorized experience. The ordinary
// preview only exposes what the player already knows plus a count; the author view
// adds the private experience records.
func (a *App) PreviewCharacterPromotion(ctx context.Context, worldID, bystanderID string, author bool) (PromotionPreview, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return PromotionPreview{}, err
	}
	if status != "ready" {
		return PromotionPreview{}, ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return PromotionPreview{}, err
	}
	defer store.Database().Close()
	snapshot, err := loadWorldSnapshot(ctx, store, 1)
	if err != nil {
		return PromotionPreview{}, err
	}
	bystander, ok := bystanderByID(snapshot.Definition.BystanderRefs, bystanderID)
	if !ok {
		return PromotionPreview{}, ErrContentNotFound
	}
	preview := PromotionPreview{
		BystanderID:   bystander.BystanderID,
		Name:          bystander.Name,
		Description:   bystander.Description,
		Avatar:        bystander.Avatar,
		Location:      bystanderLocation(snapshot, bystander),
		InScene:       bystanderInScene(snapshot, bystander),
		PlayerVisible: []PromotionSource{},
		Draft: PromotionDraft{
			Role:    "背景人物",
			Profile: bystander.Description,
		},
	}
	experiences, err := readBystanderExperiences(ctx, store.Database(), bystander.BystanderID)
	if err != nil {
		return PromotionPreview{}, err
	}
	if author {
		// The author view shows the records the person actually lived through, so the
		// author can decide what to carry into their definition.
		for _, record := range experiences {
			preview.PlayerVisible = append(preview.PlayerVisible, record)
		}
	}
	preview.Experience = len(experiences)
	return preview, nil
}

// PromoteCharacter turns one passers-by into an important character in a single world
// transaction: identity, chosen experience, scene membership and epoch move together.
func (a *App) PromoteCharacter(ctx context.Context, worldID string, request PromotionRequest) (wiaworld.Character, error) {
	request.BystanderID = wire.Clean(request.BystanderID)
	if wire.Clean(request.RequestKey) == "" || len(request.RequestKey) > 200 || request.ExpectedContextEpoch < 1 || request.BystanderID == "" {
		return wiaworld.Character{}, ErrInvalidRequest
	}
	draft, err := validatePromotionDraft(request.Draft)
	if err != nil {
		return wiaworld.Character{}, err
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return wiaworld.Character{}, err
	}
	if status != "ready" {
		return wiaworld.Character{}, ErrWorldNotReady
	}
	worldRT := a.worldRuntimeFor(worldID)
	worldRT.mu.Lock()
	defer worldRT.mu.Unlock()
	if worldRT.savePending {
		return wiaworld.Character{}, ErrWorldBusy
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return wiaworld.Character{}, err
	}
	defer store.Database().Close()
	if err := memoryReady(ctx, store.Database()); err != nil {
		return wiaworld.Character{}, err
	}
	if count, err := storage.CountActiveRuns(ctx, store.Database()); err != nil {
		return wiaworld.Character{}, err
	} else if count > 0 {
		return wiaworld.Character{}, ErrWorldBusy
	}
	snapshot, err := loadWorldSnapshot(ctx, store, 1)
	if err != nil {
		return wiaworld.Character{}, err
	}
	// A repeat request returns the character this world already created for that
	// passer-by. The comparison uses the normalized request, so blanks or spacing in the
	// source list do not turn a repeat into a conflict, while a changed draft still does.
	early := request
	early.SourceIDs = normalizedSourceIDs(request.SourceIDs)
	if existing, found, err := promotedCharacter(ctx, store, snapshot, request.BystanderID, request.RequestKey, promotionRequestHash(early, early.SourceIDs)); err != nil {
		return wiaworld.Character{}, err
	} else if found {
		return existing, nil
	}
	bystander, ok := bystanderByID(snapshot.Definition.BystanderRefs, request.BystanderID)
	if !ok {
		return wiaworld.Character{}, ErrContentNotFound
	}
	experiences, err := readBystanderExperiences(ctx, store.Database(), bystander.BystanderID)
	if err != nil {
		return wiaworld.Character{}, err
	}
	authorized := map[string]bool{}
	for _, record := range experiences {
		authorized[record.SourceID] = true
	}
	selected := normalizedSourceIDs(request.SourceIDs)
	for _, id := range selected {
		if !authorized[id] {
			return wiaworld.Character{}, fmt.Errorf("%w: %s is not an experience of this person", ErrContentInvalid, id)
		}
	}
	// The authoritative hash uses the validated selection, so a repeat whose source list
	// only differs by blanks or order still matches the recorded promotion.
	requestHash := promotionRequestHash(request, selected)
	tx, err := store.Database().BeginTx(ctx, nil)
	if err != nil {
		return wiaworld.Character{}, err
	}
	defer tx.Rollback()
	currentEpochText, err := storage.MetaGetTx(ctx, tx, "context_epoch")
	if err != nil {
		return wiaworld.Character{}, err
	}
	currentEpoch, err := strconv.ParseInt(currentEpochText, 10, 64)
	if err != nil {
		return wiaworld.Character{}, err
	}
	if currentEpoch != request.ExpectedContextEpoch {
		return wiaworld.Character{}, ErrVersionConflict
	}
	entityID, definitionID := promotionIdentity(bystander, snapshot.Definition)
	// A repeated request key with the same payload is idempotent; a different payload
	// is a conflict rather than a second promotion.
	rows, err := tx.QueryContext(ctx, `SELECT value FROM meta WHERE key LIKE 'promotion:%'`)
	if err != nil {
		return wiaworld.Character{}, err
	}
	records := []string{}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			rows.Close()
			return wiaworld.Character{}, err
		}
		records = append(records, value)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return wiaworld.Character{}, err
	}
	for _, value := range records {
		var record struct {
			RequestKey  string `json:"request_key"`
			RequestHash string `json:"request_hash"`
		}
		if json.Unmarshal([]byte(value), &record) == nil && record.RequestKey == request.RequestKey && record.RequestHash != requestHash {
			return wiaworld.Character{}, ErrIdempotencyConflict
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO characters(entity_id,definition_id,name,role,profile,knowledge,in_scene) VALUES(?,?,?,?,?,?,?)`,
		entityID, definitionID, bystander.Name, draft.Role, draft.Profile, draft.Knowledge, wire.BoolInt(bystanderInScene(snapshot, bystander))); err != nil {
		return wiaworld.Character{}, err
	}
	// The origin record states what the character came from and which of the person's
	// own experiences were carried over.
	for _, origin := range selected {
		if _, err = tx.ExecContext(ctx, `INSERT INTO character_origins(entity_id,source_kind,source_id,created_at) VALUES(?,?,?,?)`,
			entityID, "bystander_experience", origin, wire.NowText()); err != nil {
			return wiaworld.Character{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO character_origins(entity_id,source_kind,source_id,created_at) VALUES(?,?,?,?)`,
		entityID, "bystander", bystander.BystanderID, wire.NowText()); err != nil {
		return wiaworld.Character{}, err
	}
	for key, value := range map[string]string{
		"avatar:" + entityID:              bystander.Avatar,
		"initial_concerns:" + entityID:    draft.InitialConcerns,
		"definition_revision:" + entityID: definitionID,
		"promoted_from:" + entityID:       bystander.BystanderID,
	} {
		if err = storage.MetaSetTx(ctx, tx, key, value); err != nil {
			return wiaworld.Character{}, err
		}
	}
	if entityID != bystander.BystanderID && len(selected) > 0 {
		// The person keeps one identity: only the experiences the confirmation
		// selected follow the promoted entity.
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(selected)), ",")
		args := []any{entityID, bystander.BystanderID}
		for _, id := range selected {
			args = append(args, id)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE perceptions SET recipient_id=? WHERE recipient_id=? AND source_event_id IN (`+placeholders+`)`, args...); err != nil {
			return wiaworld.Character{}, err
		}
	}
	// The promoted person needs a committed scene view of their own: the world's
	// stored views are only valid when every character in the roster has one, so
	// deferring this to the next turn would leave the save unreadable. The view holds
	// what the player already knows plus the person's own authorized results, never
	// another character's private material.
	views := append([]SceneView{}, snapshot.SceneViews...)
	nextSceneVersion := snapshot.SceneVersion
	replaced := false
	for index := range views {
		if views[index].Recipient == entityID {
			views[index].Content = promotionSceneContent(bystander, draft, snapshot)
			views[index].SourceIDs = selected
			replaced = true
		}
	}
	if !replaced {
		nextSceneVersion++
		views = append(views, SceneView{Recipient: entityID, Content: promotionSceneContent(bystander, draft, snapshot), SourceIDs: selected, Version: nextSceneVersion})
	}
	if err = storage.MetaSetTx(ctx, tx, "scene_views", wire.MarshalJSON(views)); err != nil {
		return wiaworld.Character{}, err
	}
	if err = storage.MetaSetTx(ctx, tx, "scene_version", strconv.FormatInt(nextSceneVersion, 10)); err != nil {
		return wiaworld.Character{}, err
	}
	remarks := wire.MarshalJSON(map[string]any{"bystander_id": bystander.BystanderID, "sources": selected, "role": draft.Role, "request_key": request.RequestKey, "request_hash": requestHash})
	if _, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)`, "promotion:"+entityID, remarks); err != nil {
		return wiaworld.Character{}, err
	}
	// The person leaves the passer-by list; their position does not change.
	remaining := []PackBystander{}
	names := []string{}
	for _, item := range snapshot.Definition.BystanderRefs {
		if item.BystanderID == bystander.BystanderID {
			continue
		}
		remaining = append(remaining, item)
		names = append(names, item.Name)
	}
	if err = storage.MetaSetTx(ctx, tx, "bystander_refs", wire.MarshalJSON(remaining)); err != nil {
		return wiaworld.Character{}, err
	}
	if err = storage.MetaSetTx(ctx, tx, "bystanders", wire.MarshalJSON(names)); err != nil {
		return wiaworld.Character{}, err
	}
	if err = storage.MetaSetTx(ctx, tx, "context_epoch", strconv.FormatInt(currentEpoch+1, 10)); err != nil {
		return wiaworld.Character{}, err
	}
	if err = storage.MetaSetTx(ctx, tx, "updated_at", wire.NowText()); err != nil {
		return wiaworld.Character{}, err
	}
	// Derived optional material built on the previous roster is no longer a basis.
	if _, err = tx.ExecContext(ctx, `DELETE FROM meta WHERE key='suggestion_set'`); err != nil {
		return wiaworld.Character{}, err
	}
	if err = tx.Commit(); err != nil {
		return wiaworld.Character{}, err
	}
	return wiaworld.Character{EntityID: entityID, DefinitionID: definitionID, Name: bystander.Name, Role: draft.Role, Profile: draft.Profile, Knowledge: draft.Knowledge, InitialConcerns: draft.InitialConcerns, InScene: bystanderInScene(snapshot, bystander)}, nil
}

func validatePromotionDraft(draft PromotionDraft) (PromotionDraft, error) {
	draft.Role = wire.Clean(draft.Role)
	draft.Appearance = wire.Clean(draft.Appearance)
	draft.Profile = wire.Clean(draft.Profile)
	draft.Knowledge = wire.Clean(draft.Knowledge)
	draft.InitialConcerns = wire.Clean(draft.InitialConcerns)
	if draft.Role == "" {
		draft.Role = "背景人物"
	}
	if draft.Profile == "" {
		return PromotionDraft{}, fmt.Errorf("%w: a promoted character needs a profile", ErrContentInvalid)
	}
	if len([]rune(draft.Role)) > 200 || len([]rune(draft.Profile)) > 8000 || len([]rune(draft.Knowledge)) > 8000 || len([]rune(draft.InitialConcerns)) > 4000 {
		return PromotionDraft{}, fmt.Errorf("%w: draft field length", ErrContentInvalid)
	}
	examples := []string{}
	for _, line := range draft.SpeakingExamples {
		line = wire.Clean(line)
		if line == "" {
			continue
		}
		examples = append(examples, truncateRunes(line, 500))
	}
	if len(examples) > 12 {
		return PromotionDraft{}, fmt.Errorf("%w: too many speaking examples", ErrContentInvalid)
	}
	draft.SpeakingExamples = examples
	return draft, nil
}

// readBystanderExperiences lists the committed results attributed to one person.
// Only outcomes that named them grant experience; being present grants nothing. An
// experience is player-visible only when the player actually received that same
// result, never because it happens to be an action outcome.
func readBystanderExperiences(ctx context.Context, db *sql.DB, bystanderID string) ([]PromotionSource, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.source_event_id, p.source_type, p.content,
		EXISTS(SELECT 1 FROM perceptions v WHERE v.recipient_id='player' AND v.source_event_id=p.source_event_id) AS player_visible
		FROM perceptions p JOIN events e ON e.event_id=p.source_event_id WHERE p.recipient_id=? ORDER BY e.seq`, bystanderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PromotionSource{}
	for rows.Next() {
		var record PromotionSource
		var sourceType string
		var visible int
		if err = rows.Scan(&record.SourceID, &sourceType, &record.Content, &visible); err != nil {
			return nil, err
		}
		record.Kind = sourceType
		record.PlayerVisible = visible != 0
		out = append(out, record)
	}
	return out, rows.Err()
}

// promotedCharacter finds the character a previous promotion created for one
// passer-by. The same request key returns that character; a different key for the
// same person is a conflict instead of a second promotion.
func promotedCharacter(ctx context.Context, store *storage.WorldStore, snapshot worldSnapshot, bystanderID, requestKey, requestHash string) (wiaworld.Character, bool, error) {
	for _, character := range snapshot.Characters {
		raw, err := storage.MetaGet(ctx, store.Database(), "promotion:"+character.EntityID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return wiaworld.Character{}, false, err
		}
		var record struct {
			BystanderID string `json:"bystander_id"`
			RequestKey  string `json:"request_key"`
			RequestHash string `json:"request_hash"`
		}
		if json.Unmarshal([]byte(raw), &record) != nil || record.BystanderID != bystanderID {
			continue
		}
		if record.RequestKey != "" && (record.RequestKey != requestKey || (requestHash != "" && record.RequestHash != requestHash)) {
			return wiaworld.Character{}, false, ErrIdempotencyConflict
		}
		return character, true, nil
	}
	return wiaworld.Character{}, false, nil
}

// normalizedSourceIDs trims the chosen experiences and drops blanks, so the request hash
// describes the selection rather than the caller's formatting.
func normalizedSourceIDs(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if trimmed := wire.Clean(id); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// promotionRequestHash identifies one promotion request by its whole payload, so the
// same key with a different payload is a conflict rather than a silent repeat.
func promotionRequestHash(request PromotionRequest, selected []string) string {
	return hashJSON(map[string]any{
		"key": request.RequestKey, "bystander": request.BystanderID, "sources": selected, "draft": request.Draft,
	})
}

// promotionSceneContent seeds the promoted person's own view. It states where they
// are and what they themselves lived through, and never borrows another character's
// private material to fill the gap.
func promotionSceneContent(bystander PackBystander, draft PromotionDraft, snapshot worldSnapshot) string {
	parts := []string{}
	location := bystanderLocation(snapshot, bystander)
	if location != "" {
		parts = append(parts, fmt.Sprintf("%s 在%s。", bystander.Name, location))
	} else {
		parts = append(parts, fmt.Sprintf("%s 仍在原处。", bystander.Name))
	}
	if strings.TrimSpace(draft.InitialConcerns) != "" {
		parts = append(parts, "眼下在意："+strings.TrimSpace(draft.InitialConcerns))
	}
	parts = append(parts, "本人获准的经历以已提交结果为准；未参与的部分不作为已知。")
	return strings.Join(parts, "")
}

func readCharacterOrigins(ctx context.Context, db *sql.DB, entityID string) ([]PromotionSource, error) {
	rows, err := db.QueryContext(ctx, `SELECT source_kind, source_id FROM character_origins WHERE entity_id=? ORDER BY seq`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PromotionSource{}
	for rows.Next() {
		var record PromotionSource
		if err = rows.Scan(&record.Kind, &record.SourceID); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func bystanderByID(items []PackBystander, id string) (PackBystander, bool) {
	id = wire.Clean(id)
	for _, item := range items {
		if item.BystanderID == id {
			return item, true
		}
	}
	return PackBystander{}, false
}

// bystanderLocation reports where the person currently is. Promotion never falls
// back to the definition's starting location for someone already in a scene.
func bystanderLocation(snapshot worldSnapshot, bystander PackBystander) string {
	if bystanderInScene(snapshot, bystander) {
		return snapshot.Summary.Scene
	}
	return bystanderStartingLocation(snapshot, bystander)
}

// bystanderStartingLocation is the place the person was defined to be in.
func bystanderStartingLocation(snapshot worldSnapshot, bystander PackBystander) string {
	if initial := snapshot.Definition.InitialLocations[bystander.BystanderID]; initial != "" {
		return initial
	}
	return bystander.InitialLocation
}

func bystanderInScene(snapshot worldSnapshot, bystander PackBystander) bool {
	initial := bystanderStartingLocation(snapshot, bystander)
	if initial == "" {
		return true
	}
	current := snapshot.SceneLocation
	if current == "" {
		// A world written before locations were recorded falls back to comparing the
		// first location whose name starts the scene text, which still tolerates a
		// rewritten description.
		current = sceneIDFor(snapshot)
	}
	return initial == current
}

func sceneIDFor(snapshot worldSnapshot) string {
	if snapshot.SceneLocation != "" {
		return snapshot.SceneLocation
	}
	return locationIDFor(snapshot.Definition, snapshot.Summary.Scene)
}

// locationIDFor finds the location a scene text belongs to. A scene description may
// be rewritten with extra detail, so an exact match is not enough.
func locationIDFor(definition gameDefinition, scene string) string {
	scene = strings.TrimSpace(scene)
	if scene == "" {
		return ""
	}
	for _, location := range definition.Locations {
		if location.Name == scene {
			return location.ID
		}
	}
	for _, location := range definition.Locations {
		if location.Name != "" && strings.HasPrefix(scene, location.Name) {
			return location.ID
		}
	}
	return ""
}

// promotionIdentity gives the promoted character its own identity, derived from the
// passer-by id so the same person cannot be promoted twice under different names.
// A passer-by id may contain revision punctuation, so the result is reduced to the
// identifier shape the rest of the runtime accepts.
func promotionIdentity(bystander PackBystander, definition gameDefinition) (string, string) {
	source := wire.Clean(bystander.BystanderID)
	if source == "" {
		source = bystander.Name
	}
	var builder strings.Builder
	for _, r := range strings.ToLower(source) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			builder.WriteRune(r)
		case r > 127:
			builder.WriteString(fmt.Sprintf("%x", r))
		}
	}
	slug := strings.Trim(builder.String(), "_")
	if slug == "" {
		slug = "promoted"
	}
	if len(slug) > 60 {
		slug = slug[:60]
	}
	if slug[0] < 'a' || slug[0] > 'z' {
		slug = "b" + slug
	}
	entity := "npc:" + slug
	if !entityID.MatchString(entity) {
		slug = importDefinitionID(bystander.Name)
		entity = "npc:" + slug
	}
	return entity, slug
}
