package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	cfg := &config.Config{
		DB: config.DBConfig{Path: t.TempDir()},
		Session: config.SessionConfig{
			Enabled:     true,
			CookieName:  "PHPSESSID",
			TTL:         time.Hour,
			LockTimeout: 30 * time.Second,
		},
	}
	dbMgr, err := db.New(cfg, "test-node", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { dbMgr.Close() })

	mgr, err := New(cfg, dbMgr, zap.NewNop())
	require.NoError(t, err)
	return mgr
}

func TestSession_SaveAndGet(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	sess := &Session{
		ID:      "test-session",
		Data:    []byte("some data"),
		Expires: time.Now().Add(time.Hour),
	}

	err := m.SaveSession(ctx, sess)
	require.NoError(t, err)

	got, err := m.GetSession(ctx, "test-session")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []byte("some data"), got.Data)
}

func TestSession_GetMissing_ReturnsNil(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	got, err := m.GetSession(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSession_GetEmptyID_ReturnsNil(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	got, err := m.GetSession(ctx, "")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSession_Delete(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	sess := &Session{ID: "s", Data: []byte("x"), Expires: time.Now().Add(time.Hour)}
	require.NoError(t, m.SaveSession(ctx, sess))

	require.NoError(t, m.DeleteSession(ctx, "s"))

	got, err := m.GetSession(ctx, "s")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSession_SaveWithZeroExpiry_UsesDefaultTTL(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	sess := &Session{ID: "s", Data: []byte("x")} // zero Expires
	require.NoError(t, m.SaveSession(ctx, sess))

	got, err := m.GetSession(ctx, "s")
	require.NoError(t, err)
	require.NotNil(t, got, "session with zero Expires should use default TTL and not be immediately expired")
}

func TestSession_Lock_Unlock(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	sess := &Session{ID: "s", Data: []byte("x"), Expires: time.Now().Add(time.Hour)}
	require.NoError(t, m.SaveSession(ctx, sess))

	require.NoError(t, m.LockSession(ctx, "s", "holder-1"))
	require.NoError(t, m.UnlockSession(ctx, "s"))
}

func TestSession_LockMissing_CreatesSession(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	// Locking a non-existent session should create it
	err := m.LockSession(ctx, "new-session", "holder")
	require.NoError(t, err)

	got, err := m.GetSession(ctx, "new-session")
	require.NoError(t, err)
	assert.NotNil(t, got)
}

func TestSession_ExtractSessionID_FromCookie(t *testing.T) {
	m := newTestManager(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "PHPSESSID", Value: "abc123"})

	id := m.ExtractSessionID(req)
	assert.Equal(t, "abc123", id)
}

func TestSession_ExtractSessionID_NoCookie_ReturnsEmpty(t *testing.T) {
	m := newTestManager(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Empty(t, m.ExtractSessionID(req))
}

func TestSession_SetCookie(t *testing.T) {
	m := newTestManager(t)

	w := httptest.NewRecorder()
	m.SetSessionCookie(w, "sess-id", time.Now().Add(time.Hour))

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "PHPSESSID", cookies[0].Name)
	assert.Equal(t, "sess-id", cookies[0].Value)
}

func TestSession_ClearCookie(t *testing.T) {
	m := newTestManager(t)

	w := httptest.NewRecorder()
	m.ClearSessionCookie(w)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, -1, cookies[0].MaxAge)
}

func TestSession_StartStop(t *testing.T) {
	m := newTestManager(t)
	assert.NoError(t, m.Start())
	m.Stop()
}
