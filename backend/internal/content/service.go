package content

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

var (
	ErrContentNotFound     = errors.New("content not found")
	ErrContentBusy         = errors.New("content is busy")
	ErrContentInvalid      = errors.New("content is invalid")
	ErrInvalidRequest      = errors.New("invalid request")
	ErrVersionConflict     = errors.New("version conflict")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrStorageUnavailable  = errors.New("storage unavailable")
)

type Logger interface {
	Printf(string, ...any)
}

// Service owns authoring state, the pack catalog and publication recovery.
type Service struct {
	packs      map[string]loadedPack
	packErrors []PackIssue
	packRoot   string
	packsMu    sync.RWMutex
	liveMu     sync.Mutex
	liveOps    map[string]ContentOperation
	contentDir string
	root       string
	userID     string
	appDB      *sql.DB
	logger     Logger
}

type ServiceOptions struct {
	Root       string
	ContentDir string
	UserID     string
	DB         *sql.DB
	Logger     Logger
}

func NewService(options ServiceOptions) *Service {
	return &Service{
		packs:      map[string]loadedPack{},
		packErrors: []PackIssue{},
		liveOps:    map[string]ContentOperation{},
		contentDir: options.ContentDir,
		root:       options.Root,
		userID:     options.UserID,
		appDB:      options.DB,
		logger:     options.Logger,
	}
}

func (s *Service) Initialize(ctx context.Context, packsPath string) error {
	if err := s.loadPacks(ctx, packsPath); err != nil {
		return err
	}
	if err := s.recoverContentOperations(ctx); err != nil {
		return err
	}
	return s.loadPublishedRevisions(ctx)
}

func (s *Service) Pack(id string) (LoadedPack, bool) { return s.pack(id) }

func (s *Service) SetPack(id string, pack LoadedPack) { s.setPack(id, pack) }

func (s *Service) PackRoot() string { return s.packRoot }
