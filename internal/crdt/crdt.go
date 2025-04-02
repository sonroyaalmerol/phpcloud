// Package crdt implements Conflict-free Replicated Data Types
// with Hybrid Logical Clocks for automatic conflict resolution
package crdt

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/hlc"
)

// CRDT is the interface for all CRDT types
type CRDT interface {
	// Merge merges another CRDT into this one (LWW semantics)
	Merge(other CRDT) CRDT
	// GetTimestamp returns the HLC timestamp of this CRDT
	GetTimestamp() hlc.Timestamp
	// IsDeleted returns true if this CRDT is a tombstone (deleted)
	IsDeleted() bool
}

// SessionCRDT is a CRDT for session data
type SessionCRDT struct {
	ID        string        `json:"id"`
	Data      []byte        `json:"data"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	ExpiresAt time.Time     `json:"expires_at"`
	LockedBy  string        `json:"locked_by,omitempty"`
	LockedAt  time.Time     `json:"locked_at,omitempty"`
	Timestamp hlc.Timestamp `json:"ts"`   // HLC timestamp for conflict resolution
	NodeID    string        `json:"node"` // Node that last modified this
	Deleted   bool          `json:"del"`  // Tombstone flag
}

// NewSessionCRDT creates a new session CRDT
func NewSessionCRDT(id string, nodeID string, clock *hlc.Clock) *SessionCRDT {
	now := time.Now()
	return &SessionCRDT{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(time.Hour),
		Timestamp: clock.Now(),
		NodeID:    nodeID,
		Deleted:   false,
	}
}

// Merge implements CRDT.Merge with Last-Write-Wins semantics
func (s *SessionCRDT) Merge(other CRDT) CRDT {
	otherSession, ok := other.(*SessionCRDT)
	if !ok {
		return s
	}

	// If other is newer (LWW), use it
	if s.Timestamp.Less(otherSession.Timestamp) {
		return otherSession
	}

	// If timestamps are equal but other node ID is "larger" (deterministic tie-breaker)
	if s.Timestamp.Equal(otherSession.Timestamp) && s.NodeID < otherSession.NodeID {
		return otherSession
	}

	return s
}

// GetTimestamp returns the HLC timestamp
func (s *SessionCRDT) GetTimestamp() hlc.Timestamp {
	return s.Timestamp
}

// IsDeleted returns true if this is a tombstone
func (s *SessionCRDT) IsDeleted() bool {
	return s.Deleted
}

// IsExpired returns true if the session has expired
func (s *SessionCRDT) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// Update updates the session data with a new timestamp
func (s *SessionCRDT) Update(data []byte, nodeID string, clock *hlc.Clock) {
	s.Data = data
	s.UpdatedAt = time.Now()
	s.Timestamp = clock.Now()
	s.NodeID = nodeID
}

// Lock locks the session
func (s *SessionCRDT) Lock(holder string, clock *hlc.Clock) {
	s.LockedBy = holder
	s.LockedAt = time.Now()
	s.Timestamp = clock.Now()
}

// Unlock unlocks the session
func (s *SessionCRDT) Unlock(clock *hlc.Clock) {
	s.LockedBy = ""
	s.LockedAt = time.Time{}
	s.Timestamp = clock.Now()
}

// Delete marks the session as deleted (tombstone)
func (s *SessionCRDT) Delete(nodeID string, clock *hlc.Clock) {
	s.Deleted = true
	s.Timestamp = clock.Now()
	s.NodeID = nodeID
}

// SessionSet is a set of sessions with CRDT semantics
type SessionSet struct {
	sessions map[string]*SessionCRDT
}

// NewSessionSet creates a new session set
func NewSessionSet() *SessionSet {
	return &SessionSet{
		sessions: make(map[string]*SessionCRDT),
	}
}

// Get retrieves a session by ID
func (ss *SessionSet) Get(id string) *SessionCRDT {
	session, ok := ss.sessions[id]
	if !ok {
		return nil
	}
	if session.IsDeleted() || session.IsExpired() {
		return nil
	}
	return session
}

// Put adds or updates a session
func (ss *SessionSet) Put(session *SessionCRDT) {
	existing, ok := ss.sessions[session.ID]
	if !ok {
		ss.sessions[session.ID] = session
		return
	}

	// Merge using LWW
	merged := existing.Merge(session).(*SessionCRDT)
	ss.sessions[session.ID] = merged
}

// Delete marks a session as deleted
func (ss *SessionSet) Delete(id string, nodeID string, clock *hlc.Clock) {
	session, ok := ss.sessions[id]
	if !ok {
		// Create tombstone
		ss.sessions[id] = &SessionCRDT{
			ID:        id,
			Deleted:   true,
			Timestamp: clock.Now(),
			NodeID:    nodeID,
		}
		return
	}
	session.Delete(nodeID, clock)
}

// GetAll returns all non-deleted, non-expired sessions
func (ss *SessionSet) GetAll() map[string]*SessionCRDT {
	result := make(map[string]*SessionCRDT)
	for id, session := range ss.sessions {
		if !session.IsDeleted() && !session.IsExpired() {
			result[id] = session
		}
	}
	return result
}

// Merge merges another SessionSet into this one
func (ss *SessionSet) Merge(other *SessionSet) {
	for id, otherSession := range other.sessions {
		if existing, ok := ss.sessions[id]; ok {
			ss.sessions[id] = existing.Merge(otherSession).(*SessionCRDT)
		} else {
			ss.sessions[id] = otherSession
		}
	}
}

// Cleanup removes expired and old tombstone entries
func (ss *SessionSet) Cleanup() {
	now := time.Now()
	for id, session := range ss.sessions {
		// Remove expired sessions
		if !session.IsDeleted() && now.After(session.ExpiresAt) {
			delete(ss.sessions, id)
			continue
		}

		// Remove old tombstones (keep for some time for convergence)
		if session.IsDeleted() && now.Sub(time.Unix(0, session.Timestamp.Physical)) > time.Hour {
			delete(ss.sessions, id)
		}
	}
}

// LockCRDT is a CRDT for distributed locks
type LockCRDT struct {
	Key       string        `json:"key"`
	Holder    string        `json:"holder"`
	ExpiresAt time.Time     `json:"expires_at"`
	Timestamp hlc.Timestamp `json:"ts"`
	NodeID    string        `json:"node"`
	Deleted   bool          `json:"del"`
}

// NewLockCRDT creates a new lock CRDT
func NewLockCRDT(key, holder, nodeID string, ttl time.Duration, clock *hlc.Clock) *LockCRDT {
	return &LockCRDT{
		Key:       key,
		Holder:    holder,
		ExpiresAt: time.Now().Add(ttl),
		Timestamp: clock.Now(),
		NodeID:    nodeID,
		Deleted:   false,
	}
}

// Merge implements CRDT.Merge with LWW for locks
// Special handling: if lock is expired, prefer the newer acquisition
func (l *LockCRDT) Merge(other CRDT) CRDT {
	otherLock, ok := other.(*LockCRDT)
	if !ok {
		return l
	}

	now := time.Now()
	lExpired := now.After(l.ExpiresAt)
	otherExpired := now.After(otherLock.ExpiresAt)

	// If one is expired and other is not, prefer the non-expired
	if lExpired && !otherExpired {
		return otherLock
	}
	if !lExpired && otherExpired {
		return l
	}

	// Both expired or both valid - use LWW
	if l.Timestamp.Less(otherLock.Timestamp) {
		return otherLock
	}
	if l.Timestamp.Equal(otherLock.Timestamp) && l.NodeID < otherLock.NodeID {
		return otherLock
	}

	return l
}

// GetTimestamp returns the HLC timestamp
func (l *LockCRDT) GetTimestamp() hlc.Timestamp {
	return l.Timestamp
}

// IsDeleted returns true if this is released
func (l *LockCRDT) IsDeleted() bool {
	return l.Deleted || time.Now().After(l.ExpiresAt)
}

// IsHeld returns true if lock is currently held
func (l *LockCRDT) IsHeld() bool {
	return !l.Deleted && time.Now().Before(l.ExpiresAt)
}

// IsHeldBy returns true if lock is held by specific holder
func (l *LockCRDT) IsHeldBy(holder string) bool {
	return l.Holder == holder && l.IsHeld()
}

// Release marks the lock as released
func (l *LockCRDT) Release(nodeID string, clock *hlc.Clock) {
	l.Deleted = true
	l.Timestamp = clock.Now()
	l.NodeID = nodeID
}

// LockSet is a set of locks with CRDT semantics
type LockSet struct {
	locks map[string]*LockCRDT
}

// NewLockSet creates a new lock set
func NewLockSet() *LockSet {
	return &LockSet{
		locks: make(map[string]*LockCRDT),
	}
}

// Get retrieves a lock by key
func (ls *LockSet) Get(key string) *LockCRDT {
	lock, ok := ls.locks[key]
	if !ok {
		return nil
	}
	if lock.IsDeleted() {
		return nil
	}
	return lock
}

// Put adds or updates a lock
func (ls *LockSet) Put(lock *LockCRDT) {
	existing, ok := ls.locks[lock.Key]
	if !ok {
		ls.locks[lock.Key] = lock
		return
	}

	merged := existing.Merge(lock).(*LockCRDT)
	ls.locks[lock.Key] = merged
}

// Release marks a lock as released
func (ls *LockSet) Release(key, holder, nodeID string, clock *hlc.Clock) bool {
	lock, ok := ls.locks[key]
	if !ok {
		return false
	}

	// Only holder can release
	if !lock.IsHeldBy(holder) {
		return false
	}

	lock.Release(nodeID, clock)
	return true
}

// IsHeld checks if a lock is held
func (ls *LockSet) IsHeld(key string) bool {
	lock := ls.Get(key)
	if lock == nil {
		return false
	}
	return lock.IsHeld()
}

// IsHeldBy checks if a lock is held by a specific holder
func (ls *LockSet) IsHeldBy(key, holder string) bool {
	lock := ls.Get(key)
	if lock == nil {
		return false
	}
	return lock.IsHeldBy(holder)
}

// GetAll returns all non-expired locks
func (ls *LockSet) GetAll() map[string]*LockCRDT {
	result := make(map[string]*LockCRDT)
	for key, lock := range ls.locks {
		if !lock.IsDeleted() {
			result[key] = lock
		}
	}
	return result
}

// Merge merges another LockSet into this one
func (ls *LockSet) Merge(other *LockSet) {
	for key, otherLock := range other.locks {
		if existing, ok := ls.locks[key]; ok {
			ls.locks[key] = existing.Merge(otherLock).(*LockCRDT)
		} else {
			ls.locks[key] = otherLock
		}
	}
}

// Cleanup removes expired locks and old tombstones
func (ls *LockSet) Cleanup() {
	now := time.Now()
	for key, lock := range ls.locks {
		// Remove old tombstones
		if lock.Deleted && now.Sub(time.Unix(0, lock.Timestamp.Physical)) > 5*time.Minute {
			delete(ls.locks, key)
		}
	}
}

// RegisterCRDT is a CRDT for node registration/version metadata
type RegisterCRDT struct {
	Value     string        `json:"value"`
	Timestamp hlc.Timestamp `json:"ts"`
	NodeID    string        `json:"node"`
}

// NewRegisterCRDT creates a new register CRDT
func NewRegisterCRDT(value, nodeID string, clock *hlc.Clock) *RegisterCRDT {
	return &RegisterCRDT{
		Value:     value,
		Timestamp: clock.Now(),
		NodeID:    nodeID,
	}
}

// Merge implements LWW register semantics
func (r *RegisterCRDT) Merge(other *RegisterCRDT) *RegisterCRDT {
	if r.Timestamp.Less(other.Timestamp) {
		return other
	}
	if r.Timestamp.Equal(other.Timestamp) && r.NodeID < other.NodeID {
		return other
	}
	return r
}

// Set updates the register value
func (r *RegisterCRDT) Set(value, nodeID string, clock *hlc.Clock) {
	r.Value = value
	r.Timestamp = clock.Now()
	r.NodeID = nodeID
}

// MapCRDT is a CRDT map for metadata
type MapCRDT struct {
	entries map[string]*RegisterCRDT
}

// NewMapCRDT creates a new map CRDT
func NewMapCRDT() *MapCRDT {
	return &MapCRDT{
		entries: make(map[string]*RegisterCRDT),
	}
}

// Get retrieves a value
func (m *MapCRDT) Get(key string) (string, bool) {
	reg, ok := m.entries[key]
	if !ok {
		return "", false
	}
	return reg.Value, true
}

// Set sets a value
func (m *MapCRDT) Set(key, value, nodeID string, clock *hlc.Clock) {
	m.entries[key] = NewRegisterCRDT(value, nodeID, clock)
}

// Delete removes a key (by setting empty value with timestamp)
func (m *MapCRDT) Delete(key, nodeID string, clock *hlc.Clock) {
	m.entries[key] = NewRegisterCRDT("", nodeID, clock)
}

// GetAll returns all entries
func (m *MapCRDT) GetAll() map[string]string {
	result := make(map[string]string)
	for key, reg := range m.entries {
		if reg.Value != "" {
			result[key] = reg.Value
		}
	}
	return result
}

// Merge merges another MapCRDT
func (m *MapCRDT) Merge(other *MapCRDT) {
	for key, otherReg := range other.entries {
		if existing, ok := m.entries[key]; ok {
			m.entries[key] = existing.Merge(otherReg)
		} else {
			m.entries[key] = otherReg
		}
	}
}

// StateSnapshot is a complete state snapshot for gossip replication
type StateSnapshot struct {
	Sessions map[string]*SessionCRDT  `json:"sessions"`
	Locks    map[string]*LockCRDT     `json:"locks"`
	Meta     map[string]*RegisterCRDT `json:"meta"`
	NodeID   string                   `json:"node_id"`
	Version  hlc.Timestamp            `json:"version"`
}

// NewStateSnapshot creates a new state snapshot
func NewStateSnapshot(nodeID string, clock *hlc.Clock) *StateSnapshot {
	return &StateSnapshot{
		Sessions: make(map[string]*SessionCRDT),
		Locks:    make(map[string]*LockCRDT),
		Meta:     make(map[string]*RegisterCRDT),
		NodeID:   nodeID,
		Version:  clock.Now(),
	}
}

// Serialize serializes the snapshot to JSON
func (ss *StateSnapshot) Serialize() ([]byte, error) {
	return json.Marshal(ss)
}

// DeserializeSnapshot deserializes a snapshot from JSON
func DeserializeSnapshot(data []byte) (*StateSnapshot, error) {
	var ss StateSnapshot
	if err := json.Unmarshal(data, &ss); err != nil {
		return nil, fmt.Errorf("failed to deserialize snapshot: %w", err)
	}
	return &ss, nil
}

// Delta is a partial update containing only changed entries
type Delta struct {
	Sessions map[string]*SessionCRDT  `json:"sessions,omitempty"`
	Locks    map[string]*LockCRDT     `json:"locks,omitempty"`
	Meta     map[string]*RegisterCRDT `json:"meta,omitempty"`
	NodeID   string                   `json:"node_id"`
	Version  hlc.Timestamp            `json:"version"`
}

// NewDelta creates a new empty delta
func NewDelta(nodeID string, clock *hlc.Clock) *Delta {
	return &Delta{
		Sessions: make(map[string]*SessionCRDT),
		Locks:    make(map[string]*LockCRDT),
		Meta:     make(map[string]*RegisterCRDT),
		NodeID:   nodeID,
		Version:  clock.Now(),
	}
}
