package crdt

import (
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/hlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers

func newClock(node string) *hlc.Clock {
	return hlc.NewClock(node)
}

// ─── SessionCRDT ────────────────────────────────────────────────────────────

func TestSessionCRDT_New(t *testing.T) {
	clk := newClock("n1")
	s := NewSessionCRDT("sess1", "n1", clk)

	require.NotNil(t, s)
	assert.Equal(t, "sess1", s.ID)
	assert.Equal(t, "n1", s.NodeID)
	assert.False(t, s.Deleted)
	assert.False(t, s.IsExpired(), "fresh session should not be expired")
	assert.False(t, s.IsDeleted())
}

func TestSessionCRDT_Merge_LWW(t *testing.T) {
	clk := newClock("n1")

	older := NewSessionCRDT("s", "n1", clk)
	older.Data = []byte("old")

	newer := NewSessionCRDT("s", "n1", clk)
	newer.Data = []byte("new")

	result := older.Merge(newer).(*SessionCRDT)
	assert.Equal(t, []byte("new"), result.Data, "newer timestamp wins")

	// Reverse: older should still lose
	result2 := newer.Merge(older).(*SessionCRDT)
	assert.Equal(t, []byte("new"), result2.Data)
}

func TestSessionCRDT_Merge_TieBreakByNodeID(t *testing.T) {
	// Two sessions with identical timestamps but different node IDs
	// The lexicographically larger node ID should win
	ts := hlc.Timestamp{Physical: 1000, Logical: 0}

	a := &SessionCRDT{ID: "s", Data: []byte("a"), NodeID: "node-a", Timestamp: ts}
	b := &SessionCRDT{ID: "s", Data: []byte("b"), NodeID: "node-b", Timestamp: ts}

	result := a.Merge(b).(*SessionCRDT)
	assert.Equal(t, "node-b", result.NodeID, "larger node ID wins on tie")

	result2 := b.Merge(a).(*SessionCRDT)
	assert.Equal(t, "node-b", result2.NodeID, "result must be deterministic")
}

func TestSessionCRDT_Delete(t *testing.T) {
	clk := newClock("n1")
	s := NewSessionCRDT("s", "n1", clk)
	assert.False(t, s.IsDeleted())

	s.Delete("n1", clk)
	assert.True(t, s.IsDeleted())
}

func TestSessionCRDT_Expiry(t *testing.T) {
	clk := newClock("n1")
	s := NewSessionCRDT("s", "n1", clk)
	s.ExpiresAt = time.Now().Add(-time.Second) // already expired

	assert.True(t, s.IsExpired())
}

func TestSessionCRDT_Lock_Unlock(t *testing.T) {
	clk := newClock("n1")
	s := NewSessionCRDT("s", "n1", clk)

	s.Lock("holder-1", clk)
	assert.Equal(t, "holder-1", s.LockedBy)

	s.Unlock(clk)
	assert.Empty(t, s.LockedBy)
	assert.True(t, s.LockedAt.IsZero())
}

func TestSessionCRDT_Merge_TypeMismatch(t *testing.T) {
	clk := newClock("n1")
	s := NewSessionCRDT("s", "n1", clk)

	l := NewLockCRDT("k", "holder", "n1", time.Minute, clk)
	result := s.Merge(l)
	assert.Same(t, s, result.(*SessionCRDT), "wrong type returns self")
}

// ─── LockCRDT ───────────────────────────────────────────────────────────────

func TestLockCRDT_New(t *testing.T) {
	clk := newClock("n1")
	l := NewLockCRDT("mylock", "holder", "n1", time.Minute, clk)

	require.NotNil(t, l)
	assert.True(t, l.IsHeld())
	assert.True(t, l.IsHeldBy("holder"))
	assert.False(t, l.IsHeldBy("other"))
	assert.False(t, l.IsDeleted())
}

func TestLockCRDT_Release(t *testing.T) {
	clk := newClock("n1")
	l := NewLockCRDT("k", "holder", "n1", time.Minute, clk)

	l.Release("n1", clk)
	assert.True(t, l.IsDeleted())
	assert.False(t, l.IsHeld())
}

func TestLockCRDT_Expiry(t *testing.T) {
	clk := newClock("n1")
	l := NewLockCRDT("k", "holder", "n1", -time.Second, clk) // already expired TTL

	assert.True(t, l.IsDeleted(), "expired lock is treated as deleted")
	assert.False(t, l.IsHeld())
}

func TestLockCRDT_Merge_PrefersNonExpired(t *testing.T) {
	clk := newClock("n1")
	expired := NewLockCRDT("k", "old", "n1", -time.Second, clk)
	fresh := NewLockCRDT("k", "new", "n1", time.Minute, clk)

	result := expired.Merge(fresh).(*LockCRDT)
	assert.Equal(t, "new", result.Holder, "non-expired lock should win over expired")

	result2 := fresh.Merge(expired).(*LockCRDT)
	assert.Equal(t, "new", result2.Holder)
}

func TestLockCRDT_Merge_LWW_WhenBothValid(t *testing.T) {
	clkA := newClock("n1")
	clkB := newClock("n2")

	older := NewLockCRDT("k", "a", "n1", time.Minute, clkA)
	newer := NewLockCRDT("k", "b", "n2", time.Minute, clkB)

	result := older.Merge(newer).(*LockCRDT)
	assert.Equal(t, "b", result.Holder, "newer timestamp wins")
}

func TestLockCRDT_Merge_TypeMismatch(t *testing.T) {
	clk := newClock("n1")
	l := NewLockCRDT("k", "h", "n1", time.Minute, clk)
	s := NewSessionCRDT("s", "n1", clk)

	result := l.Merge(s)
	assert.Same(t, l, result.(*LockCRDT), "wrong type returns self")
}

// ─── RegisterCRDT ───────────────────────────────────────────────────────────

func TestRegisterCRDT_New(t *testing.T) {
	clk := newClock("n1")
	r := NewRegisterCRDT("hello", "n1", clk)

	assert.Equal(t, "hello", r.Value)
	assert.Equal(t, "n1", r.NodeID)
}

func TestRegisterCRDT_Merge_LWW(t *testing.T) {
	clkA := newClock("n1")
	older := NewRegisterCRDT("old", "n1", clkA)

	clkB := newClock("n2")
	newer := NewRegisterCRDT("new", "n2", clkB)

	result := older.Merge(newer)
	assert.Equal(t, "new", result.Value, "newer timestamp wins")

	result2 := newer.Merge(older)
	assert.Equal(t, "new", result2.Value, "merge is commutative")
}

func TestRegisterCRDT_Merge_TieBreak(t *testing.T) {
	ts := hlc.Timestamp{Physical: 1000, Logical: 0}
	a := &RegisterCRDT{Value: "a", NodeID: "node-a", Timestamp: ts}
	b := &RegisterCRDT{Value: "b", NodeID: "node-b", Timestamp: ts}

	result := a.Merge(b)
	assert.Equal(t, "b", result.Value, "larger node ID wins on tie")

	result2 := b.Merge(a)
	assert.Equal(t, "b", result2.Value, "deterministic")
}

func TestRegisterCRDT_Set(t *testing.T) {
	clk := newClock("n1")
	r := NewRegisterCRDT("initial", "n1", clk)
	r.Set("updated", "n1", clk)
	assert.Equal(t, "updated", r.Value)
}

// ─── StateSnapshot ──────────────────────────────────────────────────────────

func TestStateSnapshot_SerializeDeserialize(t *testing.T) {
	clk := newClock("n1")
	snap := NewStateSnapshot("n1", clk)

	session := NewSessionCRDT("s1", "n1", clk)
	session.Data = []byte("data")
	snap.Sessions["s1"] = session

	lock := NewLockCRDT("lock1", "holder", "n1", time.Minute, clk)
	snap.Locks["lock1"] = lock

	snap.Meta["key1"] = NewRegisterCRDT("val1", "n1", clk)

	data, err := snap.Serialize()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	parsed, err := DeserializeSnapshot(data)
	require.NoError(t, err)

	assert.Equal(t, "n1", parsed.NodeID)
	assert.Contains(t, parsed.Sessions, "s1")
	assert.Contains(t, parsed.Locks, "lock1")
	assert.Contains(t, parsed.Meta, "key1")
	assert.Equal(t, []byte("data"), parsed.Sessions["s1"].Data)
	assert.Equal(t, "val1", parsed.Meta["key1"].Value)
}

func TestDeserializeSnapshot_InvalidJSON(t *testing.T) {
	_, err := DeserializeSnapshot([]byte("not-json"))
	assert.Error(t, err)
}

// ─── Delta ──────────────────────────────────────────────────────────────────

func TestDelta_New(t *testing.T) {
	clk := newClock("n1")
	d := NewDelta("n1", clk)

	assert.Equal(t, "n1", d.NodeID)
	assert.NotNil(t, d.Sessions)
	assert.NotNil(t, d.Locks)
	assert.NotNil(t, d.Meta)
}
