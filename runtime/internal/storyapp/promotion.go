package storyapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	store, err := openWorldDB(path)
	if err != nil {
		return PromotionPreview{}, err
	}
	defer store.db.Close()
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
	experiences, err := readBystanderExperiences(ctx, store.db, bystander.BystanderID)
	if err != nil {
		return PromotionPreview{}, err
	}
	preview.Experience = len(experiences)
	for _, record := range experiences {
		if author {
			preview.PlayerVisible = append(preview.PlayerVisible, record)
			continue
		}
		// The player only ever sees what they were told; the count still shows that
		// the person lived through more than the player witnessed.
		if record.PlayerVisible {
			preview.PlayerVisible = append(preview.PlayerVisible, record)
		}
	}
	return preview, nil
}

// PromoteCharacter turns one passers-by into an important character in a single world
// transaction: identity, chosen experience, scene membership and epoch move together.
func (a *App) PromoteCharacter(ctx context.Context, worldID string, request PromotionRequest) (Character, error) {
	request.BystanderID = cleanText(request.BystanderID)
	if cleanText(request.RequestKey) == "" || len(request.RequestKey) > 200 || request.ExpectedContextEpoch < 1 || request.BystanderID == "" {
		return Character{}, ErrInvalidRequest
	}
	draft, err := validatePromotionDraft(request.Draft)
	if err != nil {
		return Character{}, err
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return Character{}, err
	}
	if status != "ready" {
		return Character{}, ErrWorldNotReady
	}
	world := a.worldRuntimeFor(worldID)
	world.mu.Lock()
	defer world.mu.Unlock()
	if world.savePending {
		return Character{}, ErrWorldBusy
	}
	store, err := openWorldDB(path)
	if err != nil {
		return Character{}, err
	}
	defer store.db.Close()
	if err := memoryReady(ctx, store.db); err != nil {
		return Character{}, err
	}
	if count, err := countActiveRuns(ctx, store.db); err != nil {
		return Character{}, err
	} else if count > 0 {
		return Character{}, ErrWorldBusy
	}
	snapshot, err := loadWorldSnapshot(ctx, store, 1)
	if err != nil {
		return Character{}, err
	}
	// A repeat request returns the character this world already created for that
	// passer-by; a different request key for the same person is a conflict.
	if existing, found, err := promotedCharacter(ctx, store, snapshot, request.BystanderID, request.RequestKey); err != nil {
		return Character{}, err
	} else if found {
		return existing, nil
	}
	bystander, ok := bystanderByID(snapshot.Definition.BystanderRefs, request.BystanderID)
	if !ok {
		return Character{}, ErrContentNotFound
	}
	experiences, err := readBystanderExperiences(ctx, store.db, bystander.BystanderID)
	if err != nil {
		return Character{}, err
	}
	authorized := map[string]bool{}
	for _, record := range experiences {
		authorized[record.SourceID] = true
	}
	selected := []string{}
	for _, id := range request.SourceIDs {
		id = cleanText(id)
		if id == "" {
			continue
		}
		if !authorized[id] {
			return Character{}, fmt.Errorf("%w: %s is not an experience of this person", ErrContentInvalid, id)
		}
		selected = append(selected, id)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Character{}, err
	}
	defer tx.Rollback()
	currentEpochText, err := metaGetTx(ctx, tx, "context_epoch")
	if err != nil {
		return Character{}, err
	}
	currentEpoch, err := strconv.ParseInt(currentEpochText, 10, 64)
	if err != nil {
		return Character{}, err
	}
	if currentEpoch != request.ExpectedContextEpoch {
		return Character{}, ErrVersionConflict
	}
	entityID, definitionID := promotionIdentity(bystander, snapshot.Definition)
	requestHash := hashJSON(map[string]any{
		"key": request.RequestKey, "bystander": request.BystanderID, "sources": selected,
		"draft": draft, "epoch": request.ExpectedContextEpoch,
	})
	// A repeated request key with the same payload is idempotent; a different payload
	// is a conflict rather than a second promotion.
	rows, err := tx.QueryContext(ctx, `SELECT value FROM meta WHERE key LIKE 'promotion:%'`)
	if err != nil {
		return Character{}, err
	}
	records := []string{}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			rows.Close()
			return Character{}, err
		}
		records = append(records, value)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Character{}, err
	}
	for _, value := range records {
		var record struct {
			RequestKey  string `json:"request_key"`
			RequestHash string `json:"request_hash"`
		}
		if json.Unmarshal([]byte(value), &record) == nil && record.RequestKey == request.RequestKey && record.RequestHash != requestHash {
			return Character{}, ErrIdempotencyConflict
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO characters(entity_id,definition_id,name,role,profile,knowledge,in_scene) VALUES(?,?,?,?,?,?,?)`,
		entityID, definitionID, bystander.Name, draft.Role, draft.Profile, draft.Knowledge, boolInt(bystanderInScene(snapshot, bystander))); err != nil {
		return Character{}, err
	}
	// The origin record states what the character came from and which of the person's
	// own experiences were carried over.
	for _, origin := range selected {
		if _, err = tx.ExecContext(ctx, `INSERT INTO character_origins(entity_id,source_kind,source_id,created_at) VALUES(?,?,?,?)`,
			entityID, "bystander_experience", origin, nowText()); err != nil {
			return Character{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO character_origins(entity_id,source_kind,source_id,created_at) VALUES(?,?,?,?)`,
		entityID, "bystander", bystander.BystanderID, nowText()); err != nil {
		return Character{}, err
	}
	for key, value := range map[string]string{
		"avatar:" + entityID:              bystander.Avatar,
		"initial_concerns:" + entityID:    draft.InitialConcerns,
		"definition_revision:" + entityID: definitionID,
		"promoted_from:" + entityID:       bystander.BystanderID,
	} {
		if err = metaSetTx(ctx, tx, key, value); err != nil {
			return Character{}, err
		}
	}
	if entityID != bystander.BystanderID {
		// The person keeps one identity: earlier authorized experience follows the
		// promoted entity instead of being copied into unrelated records.
		if _, err = tx.ExecContext(ctx, `UPDATE perceptions SET recipient_id=? WHERE recipient_id=?`, entityID, bystander.BystanderID); err != nil {
			return Character{}, err
		}
	}
	remarks := marshalJSON(map[string]any{"bystander_id": bystander.BystanderID, "sources": selected, "role": draft.Role, "request_key": request.RequestKey, "request_hash": requestHash})
	if _, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)`, "promotion:"+entityID, remarks); err != nil {
		return Character{}, err
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
	if err = metaSetTx(ctx, tx, "bystander_refs", marshalJSON(remaining)); err != nil {
		return Character{}, err
	}
	if err = metaSetTx(ctx, tx, "bystanders", marshalJSON(names)); err != nil {
		return Character{}, err
	}
	if err = metaSetTx(ctx, tx, "context_epoch", strconv.FormatInt(currentEpoch+1, 10)); err != nil {
		return Character{}, err
	}
	if err = metaSetTx(ctx, tx, "updated_at", nowText()); err != nil {
		return Character{}, err
	}
	// Derived optional material built on the previous roster is no longer a basis.
	if _, err = tx.ExecContext(ctx, `DELETE FROM meta WHERE key='suggestion_set'`); err != nil {
		return Character{}, err
	}
	if err = tx.Commit(); err != nil {
		return Character{}, err
	}
	return Character{EntityID: entityID, DefinitionID: definitionID, Name: bystander.Name, Role: draft.Role, Profile: draft.Profile, Knowledge: draft.Knowledge, InitialConcerns: draft.InitialConcerns, InScene: bystanderInScene(snapshot, bystander)}, nil
}

func validatePromotionDraft(draft PromotionDraft) (PromotionDraft, error) {
	draft.Role = cleanText(draft.Role)
	draft.Appearance = cleanText(draft.Appearance)
	draft.Profile = cleanText(draft.Profile)
	draft.Knowledge = cleanText(draft.Knowledge)
	draft.InitialConcerns = cleanText(draft.InitialConcerns)
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
		line = cleanText(line)
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
// Only outcomes that named them grant experience; being present grants nothing.
func readBystanderExperiences(ctx context.Context, db *sql.DB, bystanderID string) ([]PromotionSource, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.source_event_id, p.source_type, p.content FROM perceptions p JOIN events e ON e.event_id=p.source_event_id WHERE p.recipient_id=? ORDER BY e.seq`, bystanderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PromotionSource{}
	for rows.Next() {
		var record PromotionSource
		var sourceType string
		if err = rows.Scan(&record.SourceID, &sourceType, &record.Content); err != nil {
			return nil, err
		}
		record.Kind = sourceType
		record.PlayerVisible = strings.HasPrefix(sourceType, "action_")
		out = append(out, record)
	}
	return out, rows.Err()
}

