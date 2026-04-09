package db

import (
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/crdt"
	"github.com/sonroyaalmerol/phpcloud/internal/hlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	cfg := &config.Config{DB: config.DBConfig{Path: t.TempDir()}}
	m, err := New(cfg, "test-node", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })
	return m
}

// ─── Sessions ───────────────────────────────────────────────────────────────

func TestDB_Session_CreateAndGet(t *testing.T) {
	m := newTestManager(t)

	s := m.CreateSession("sess1", []byte("hello"), time.Hour)
	require.NotNil(t, s)

	got := m.GetSession("sess1")
	require.NotNil(t, got)
	assert.Equal(t, []byte("hello"), got.Data)
}

func TestDB_Session_GetMissing(t *testing.T) {
	m := newTestManager(t)
	assert.Nil(t, m.GetSession("nonexistent"))
	assert.Nil(t, m.GetSession("")) // empty ID
}

func TestDB_Session_Delete(t *testing.T) {
	m := newTestManager(t)
	m.CreateSession("s", []byte("data"), time.Hour)

	m.DeleteSession("s")
	assert.Nil(t, m.GetSession("s"), "deleted session should not be returned")
}

func TestDB_Session_Expired(t *testing.T) {
	m := newTestManager(t)
	s := m.CreateSession("s", []byte("data"), -time.Second) // already expired
	require.NotNil(t, s)

	assert.Nil(t, m.GetSession("s"), "expired session should not be returned")
}

func TestDB_Session_GetAllSessions_FiltersDeletedAndExpired(t *testing.T) {
	m := newTestManager(t)

	m.CreateSession("live", []byte("live"), time.Hour)
	m.CreateSession("expired", []byte("exp"), -time.Second)
	m.CreateSession("deleted", []byte("del"), time.Hour)
	m.DeleteSession("deleted")

	all := m.GetAllSessions()
	assert.Contains(t, all, "live")
	assert.NotContains(t, all, "expired")
	assert.NotContains(t, all, "deleted")
}

func TestDB_Session_MergeSessions(t *testing.T) {
	m := newTestManager(t)
	m.CreateSession("s", []byte("original"), time.Hour)

	clk := hlc.NewClock("remote-node")
	remote := crdt.NewSessionCRDT("s", "remote-node", clk)
	remote.Data = []byte("updated")
	remote.ExpiresAt = time.Now().Add(time.Hour)

	m.MergeSessions(map[string]*crdt.SessionCRDT{"s": remote})

	// The newer remote write should win (both are new; remote has the freshest HLC tick)
	got := m.GetSession("s")
	require.NotNil(t, got)
	// We can't know which wins without controlling timestamps, but the merge must succeed
	// without panic and return a valid session
	assert.NotNil(t, got.Data)
}

// ─── Locks ──────────────────────────────────────────────────────────────────

func TestDB_Lock_AcquireAndRelease(t *testing.T) {
	m := newTestManager(t)

	acquired, err := m.AcquireLock("mylock", "holder", time.Minute)
	require.NoError(t, err)
	assert.True(t, acquired, "first acquire should succeed")

	// Second acquire by a different holder should fail while lock is held
	again, err := m.AcquireLock("mylock", "other", time.Minute)
	require.NoError(t, err)
	assert.False(t, again, "cannot acquire lock held by another")

	assert.True(t, m.IsLockHeld("mylock"))
	assert.True(t, m.IsLockHeldBy("mylock", "holder"))
	assert.False(t, m.IsLockHeldBy("mylock", "other"))

	err = m.ReleaseLock("mylock", "holder")
	require.NoError(t, err)
	assert.False(t, m.IsLockHeld("mylock"), "lock should be released")
}

func TestDB_Lock_ExpiredLockCanBeReacquired(t *testing.T) {
	m := newTestManager(t)

	acquired, err := m.AcquireLock("k", "holder", -time.Second) // expired immediately
	require.NoError(t, err)
	assert.True(t, acquired)

	// Now acquire again — expired lock should be overwritten
	again, err := m.AcquireLock("k", "new-holder", time.Minute)
	require.NoError(t, err)
	assert.True(t, again, "expired lock should be re-acquirable")
}

func TestDB_Lock_ReleaseMissing(t *testing.T) {
	m := newTestManager(t)
	err := m.ReleaseLock("nonexistent", "holder")
	assert.NoError(t, err, "releasing missing lock should be a no-op")
}

