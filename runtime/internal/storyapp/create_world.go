package storyapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type CreateWorldRequest struct {
	GameID           string `json:"game_id"`
	ExpectedRevision string `json:"expected_revision"`
	RequestKey       string `json:"request_key"`
	Name             string `json:"name"`
	PlayerName       string `json:"player_name"`
	PlayerProfile    string `json:"player_profile"`
	PersonaID        string `json:"persona_id,omitempty"`
	Activate         bool   `json:"activate"`
}

func (a *App) CreateStoryWorld(ctx context.Context, request CreateWorldRequest) (WorldSummary, error) {
	if !packID.MatchString(request.GameID) || !packID.MatchString(request.ExpectedRevision) || strings.TrimSpace(request.RequestKey) == "" || len(request.RequestKey) > 200 {
		return WorldSummary{}, ErrInvalidRequest
	}
	a.copyMu.Lock()
	if a.closing {
		a.copyMu.Unlock()
		return WorldSummary{}, ErrWorldBusy
	}
	a.copyWG.Add(1)
	a.copyMu.Unlock()
	defer a.copyWG.Done()
	a.createMu.Lock()
	defer a.createMu.Unlock()
	body, _ := json.Marshal(request)
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	var priorID, priorHash string
	err := a.appDB.QueryRowContext(ctx, `SELECT world_id,request_hash FROM creation_operations WHERE user_id=? AND request_key=?`, a.userID, request.RequestKey).Scan(&priorID, &priorHash)
	if err == nil {
		if priorHash != hash {
			return WorldSummary{}, ErrIdempotencyConflict
		}
		return a.worldSummary(ctx, priorID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorldSummary{}, err
	}
	pack, ok := a.packs[request.GameID]
	if !ok {
		return WorldSummary{}, ErrWorldNotFound
	}
	if pack.Definition.Revision != request.ExpectedRevision {
		return WorldSummary{}, ErrVersionConflict
	}
	return a.createWorldFromPack(ctx, pack, request, hash)
}

// CreateWorld is a convenience for local callers; the public HTTP API requires
// the selected game and revision. The author-owned mode cannot be overridden.
func (a *App) CreateWorld(ctx context.Context, name, mode, playerName, playerProfile string, activate bool) (WorldSummary, error) {
	p, ok := a.packs[GameID]
	if !ok {
		return WorldSummary{}, ErrWorldNotFound
	}
	if mode != "" && mode != p.Definition.Summary.Mode {
		return WorldSummary{}, ErrInvalidRequest
	}
	return a.CreateStoryWorld(ctx, CreateWorldRequest{GameID: GameID, ExpectedRevision: p.Definition.Revision, RequestKey: newID("create"), Name: name, PlayerName: playerName, PlayerProfile: playerProfile, Activate: activate})
}

func (a *App) createWorldFromPack(ctx context.Context, pack loadedPack, request CreateWorldRequest, hash string) (WorldSummary, error) {
	def := pack.Definition
	name := cleanText(request.Name)
	if name == "" {
		name = def.Summary.Title + " · 新存档"
	}
	playerName, playerProfile := cleanText(request.PlayerName), cleanText(request.PlayerProfile)
	if playerName == "" {
		playerName = def.Summary.Player.Name
	}
	if playerName == "" {
		playerName = a.worldPlayerName
	}
	if playerProfile == "" {
		playerProfile = def.Summary.Player.Profile
	}
	// A selected template is copied into the world; later template edits never
	// reach a world that already exists.
	if personaName, personaProfile, err := a.personaDefaults(ctx, request.PersonaID, def); err != nil {
		return WorldSummary{}, err
	} else if personaName != "" {
		playerName, playerProfile = personaName, personaProfile
	}
	if len([]rune(name)) > 160 || len([]rune(playerName)) > 80 || len([]rune(playerProfile)) > 2000 {
		return WorldSummary{}, ErrInvalidRequest
	}
	if !def.Summary.Player.Editable && (playerName != def.Summary.Player.Name || playerProfile != def.Summary.Player.Profile) {
		return WorldSummary{}, ErrInvalidRequest
	}
	worldID := newID("world")
	path := a.worldPathFor(def.Summary.ID, worldID)
	store, err := openWorldDB(path)
	if err != nil {
		return WorldSummary{}, err
	}
	defer store.db.Close()
	if err = initializeWorld(ctx, store, a.userID, worldID, def, def.Summary.Mode, playerName, playerProfile); err != nil {
		return WorldSummary{}, err
	}
	if len(pack.Cover) > 0 {
		if err = os.WriteFile(filepath.Join(filepath.Dir(path), "cover"), pack.Cover, 0644); err != nil {
			return WorldSummary{}, err
		}
		if _, err = store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('cover_type',?)`, pack.CoverType); err != nil {
			return WorldSummary{}, err
		}
	}
	// A world keeps the images it started with, so a later edit, republish or
	// deletion of the template cannot change or remove them.
	worldDir := filepath.Dir(path)
	coverAsset, avatars, err := a.snapshotWorldAssets(pack, worldDir)
	if err != nil {
		return WorldSummary{}, err
	}
	if coverAsset != "" {
		if _, err = store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('cover_asset',?)`, coverAsset); err != nil {
			return WorldSummary{}, err
		}
	}
	for entityID, asset := range avatars {
		if _, err = store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)`, "avatar:"+entityID, asset); err != nil {
			return WorldSummary{}, err
		}
	}
	if _, err = store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('name',?),('updated_at',?)`, name, nowText()); err != nil {
		return WorldSummary{}, err
	}
	if err = store.db.Close(); err != nil {
		return WorldSummary{}, err
	}
	tx, err := a.appDB.BeginTx(ctx, nil)
	if err != nil {
		return WorldSummary{}, err
	}
	defer tx.Rollback()
	now := nowText()
	if _, err = tx.ExecContext(ctx, `INSERT INTO worlds(user_id,game_id,world_id,name,path,status,created_at,updated_at) VALUES(?,?,?,?,?,'ready',?,?)`, a.userID, def.Summary.ID, worldID, name, path, now, now); err != nil {
		return WorldSummary{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO creation_operations(user_id,request_key,request_hash,world_id) VALUES(?,?,?,?)`, a.userID, request.RequestKey, hash, worldID); err != nil {
		return WorldSummary{}, err
	}
	if err = tx.Commit(); err != nil {
		return WorldSummary{}, err
	}
	if request.Activate {
		a.activationMu.Lock()
		err = a.activate(ctx, worldID, 0)
		a.activationMu.Unlock()
		if err != nil {
			return WorldSummary{}, err
		}
	}
	return a.worldSummary(ctx, worldID)
}

func (a *App) worldGame(ctx context.Context, id string) (string, error) {
	var game string
	err := a.appDB.QueryRowContext(ctx, `SELECT game_id FROM worlds WHERE user_id=? AND world_id=?`, a.userID, id).Scan(&game)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrWorldNotFound
	}
	return game, err
}

func copyWorldCover(source, target string) error {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "cover"))
	if errors.Is(err, os.ErrNotExist) {
		store, e := openWorldDB(source)
		if e != nil {
			return e
		}
		defer store.db.Close()
		_, e = metaGet(context.Background(), store.db, "cover_type")
		if e == nil {
			return ErrStorageUnavailable
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(filepath.Dir(target), "cover"), data, 0644)
}

func (a *App) WorldGame(ctx context.Context, id string) (GameSummary, error) {
	s, err := a.ReadWorld(ctx, id, 1)
	if err != nil {
		return GameSummary{}, err
	}
	g := s.Definition.Summary
	if g.CoverURL != "" {
		g.CoverURL = "/api/v1/worlds/" + id + "/cover"
	}
	return g, nil
}

func (a *App) WorldCover(ctx context.Context, id string) ([]byte, string, error) {
	path, status, err := a.worldRecord(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if status != "ready" {
		return nil, "", ErrWorldNotReady
	}
	store, err := openWorldDB(path)
	if err != nil {
		return nil, "", err
	}
	defer store.db.Close()
	mime, err := metaGet(ctx, store.db, "cover_type")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrWorldNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if mime != "image/png" && mime != "image/jpeg" {
		return nil, "", ErrStorageUnavailable
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(path), "cover"))
	return body, mime, err
}
