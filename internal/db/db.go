// Package db provides ephemeral in-memory storage using CRDTs with HLC
// for automatic conflict resolution in a leaderless AP system.
// State is replicated across the cluster via gossip protocol.
package db

import (
	"context"
	"sync"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/crdt"
	"github.com/sonroyaalmerol/phpcloud/internal/hlc"
	"github.com/puzpuzpuz/xsync/v4"
	"go.uber.org/zap"
)

// Manager handles ephemeral in-memory CRDT storage
// All state is replicated across cluster nodes via gossip
// Uses Last-Write-Wins with HLC for automatic conflict resolution
type Manager struct {
	config *config.Config
	logger *zap.Logger
	nodeID string

	// HLC clock for this node
	clock *hlc.Clock

	// CRDT data structures
	sessions *xsync.Map[string, *crdt.SessionCRDT]
	locks    *xsync.Map[string, *crdt.LockCRDT]
	meta     *xsync.Map[string, *crdt.RegisterCRDT]

	// Gossip state tracking
	lastGossipVersion hlc.Timestamp
	gossipMu          sync.RWMutex

	// Background cleanup control
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new CRDT storage manager
func New(cfg *config.Config, nodeID string, logger *zap.Logger) (*Manager, error) {
	if nodeID == "" {
		nodeID = "unknown"
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &Manager{
		config:   cfg,
		logger:   logger,
		nodeID:   nodeID,
		clock:    hlc.NewClock(nodeID),
		sessions: xsync.NewMap[string, *crdt.SessionCRDT](),
		locks:    xsync.NewMap[string, *crdt.LockCRDT](),
		meta:     xsync.NewMap[string, *crdt.RegisterCRDT](),
		ctx:      ctx,
		cancel:   cancel,
	}

	// Start background cleanup
	go m.cleanupLoop()

	logger.Info("CRDT storage initialized",
		zap.String("node_id", nodeID),
		zap.Time("hlc_physical_time", time.Unix(0, m.clock.GetLatest().Physical)))

	return m, nil
}

// cleanupLoop periodically removes expired entries
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.cleanupExpired()
		case <-m.ctx.Done():
			return
		}
	}
}

// cleanupExpired removes expired sessions and locks
func (m *Manager) cleanupExpired() {
	now := time.Now()
	count := 0

	// Cleanup expired sessions
	m.sessions.Range(func(id string, s *crdt.SessionCRDT) bool {
		if s.IsExpired() || (s.IsDeleted() && now.Sub(time.Unix(0, s.Timestamp.Physical)) > time.Hour) {
			m.sessions.Delete(id)
			count++
		}
		return true
	})

	// Cleanup expired locks
	m.locks.Range(func(key string, l *crdt.LockCRDT) bool {
		if l.IsDeleted() && now.Sub(time.Unix(0, l.Timestamp.Physical)) > 5*time.Minute {
			m.locks.Delete(key)
			count++
		}
		return true
	})

	if count > 0 {
		m.logger.Debug("Cleaned up expired CRDT entries", zap.Int("count", count))
	}
}

// GetClock returns the HLC clock for this node
func (m *Manager) GetClock() *hlc.Clock {
	return m.clock
}

// GetNodeID returns this node's ID
func (m *Manager) GetNodeID() string {
	return m.nodeID
}

// ==================== Session Operations ====================

// GetSession retrieves a session by ID
// Returns nil if not found, expired, or deleted (tombstone)
func (m *Manager) GetSession(id string) *crdt.SessionCRDT {
	if id == "" {
		return nil
	}

	session, ok := m.sessions.Load(id)
	if !ok {
		return nil
	}

	// Check if deleted (tombstone)
	if session.IsDeleted() {
		return nil
	}

	// Check if expired
	if session.IsExpired() {
		m.sessions.Delete(id)
		return nil
	}

	return session
}

// SaveSession saves a session (creates new or updates existing with LWW)
// Note: If session was deleted (tombstone exists), this will NOT resurrect it
func (m *Manager) SaveSession(session *crdt.SessionCRDT) {
	// Set HLC timestamp
	session.Timestamp = m.clock.Now()
	session.NodeID = m.nodeID

	// Load existing and merge
	existing, loaded := m.sessions.LoadOrStore(session.ID, session)
	if loaded {
		// If existing is a tombstone (deleted) and new session is not a resurrection,
		// preserve the tombstone unless the new session has a strictly newer timestamp
		if existing.IsDeleted() && !session.IsDeleted() {
			// If existing tombstone has newer or equal timestamp, keep it
			if existing.Timestamp.Less(session.Timestamp) {
				// New session is strictly newer, allow it (resurrection)
				merged := existing.Merge(session).(*crdt.SessionCRDT)
				m.sessions.Store(session.ID, merged)
			}
			// Otherwise, keep the tombstone (don't store)
			return
		}

		// Merge using LWW
		merged := existing.Merge(session).(*crdt.SessionCRDT)
		m.sessions.Store(session.ID, merged)
	}
}