// promotedCharacter finds the character a previous promotion created for one
// passer-by. The same request key returns that character; a different key for the
// same person is a conflict instead of a second promotion.
func promotedCharacter(ctx context.Context, store *worldStore, snapshot worldSnapshot, bystanderID, requestKey string) (Character, bool, error) {
	for _, character := range snapshot.Characters {
		raw, err := metaGet(ctx, store.db, "promotion:"+character.EntityID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Character{}, false, err
		}
		var record struct {
			BystanderID string `json:"bystander_id"`
			RequestKey  string `json:"request_key"`
		}
		if json.Unmarshal([]byte(raw), &record) != nil || record.BystanderID != bystanderID {
			continue
		}
		if record.RequestKey != "" && record.RequestKey != requestKey {
			return Character{}, false, ErrIdempotencyConflict
		}
		return character, true, nil
	}
	return Character{}, false, nil
}

// readCharacterOrigins lists where a character in this world came from.
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
	id = cleanText(id)
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
	return snapshot.Definition.InitialLocations[bystander.BystanderID]
}

func bystanderInScene(snapshot worldSnapshot, bystander PackBystander) bool {
	initial := snapshot.Definition.InitialLocations[bystander.BystanderID]
	if initial == "" {
		// Passers-by defined with their own location are present where they live.
		initial = bystander.InitialLocation
	}
	return initial == "" || initial == sceneIDFor(snapshot)
}

func sceneIDFor(snapshot worldSnapshot) string {
	for _, location := range snapshot.Definition.Locations {
		if location.Name == snapshot.Summary.Scene {
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
	source := cleanText(bystander.BystanderID)
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
