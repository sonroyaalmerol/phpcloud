package lock

import (
	"context"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/db"
	"go.uber.org/zap"
)

// Manager handles distributed locking using CRDTs with HLC
type Manager interface {
	Acquire(ctx context.Context, key string, holder string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, key string, holder string) error
	IsHeld(ctx context.Context, key string, holder string) (bool, error)
}

// CRDTLockManager implements distributed locking using CRDTs with HLC
// Provides automatic conflict resolution in a leaderless AP system
type CRDTLockManager struct {
	config *config.Config
	db     *db.Manager
	logger *zap.Logger
}

// New creates a new CRDT lock manager
func New(cfg *config.Config, dbManager *db.Manager, logger *zap.Logger) (Manager, error) {
	return &CRDTLockManager{
		config: cfg,
		db:     dbManager,
		logger: logger,
	}, nil
}

// Acquire attempts to acquire a distributed lock using CRDT semantics
// Returns true if lock was acquired, false if already held by another node
func (m *CRDTLockManager) Acquire(ctx context.Context, key string, holder string, ttl time.Duration) (bool, error) {
	return m.db.AcquireLock(key, holder, ttl)
}

// Release releases a lock held by the specified holder
func (m *CRDTLockManager) Release(ctx context.Context, key string, holder string) error {
	return m.db.ReleaseLock(key, holder)
}

// IsHeld checks if a specific holder currently holds the lock
// Uses CRDT LWW semantics for automatic conflict resolution
func (m *CRDTLockManager) IsHeld(ctx context.Context, key string, holder string) (bool, error) {
	return m.db.IsLockHeldBy(key, holder), nil
}