// CreateSession creates a new session
func (m *Manager) CreateSession(id string, data []byte, ttl time.Duration) *crdt.SessionCRDT {
	session := crdt.NewSessionCRDT(id, m.nodeID, m.clock)
	session.Data = data
	session.ExpiresAt = time.Now().Add(ttl)

	m.SaveSession(session)
	return session
}

// DeleteSession marks a session as deleted (tombstone)
func (m *Manager) DeleteSession(id string) {
	session, ok := m.sessions.Load(id)
	if ok {
		session.Delete(m.nodeID, m.clock)
		m.sessions.Store(id, session)
	}
}

// GetAllSessions returns all non-deleted, non-expired sessions
func (m *Manager) GetAllSessions() map[string]*crdt.SessionCRDT {
	result := make(map[string]*crdt.SessionCRDT)
	now := time.Now()

	m.sessions.Range(func(id string, s *crdt.SessionCRDT) bool {
		if !s.IsDeleted() && !s.IsExpired() && now.Before(s.ExpiresAt) {
			result[id] = s
		}
		return true
	})

	return result
}

// ==================== Lock Operations ====================

// AcquireLock attempts to acquire a distributed lock
func (m *Manager) AcquireLock(key string, holder string, ttl time.Duration) (bool, error) {
	newLock := crdt.NewLockCRDT(key, holder, m.nodeID, ttl, m.clock)

	existing, loaded := m.locks.LoadOrStore(key, newLock)
	if !loaded {
		// Lock was not present, we acquired it
		return true, nil
	}

	// Check if existing lock is expired
	if existing.IsDeleted() {
		// Lock is released, we can take it
		m.locks.Store(key, newLock)
		return true, nil
	}

	// Lock is held by someone else and not expired
	return false, nil
}

// GetLock retrieves lock info
func (m *Manager) GetLock(key string) *crdt.LockCRDT {
	lock, ok := m.locks.Load(key)
	if !ok {
		return nil
	}

	if lock.IsDeleted() {
		return nil
	}

	return lock
}

// ReleaseLock releases a lock held by the specified holder
func (m *Manager) ReleaseLock(key string, holder string) error {
	lock, ok := m.locks.Load(key)
	if !ok {
		return nil // Already released
	}

	// Only release if held by this holder
	if lock.IsHeldBy(holder) {
		lock.Release(m.nodeID, m.clock)
		m.locks.Store(key, lock)
	}

	return nil
}

// IsLockHeld checks if a lock is currently held
func (m *Manager) IsLockHeld(key string) bool {
	lock := m.GetLock(key)
	if lock == nil {
		return false
	}
	return lock.IsHeld()
}

// IsLockHeldBy checks if a lock is held by a specific holder
func (m *Manager) IsLockHeldBy(key string, holder string) bool {
	lock := m.GetLock(key)
	if lock == nil {
		return false
	}
	return lock.IsHeldBy(holder)
}

// GetAllLocks returns all non-expired locks
func (m *Manager) GetAllLocks() map[string]*crdt.LockCRDT {
	result := make(map[string]*crdt.LockCRDT)

	m.locks.Range(func(key string, l *crdt.LockCRDT) bool {
		if !l.IsDeleted() {
			result[key] = l
		}
		return true
	})

	return result
}

// ==================== Metadata Operations ====================

// GetMeta retrieves metadata value
func (m *Manager) GetMeta(key string) (string, bool) {
	reg, ok := m.meta.Load(key)
	if !ok {
		return "", false
	}
	return reg.Value, reg.Value != ""
}

// SetMeta sets metadata value (LWW register)
func (m *Manager) SetMeta(key string, value string) {
	newReg := crdt.NewRegisterCRDT(value, m.nodeID, m.clock)

	existing, loaded := m.meta.LoadOrStore(key, newReg)
	if loaded {
		merged := existing.Merge(newReg)
		m.meta.Store(key, merged)
	}
}

// DeleteMeta deletes metadata (sets empty value with timestamp)
func (m *Manager) DeleteMeta(key string) {
	m.SetMeta(key, "")
}

// GetAllMeta returns all non-empty metadata
func (m *Manager) GetAllMeta() map[string]string {
	result := make(map[string]string)

	m.meta.Range(func(key string, r *crdt.RegisterCRDT) bool {
		if r.Value != "" {
			result[key] = r.Value
		}
		return true
	})

	return result
}

// ==================== CRDT Merge Operations ====================

// MergeSessions merges sessions from another node
func (m *Manager) MergeSessions(other map[string]*crdt.SessionCRDT) {
	for id, otherSession := range other {
		existing, loaded := m.sessions.LoadOrStore(id, otherSession)
		if loaded {
			// Update our clock with the received timestamp
			m.clock.Witness(otherSession.Timestamp)
			// Merge using LWW
			merged := existing.Merge(otherSession).(*crdt.SessionCRDT)
			m.sessions.Store(id, merged)
		}
	}
}

