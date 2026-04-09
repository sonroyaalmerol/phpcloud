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
