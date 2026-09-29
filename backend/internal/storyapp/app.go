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
	"strings"
	"time"

	"gameagent/backend/internal/atomicfile"
	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
	"gameagent/backend/internal/secret"
	"gameagent/backend/internal/storage"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

var writeModelConfig = atomicfile.Write

func Open(ctx context.Context, options Options) (*App, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root := strings.TrimSpace(options.DataRoot)
	if root == "" {
		return nil, fmt.Errorf("storyapp: data root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	appRoot := filepath.Join(root, "story-app")
	userID := strings.TrimSpace(options.UserID)
	if userID == "" {
		userID = LocalUserID
	}
	if err := os.MkdirAll(filepath.Join(appRoot, "worlds", userID), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(appRoot, "config"), 0o755); err != nil {
		return nil, err
	}
	processLock, err := acquireProcessLock(filepath.Join(appRoot, "app.lock"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAppBusy, err)
	}
	// The application schema is assembled from the features that own these tables,
	// so it is passed in rather than known by the storage package.
	db, err := storage.OpenAppDB(filepath.Join(appRoot, "app.db"), appSchema+usageSchema+contentSchema)
	if err != nil {
		_ = processLock.Release()
		return nil, err
	}
	copyCtx, copyCancel := context.WithCancel(context.Background())
	app := &App{root: appRoot, dataRoot: root, appDB: db, processLock: processLock, userID: userID, worlds: make(map[string]*worldRuntime), runs: make(map[string]*runRuntime), copyCtx: copyCtx, copyCancel: copyCancel, logger: options.Logger, closed: make(chan struct{}), worldPlayerName: wire.Clean(options.WorldPlayerName)}
	if options.ModelConfigPath != "" {
		app.modelPath = options.ModelConfigPath
	} else if value := strings.TrimSpace(os.Getenv("WIA_MODEL_CONFIG")); value != "" {
		app.modelPath = value
	} else {
		app.modelPath = filepath.Join(appRoot, "config", "model.json")
	}
	if options.Generator != nil {
		app.generator = options.Generator
		app.modelInfo = ModelInfo{Provider: "test", Model: "injected", Configured: true, Source: "injected"}
	} else {
		app.loadModelConfig(options.AllowFake)
	}
	if err := app.loadPacks(ctx, options.StoryPacksPath); err != nil {
		app.Close()
		return nil, err
	}
	if err := app.recoverContentOperations(ctx); err != nil {
		app.Close()
		return nil, err
	}
	if err := app.loadPublishedRevisions(ctx); err != nil {
		app.Close()
		return nil, err
	}
	if err := app.markInterrupted(ctx); err != nil {
		db.Close()
		_ = processLock.Release()
		return nil, err
	}
	if err := app.markInterruptedCopies(ctx); err != nil {
		db.Close()
		_ = processLock.Release()
		return nil, err
	}
	if err := app.resumeMemoryJobs(ctx); err != nil {
		app.Close()
		return nil, err
	}
	return app, nil
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	select {
	case <-a.closed:
	default:
		close(a.closed)
	}
	a.copyMu.Lock()
	a.closing = true
	if a.copyCancel != nil {
		a.copyCancel()
	}
	a.copyMu.Unlock()
	a.runsMu.Lock()
	active := make([]*runRuntime, 0, len(a.runs))
	for _, r := range a.runs {
		r.Cancel()
		active = append(active, r)
	}
	a.runsMu.Unlock()
	for _, r := range active {
		select {
		case <-r.Done:
		case <-time.After(5 * time.Second):
		}
	}
	a.copyWG.Wait()
	var err error
	if a.appDB != nil {
		err = a.appDB.Close()
	}
	a.processLockOnce.Do(func() {
		if lockErr := a.processLock.Release(); err == nil {
			err = lockErr
		}
	})
	return err
}

func (a *App) loadModelConfig(allowFake bool) {
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	a.generator = nil
	a.modelInfo = ModelInfo{}
	a.modelError = ""
	config, err := llm.LoadConfig(a.modelPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			a.modelError = "model configuration could not be read"
		}
		return
	}
	if (config.Provider == "" || config.Provider == "fake") && !allowFake {
		a.modelError = "model_not_configured: a real provider is required"
		return
	}
	provider, _, err := llm.NewProviderFromConfigFile(a.modelPath)
	if err != nil {
		a.modelError = err.Error()
		return
	}
	generator, ok := provider.(model.TextGenerator)
	if !ok {
		a.modelError = "text_generation_unsupported: configured provider does not support text generation"
		return
	}
	a.generator = generator
	a.modelInfo = ModelInfo{Provider: config.Provider, Model: config.Model, Configured: true, Source: "config"}
}

func (a *App) Logger() Logger          { return a.logger }
func (a *App) DataRoot() string        { return a.dataRoot }
func (a *App) ModelConfigPath() string { return a.modelPath }

func (a *App) Status(ctx context.Context) (Status, error) {
	a.modelMu.RLock()
	info, modelErr, ready := a.modelInfo, a.modelError, a.generator != nil
	a.modelMu.RUnlock()
	activeID, revision, err := a.activeWorldState(ctx)
	if err != nil {
		return Status{}, err
	}
	var active *wiaworld.WorldSummary
	if activeID != "" {
		summary, err := a.worldSummary(ctx, activeID)
		if err != nil && !errors.Is(err, ErrWorldNotFound) {
			return Status{}, err
		} else if err == nil {
			active = &summary
		}
	}
	return Status{Ready: ready, Model: info, ModelError: modelErr, UserID: a.userID, ActiveWorld: active, ActiveRevision: revision, DataRoot: a.dataRoot, ModelConfigPath: a.modelPath}, nil
}

func (a *App) ListWorlds(ctx context.Context) ([]wiaworld.WorldSummary, error) {
	rows, err := a.appDB.QueryContext(ctx, `SELECT world_id,name,status,updated_at FROM worlds WHERE user_id=? AND status='ready' ORDER BY updated_at DESC`, a.userID)
	if err != nil {
		return nil, err
	}
	type worldRef struct {
		id, name, status, updated string
	}
	var refs []worldRef
	for rows.Next() {
		var ref worldRef
		if err := rows.Scan(&ref.id, &ref.name, &ref.status, &ref.updated); err != nil {
			_ = rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]wiaworld.WorldSummary, 0, len(refs))
	for _, ref := range refs {
		summary, err := a.worldSummary(ctx, ref.id)
		if err != nil {
			return nil, err
		}
		summary.Name = ref.name
		summary.Status = ref.status
		summary.UpdatedAt, _ = time.Parse(time.RFC3339Nano, ref.updated)
		result = append(result, summary)
	}
	return result, nil
}

func (a *App) worldSummary(ctx context.Context, worldID string) (wiaworld.WorldSummary, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return wiaworld.WorldSummary{}, err
	}
	if status != "ready" {
		return wiaworld.WorldSummary{}, ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return wiaworld.WorldSummary{}, err
	}
	defer store.Close()
	snapshot, err := loadTurnSnapshot(ctx, store, 1)
	if err != nil {
		return wiaworld.WorldSummary{}, err
	}
	snapshot.Summary.Status = status
	return snapshot.Summary, nil
}