// MergeLocks merges locks from another node
func (m *Manager) MergeLocks(other map[string]*crdt.LockCRDT) {
	for key, otherLock := range other {
		existing, loaded := m.locks.LoadOrStore(key, otherLock)
		if loaded {
			// Update our clock with the received timestamp
			m.clock.Witness(otherLock.Timestamp)
			// Merge using LWW with lock-specific logic
			merged := existing.Merge(otherLock).(*crdt.LockCRDT)
			m.locks.Store(key, merged)
		}
	}
}

// MergeMeta merges metadata from another node
func (m *Manager) MergeMeta(other map[string]*crdt.RegisterCRDT) {
	for key, otherReg := range other {
		existing, loaded := m.meta.LoadOrStore(key, otherReg)
		if loaded {
			// Update our clock with the received timestamp
			m.clock.Witness(otherReg.Timestamp)
			// Merge using LWW
			merged := existing.Merge(otherReg)
			m.meta.Store(key, merged)
		}
	}
}

// ==================== State Snapshots ====================

// GetStateSnapshot returns a complete state snapshot for gossip replication
func (m *Manager) GetStateSnapshot() *crdt.StateSnapshot {
	snapshot := crdt.NewStateSnapshot(m.nodeID, m.clock)

	// Add all sessions
	m.sessions.Range(func(id string, s *crdt.SessionCRDT) bool {
		snapshot.Sessions[id] = s
		return true
	})

	// Add all locks
	m.locks.Range(func(key string, l *crdt.LockCRDT) bool {
		snapshot.Locks[key] = l
		return true
	})

	// Add all metadata
	m.meta.Range(func(key string, r *crdt.RegisterCRDT) bool {
		snapshot.Meta[key] = r
		return true
	})

	// Update last gossip version
	m.gossipMu.Lock()
	m.lastGossipVersion = snapshot.Version
	m.gossipMu.Unlock()

	return snapshot
}

// ImportStateSnapshot imports a full state snapshot from gossip
func (m *Manager) ImportStateSnapshot(snapshot *crdt.StateSnapshot) {
	if snapshot == nil {
		return
	}

	// Update HLC with the snapshot version
	m.clock.Update(snapshot.Version)

	// Merge all state
	if snapshot.Sessions != nil {
		m.MergeSessions(snapshot.Sessions)
	}
	if snapshot.Locks != nil {
		m.MergeLocks(snapshot.Locks)
	}
	if snapshot.Meta != nil {
		m.MergeMeta(snapshot.Meta)
	}

	m.logger.Debug("Imported state snapshot",
		zap.String("from_node", snapshot.NodeID),
		zap.String("version", snapshot.Version.String()))
}

// GetDeltaSince returns changes since a given version
// Used for efficient incremental gossip
func (m *Manager) GetDeltaSince(since hlc.Timestamp) *crdt.Delta {
	delta := crdt.NewDelta(m.nodeID, m.clock)

	// Get sessions newer than since
	m.sessions.Range(func(id string, s *crdt.SessionCRDT) bool {
		if since.Less(s.Timestamp) {
			delta.Sessions[id] = s
		}
		return true
	})

	// Get locks newer than since
	m.locks.Range(func(key string, l *crdt.LockCRDT) bool {
		if since.Less(l.Timestamp) {
			delta.Locks[key] = l
		}
		return true
	})

	// Get metadata newer than since
	m.meta.Range(func(key string, r *crdt.RegisterCRDT) bool {
		if since.Less(r.Timestamp) {
			delta.Meta[key] = r
		}
		return true
	})

	return delta
}

// ApplyDelta applies a delta update from another node
func (m *Manager) ApplyDelta(delta *crdt.Delta) {
	if delta == nil {
		return
	}

	// Update HLC with the delta version
	m.clock.Update(delta.Version)

	// Merge all delta entries
	if len(delta.Sessions) > 0 {
		m.MergeSessions(delta.Sessions)
	}
	if len(delta.Locks) > 0 {
		m.MergeLocks(delta.Locks)
	}
	if len(delta.Meta) > 0 {
		m.MergeMeta(delta.Meta)
	}

	m.logger.Debug("Applied state delta",
		zap.String("from_node", delta.NodeID),
		zap.Int("sessions", len(delta.Sessions)),
		zap.Int("locks", len(delta.Locks)),
		zap.Int("meta", len(delta.Meta)))
}

// GetLastGossipVersion returns the version of the last gossip snapshot
func (m *Manager) GetLastGossipVersion() hlc.Timestamp {
	m.gossipMu.RLock()
	defer m.gossipMu.RUnlock()
	return m.lastGossipVersion
}

// ==================== Lifecycle ====================

// Close closes the storage manager
func (m *Manager) Close() error {
	m.cancel()
	return nil
}

// RunGC triggers garbage collection
func (m *Manager) RunGC() error {
	m.cleanupExpired()
	return nil
}
