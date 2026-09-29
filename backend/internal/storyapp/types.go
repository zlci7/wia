package storyapp

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"gameagent/backend/internal/model"
	wiaworld "gameagent/backend/internal/world"
)

const (
	LocalUserID   = "local"
	GameID        = "lantern-dusk"
	SchemaVersion = 1
)

var (
	ErrUnauthorized        = errors.New("unauthorized")
	ErrForbidden           = errors.New("forbidden")
	ErrWorldNotFound       = errors.New("world not found")
	ErrWorldNotReady       = errors.New("world not ready")
	ErrWorldBusy           = errors.New("world is busy")
	ErrStoryEnded          = errors.New("guided story has ended")
	ErrAppBusy             = errors.New("story app is already open for this data root")
	ErrVersionConflict     = errors.New("version conflict")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrModelNotConfigured  = errors.New("model not configured")
	ErrGenerationFailed    = errors.New("generation failed")
	ErrStorageUnavailable  = errors.New("storage unavailable")
	ErrSaveFailed          = errors.New("save failed")
	ErrInvalidRequest      = errors.New("invalid request")
	ErrRunNotFound         = errors.New("run not found")
	ErrPersonaNotFound     = errors.New("persona not found")
	ErrContentNotFound     = errors.New("content not found")
	ErrContentBusy         = errors.New("content is busy")
	ErrContentInvalid      = errors.New("content is invalid")
)

type Options struct {
	StoryPacksPath  string
	DataRoot        string
	ModelConfigPath string
	UserID          string
	Generator       model.TextGenerator
	AllowFake       bool
	Logger          Logger
	// WorldPlayerName overrides the lead character name of worlds created through
	// this instance; the disposable browser fixture uses it.
	WorldPlayerName string
}

type Logger interface {
	Printf(string, ...any)
}

type App struct {
	packs      map[string]loadedPack
	packErrors []PackIssue
	packRoot   string
	// packsMu guards the story directory. M3 publishes into a running process, so the
	// map is no longer written only at startup; loading and validating a package stays
	// outside the lock and only the directory entry swap is inside it.
	packsMu sync.RWMutex
	// liveOps holds the publications running in this process, keyed by request key, so
	// a repeated request returns the live operation instead of recovering it.
	liveMu  sync.Mutex
	liveOps map[string]ContentOperation
	// contentDir holds this instance's published user content. Tests may point it
	// at a temporary directory.
	contentDir string
	// worldPlayerName overrides the lead character name of worlds created through
	// this instance; the disposable browser fixture uses it.
	worldPlayerName string
	createMu        sync.Mutex
	root            string
	userID          string
	appDB           *sql.DB
	dataRoot        string
	processLock     *processLock
	processLockOnce sync.Once
	modelPath       string
	modelMu         sync.RWMutex
	modelConfigMu   sync.Mutex
	generator       model.TextGenerator
	modelInfo       ModelInfo
	modelError      string
	worldMu         sync.Mutex
	worlds          map[string]*worldRuntime
	activationMu    sync.Mutex
	runsMu          sync.Mutex
	runs            map[string]*runRuntime
	copyCtx         context.Context
	copyCancel      context.CancelFunc
	copyMu          sync.Mutex
	copyWG          sync.WaitGroup
	memoryWorkers   map[string]bool
	memoryWake      map[string]bool
	closing         bool
	logger          Logger
	closed          chan struct{}
}

type ModelInfo struct {
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
}

type ModelConfigRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key"`
}

type Status struct {
	Ready           bool                   `json:"ready"`
	Model           ModelInfo              `json:"model"`
	ModelError      string                 `json:"model_error,omitempty"`
	UserID          string                 `json:"user_id"`
	ActiveWorld     *wiaworld.WorldSummary `json:"active_world"`
	ActiveRevision  int64                  `json:"active_revision"`
	DataRoot        string                 `json:"-"`
	ModelConfigPath string                 `json:"-"`
}

type GameSummary struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Modes       []string       `json:"modes"`
	DefaultMode string         `json:"default_mode"`
	Revision    string         `json:"revision"`
	Mode        string         `json:"mode"`
	Gameplay    string         `json:"gameplay"`
	Background  string         `json:"background"`
	CoverURL    string         `json:"cover_url,omitempty"`
	CoverAlt    string         `json:"cover_alt,omitempty"`
	Player      PlayerDefaults `json:"player"`
}

// SceneLocation is the current location's identifier. Presence is decided by
// identity, not by comparing human-readable scene text.

// Avatar is a package-relative asset reference from the definition, not a world
// resource name; the world snapshot records its own copy separately.

// SpeakingExamples are authored dialogue samples showing how this character
// sounds. They are style material, never events that happened.

// PublicCharacter is the player-facing character projection. Private role
// material stays inside the story runtime and is never sent through ordinary
// play routes.

func PublicCharacterViews(characters []wiaworld.Character) []wiaworld.PublicCharacter {
	views := make([]wiaworld.PublicCharacter, 0, len(characters))
	for _, character := range characters {
		views = append(views, wiaworld.PublicCharacter{
			Appearance: character.Appearance,
			EntityID:   character.EntityID, DefinitionID: character.DefinitionID,
			Name: character.Name, Role: character.Role, InScene: character.InScene,
		})
	}
	return views
}

type SaveOperation struct {
	OperationID   string    `json:"operation_id"`
	RequestKey    string    `json:"request_key"`
	SourceWorldID string    `json:"source_world_id"`
	TargetWorldID string    `json:"target_world_id"`
	TargetName    string    `json:"target_name"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type RunRequest struct {
	RequestKey             string `json:"request_key"`
	Input                  string `json:"input"`
	AddresseeID            string `json:"addressee_id,omitempty"`
	ExpectedActiveRevision int64  `json:"expected_active_revision"`
	ExpectedMessageHead    int64  `json:"expected_message_head"`
	ExpectedEventHead      int64  `json:"expected_event_head"`
	ExpectedContextEpoch   int64  `json:"expected_context_epoch"`
	attempt                int
	inputID                string
	inputSeq               int64
	requireBaseline        bool
	expectedTurnSeq        int64
	expectedSceneVersion   int64
}

type runRuntime struct {
	Cancel         context.CancelFunc
	Done           chan struct{}
	WorldID        string
	RunID          string
	ActiveRevision int64
	Generator      model.TextGenerator
}

type worldRuntime struct {
	suggestionID     string
	suggestionCancel context.CancelFunc
	mu               sync.Mutex
	worldID          string
	savePending      bool
	pendingOperation string
}