func (a *App) ReadWorld(ctx context.Context, worldID string, limit int) (turn.Snapshot, error) {
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return turn.Snapshot{}, err
	}
	if status != "ready" {
		return turn.Snapshot{}, ErrWorldNotReady
	}
	store, err := storage.OpenWorldDB(path)
	if err != nil {
		return turn.Snapshot{}, err
	}
	defer store.Close()
	return loadTurnSnapshot(ctx, store, limit)
}

func (a *App) ReadMessages(ctx context.Context, worldID string, limit int) ([]wiaworld.Message, error) {
	snap, err := a.ReadWorld(ctx, worldID, limit)
	return snap.Messages, err
}
func (a *App) ReadCharacters(ctx context.Context, worldID string) ([]wiaworld.Character, error) {
	snap, err := a.ReadWorld(ctx, worldID, 1)
	return snap.Characters, err
}
func (a *App) ReadMemories(ctx context.Context, worldID, recipient string) ([]wiaworld.Memory, error) {
	snap, err := a.ReadWorld(ctx, worldID, 1)
	return snap.Memories[recipient], err
}
func (a *App) ReadPerceptions(ctx context.Context, worldID, recipient string) ([]wiaworld.Perception, error) {
	snap, err := a.ReadWorld(ctx, worldID, 1)
	return snap.Perceptions[recipient], err
}