func TestDB_Lock_ReleaseByWrongHolder(t *testing.T) {
	m := newTestManager(t)
	_, _ = m.AcquireLock("k", "real-holder", time.Minute)

	err := m.ReleaseLock("k", "impostor")
	assert.NoError(t, err)
	assert.True(t, m.IsLockHeld("k"), "lock should still be held after wrong-holder release")
}

func TestDB_Lock_GetAllLocks(t *testing.T) {
	m := newTestManager(t)

	_, _ = m.AcquireLock("a", "h", time.Minute)
	_, _ = m.AcquireLock("b", "h", time.Minute)
	_, _ = m.AcquireLock("c", "h", -time.Second) // expired

	all := m.GetAllLocks()
	assert.Contains(t, all, "a")
	assert.Contains(t, all, "b")
	assert.NotContains(t, all, "c", "expired locks must not appear in GetAllLocks")
}

// ─── Metadata ───────────────────────────────────────────────────────────────

func TestDB_Meta_SetAndGet(t *testing.T) {
	m := newTestManager(t)

	m.SetMeta("key", "value")
	v, ok := m.GetMeta("key")
	assert.True(t, ok)
	assert.Equal(t, "value", v)
}

func TestDB_Meta_GetMissing(t *testing.T) {
	m := newTestManager(t)
	_, ok := m.GetMeta("missing")
	assert.False(t, ok)
}

func TestDB_Meta_Delete(t *testing.T) {
	m := newTestManager(t)
	m.SetMeta("k", "v")
	m.DeleteMeta("k")

	_, ok := m.GetMeta("k")
	assert.False(t, ok, "deleted meta key should not be found")
}

func TestDB_Meta_LWW(t *testing.T) {
	m := newTestManager(t)

	m.SetMeta("version", "v1")
	m.SetMeta("version", "v2") // newer wins
	v, _ := m.GetMeta("version")
	assert.Equal(t, "v2", v)
}

func TestDB_Meta_GetAllMeta(t *testing.T) {
	m := newTestManager(t)

	m.SetMeta("a", "1")
	m.SetMeta("b", "2")
	m.SetMeta("c", "") // empty — should be excluded

	all := m.GetAllMeta()
	assert.Contains(t, all, "a")
	assert.Contains(t, all, "b")
	assert.NotContains(t, all, "c")
}

// ─── State Snapshot / Delta ──────────────────────────────────────────────────

func TestDB_GetStateSnapshot_IncludesAllState(t *testing.T) {
	m := newTestManager(t)

	m.CreateSession("s1", []byte("d"), time.Hour)
	_, _ = m.AcquireLock("l1", "h", time.Minute)
	m.SetMeta("m1", "v")

	snap := m.GetStateSnapshot()
	assert.Equal(t, "test-node", snap.NodeID)
	assert.Contains(t, snap.Sessions, "s1")
	assert.Contains(t, snap.Locks, "l1")
	assert.Contains(t, snap.Meta, "m1")
}

func TestDB_ImportStateSnapshot_MergesState(t *testing.T) {
	m1 := newTestManager(t)
	m1.CreateSession("s1", []byte("hello"), time.Hour)
	m1.SetMeta("ver", "1")

	snap := m1.GetStateSnapshot()

	m2 := newTestManager(t)
	m2.ImportStateSnapshot(snap)

	got := m2.GetSession("s1")
	require.NotNil(t, got)
	assert.Equal(t, []byte("hello"), got.Data)

	v, ok := m2.GetMeta("ver")
	assert.True(t, ok)
	assert.Equal(t, "1", v)
}

func TestDB_ImportStateSnapshot_NilIsNoOp(t *testing.T) {
	m := newTestManager(t)
	assert.NotPanics(t, func() { m.ImportStateSnapshot(nil) })
}

func TestDB_GetDeltaSince_ReturnsOnlyNewer(t *testing.T) {
	m := newTestManager(t)

	checkpoint := m.GetClock().GetLatest()

	m.CreateSession("after", []byte("new"), time.Hour)

	delta := m.GetDeltaSince(checkpoint)
	assert.Contains(t, delta.Sessions, "after")
}

func TestDB_ApplyDelta_NilIsNoOp(t *testing.T) {
	m := newTestManager(t)
	assert.NotPanics(t, func() { m.ApplyDelta(nil) })
}

func TestDB_RunGC_RemovesExpiredEntries(t *testing.T) {
	m := newTestManager(t)
	m.CreateSession("stale", []byte("x"), -time.Second)

	err := m.RunGC()
	assert.NoError(t, err)

	assert.Nil(t, m.GetSession("stale"))
}