func (a *App) activeWorldState(ctx context.Context) (string, int64, error) {
	var id string
	var revision int64
	err := a.appDB.QueryRowContext(ctx, `SELECT active_world_id,active_revision FROM user_play_state WHERE user_id=?`, a.userID).Scan(&id, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return id, revision, err
}

func (a *App) activeWorldStateExact(ctx context.Context) (string, int64, error) {
	var id string
	var rev int64
	err := a.appDB.QueryRowContext(ctx, `SELECT active_world_id,active_revision FROM user_play_state WHERE user_id=?`, a.userID).Scan(&id, &rev)
	return id, rev, err
}

func (a *App) activeWorld(ctx context.Context) (string, int64, error) {
	return a.activeWorldStateExact(ctx)
}

func (a *App) worldRecord(ctx context.Context, worldID string) (string, string, error) {
	var path, status, gameID string
	err := a.appDB.QueryRowContext(ctx, `SELECT path,status,game_id FROM worlds WHERE user_id=? AND world_id=?`, a.userID, worldID).Scan(&path, &status, &gameID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrWorldNotFound
	}
	if err == nil && (!packID.MatchString(gameID) || !packID.MatchString(worldID) || filepath.Clean(path) != filepath.Clean(a.worldPathFor(gameID, worldID))) {
		return "", "", ErrWorldNotFound
	}
	return path, status, err
}

func (a *App) worldPathFor(gameID, worldID string) string {
	return filepath.Join(a.root, "worlds", a.userID, gameID, worldID, "world.db")
}

func (a *App) touchWorld(ctx context.Context, worldID string) error {
	_, err := a.appDB.ExecContext(ctx, `UPDATE worlds SET updated_at=? WHERE user_id=? AND world_id=?`, wire.NowText(), a.userID, worldID)
	return err
}

func (a *App) markInterrupted(ctx context.Context) error {
	rows, err := a.appDB.QueryContext(ctx, `SELECT path FROM worlds WHERE user_id=? AND status='ready'`, a.userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		store, err := storage.OpenWorldDB(path)
		if err != nil {
			return err
		}
		if err := store.MarkRunInterrupted(ctx); err != nil {
			store.Close()
			return err
		}
		store.Close()
	}
	return nil
}

func (a *App) markInterruptedCopies(ctx context.Context) error {
	rows, err := a.appDB.QueryContext(ctx, `SELECT operation_id,target_world_id FROM copy_operations WHERE user_id=? AND status='copying'`, a.userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type copyRef struct{ operationID, worldID string }
	var refs []copyRef
	for rows.Next() {
		var ref copyRef
		if err := rows.Scan(&ref.operationID, &ref.worldID); err != nil {
			return err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, ref := range refs {
		path, _, recordErr := a.worldRecord(ctx, ref.worldID)
		if recordErr == nil {
			_ = os.RemoveAll(filepath.Dir(path))
		}
		if _, err := a.appDB.ExecContext(ctx, `UPDATE copy_operations SET status='failed',error=?,updated_at=? WHERE user_id=? AND operation_id=?`, "另存任务在运行时重启，原存档保持不变", wire.NowText(), a.userID, ref.operationID); err != nil {
			return err
		}
		if _, err := a.appDB.ExecContext(ctx, `UPDATE worlds SET status='failed',updated_at=? WHERE user_id=? AND world_id=?`, wire.NowText(), a.userID, ref.worldID); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) worldRuntimeFor(id string) *worldRuntime {
	a.worldMu.Lock()
	defer a.worldMu.Unlock()
	if w := a.worlds[id]; w != nil {
		return w
	}
	w := &worldRuntime{worldID: id}
	a.worlds[id] = w
	return w
}

func (a *App) isActive(ctx context.Context, worldID string, revision int64) bool {
	id, rev, err := a.activeWorldState(ctx)
	return err == nil && id == worldID && rev == revision
}

func (a *App) hashRun(req RunRequest) string {
	data, _ := json.Marshal(struct{ Input, Addressee string }{req.Input, req.AddresseeID})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ConfigureModel verifies a real provider before publishing its configuration.
// Credentials are kept in the runtime secrets directory and never returned to the client.
func (a *App) ConfigureModel(ctx context.Context, request ModelConfigRequest) (Status, error) {
	provider := strings.TrimSpace(request.Provider)
	if provider != "openai" && provider != "deepseek" {
		return Status{}, ErrInvalidRequest
	}
	if strings.TrimSpace(request.APIKey) == "" {
		return Status{}, ErrInvalidRequest
	}
	a.modelConfigMu.Lock()
	configLockHeld := true
	defer func() {
		if configLockHeld {
			a.modelConfigMu.Unlock()
		}
	}()
	configDir := filepath.Dir(a.modelPath)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return Status{}, err
	}
	secretDir := filepath.Clean(filepath.Join(configDir, "..", "secrets"))
	tmpKey := filepath.Join(secretDir, "model.pending."+wire.NewID("cfg")+".key")
	if err := secret.Write(tmpKey, request.APIKey); err != nil {
		return Status{}, err
	}
	tmpConfig := filepath.Join(configDir, "model.pending."+wire.NewID("cfg")+".json")
	window := llm.DefaultWindowLimits(provider, request.Model)
	config := llm.Config{Provider: provider, Model: strings.TrimSpace(request.Model), BaseURL: strings.TrimRight(strings.TrimSpace(request.BaseURL), "/"), APIKey: "file:../secrets/" + filepath.Base(tmpKey), WindowLimits: window}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(tmpConfig, data, 0o600); err != nil {
		return Status{}, err
	}
	providerClient, _, err := llm.NewProviderFromConfigFile(tmpConfig)
	if err != nil {
		_ = os.Remove(tmpConfig)
		_ = os.Remove(tmpKey)
		return Status{}, fmt.Errorf("model_not_configured: %w", err)
	}
	generator, ok := providerClient.(model.TextGenerator)
	if !ok {
		_ = os.Remove(tmpConfig)
		_ = os.Remove(tmpKey)
		return Status{}, errors.New("text_generation_unsupported")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	probe := model.TextRequest{System: "Reply with one short word.", Input: "ready", MaxInputTokens: 1024, MaxOutputTokens: 64}
	if _, err := a.meteredText(probeCtx, generator, probe, ContextScope{Purpose: "connection"}, ContextBuildReport{InputTokens: framedContextTokens(probe), TotalOutputTokens: 64}); err != nil {
		_ = os.Remove(tmpConfig)
		_ = os.Remove(tmpKey)
		return Status{}, fmt.Errorf("model_not_configured: provider verification failed")
	}
	final := llm.Config{Provider: provider, Model: strings.TrimSpace(request.Model), BaseURL: strings.TrimRight(strings.TrimSpace(request.BaseURL), "/"), APIKey: "file:../secrets/model.key", WindowLimits: window}
	final, err = publishModelConfig(a.modelPath, secretDir, request.APIKey, final)
	if err != nil {
		return Status{}, err
	}
	_ = os.Remove(tmpConfig)
	_ = os.Remove(tmpKey)
	a.modelMu.Lock()
	a.generator = generator
	a.modelInfo = ModelInfo{Provider: provider, Model: final.Model, Configured: true, Source: "config"}
	a.modelError = ""
	a.modelMu.Unlock()
	a.modelConfigMu.Unlock()
	configLockHeld = false
	return a.Status(ctx)
}

func publishModelConfig(configPath, secretDir, apiKey string, modelConfig llm.Config) (llm.Config, error) {
	keyName := "model." + wire.NewID("cfg") + ".key"
	keyPath := filepath.Join(secretDir, keyName)
	if err := secret.Write(keyPath, apiKey); err != nil {
		return llm.Config{}, err
	}
	modelConfig.APIKey = "file:../secrets/" + keyName
	data, err := json.Marshal(modelConfig)
	if err != nil {
		_ = os.Remove(keyPath)
		return llm.Config{}, err
	}
	if err := writeModelConfig(configPath, data); err != nil {
		_ = os.Remove(keyPath)
		return llm.Config{}, err
	}
	return modelConfig, nil
}
