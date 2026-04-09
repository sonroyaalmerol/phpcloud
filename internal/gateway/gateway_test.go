package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	return &config.Config{
		Server: config.ServerConfig{
			HTTPPort:     18080,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		PHPFPM: config.PHPFPMConfig{
			Enabled: false, // no real FPM in tests
			Socket:  "unix:///tmp/test-fpm.sock",
		},
		Session: config.SessionConfig{
			Enabled:    true,
			CookieName: "PHPSESSID",
		},
		StaticFiles: config.StaticFilesConfig{
			Enabled:    true,
			Root:       root,
			Extensions: []string{".css", ".js", ".png"},
		},
	}
}

// ─── Construction ────────────────────────────────────────────────────────────

func TestNew_WithFPMDisabled(t *testing.T) {
	cfg := newTestConfig(t)
	s, err := New(cfg, nil, nil, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Nil(t, s.phpHandler, "phpHandler must be nil when FPM is disabled")
}

// ─── isStaticFile ────────────────────────────────────────────────────────────

func TestIsStaticFile_MatchingExtension(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	assert.True(t, s.isStaticFile("/style.css"))
	assert.True(t, s.isStaticFile("/app.js"))
	assert.True(t, s.isStaticFile("/logo.png"))
}

func TestIsStaticFile_NonMatchingExtension(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	assert.False(t, s.isStaticFile("/index.php"))
	assert.False(t, s.isStaticFile("/api/data"))
	assert.False(t, s.isStaticFile("/"))
}

func TestIsStaticFile_DisabledStaticFiles(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.StaticFiles.Enabled = false
	s, _ := New(cfg, nil, nil, zap.NewNop())

	assert.False(t, s.isStaticFile("/style.css"), "static files disabled — must return false")
}

// ─── serveStatic ─────────────────────────────────────────────────────────────

func TestServeStatic_ExistingFile(t *testing.T) {
	cfg := newTestConfig(t)
	cssContent := "body { color: red; }"
	require.NoError(t, os.WriteFile(filepath.Join(cfg.StaticFiles.Root, "test.css"), []byte(cssContent), 0644))

	s, _ := New(cfg, nil, nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/test.css", nil)
	rec := httptest.NewRecorder()
	s.serveStatic(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "body { color: red; }")
}

func TestServeStatic_MissingFile_Returns404(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/nonexistent.css", nil)
	rec := httptest.NewRecorder()
	s.serveStatic(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeStatic_DirectoryTraversal_Returns403(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	// Attempt directory traversal
	req := httptest.NewRequest(http.MethodGet, "/../../../etc/passwd", nil)
	rec := httptest.NewRecorder()
	s.serveStatic(rec, req)

	// Either 403 or 404 is acceptable — must not serve the file
	assert.True(t, rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound,
		"traversal attempt must be blocked, got %d", rec.Code)
}

func TestServeStatic_Directory_Returns404(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	// Request the root directory itself
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.serveStatic(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ─── buildFastCGIParams ──────────────────────────────────────────────────────

func TestBuildFastCGIParams_StandardFields(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/index.php?foo=bar", nil)
	req.Header.Set("Content-Type", "application/json")

	params := s.buildFastCGIParams(req)

	assert.Equal(t, "POST", params["REQUEST_METHOD"])
	assert.Equal(t, "/index.php?foo=bar", params["REQUEST_URI"])
	assert.Equal(t, "foo=bar", params["QUERY_STRING"])
	assert.Equal(t, "application/json", params["CONTENT_TYPE"])
	assert.Equal(t, cfg.StaticFiles.Root, params["DOCUMENT_ROOT"])
	assert.Equal(t, filepath.Join(cfg.StaticFiles.Root, "/index.php"), params["SCRIPT_FILENAME"])
}

func TestBuildFastCGIParams_HTTPHeadersExposed(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom-Header", "hello")

	params := s.buildFastCGIParams(req)

	assert.Equal(t, "hello", params["HTTP_X_CUSTOM_HEADER"])
}

// ─── buildPHPValue ───────────────────────────────────────────────────────────

func TestBuildPHPValue_SessionEnabled(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	val := s.buildPHPValue()
	assert.Contains(t, val, "session.save_handler=user")
	assert.Contains(t, val, "session.name=PHPSESSID")
}

func TestBuildPHPValue_SessionDisabled(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Session.Enabled = false
	s, _ := New(cfg, nil, nil, zap.NewNop())

	val := s.buildPHPValue()
	assert.NotContains(t, val, "session.save_handler")
}

func TestBuildPHPValue_PHPINIOverrides(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.PHPFPM.PHPINIOverrides = map[string]string{"memory_limit": "256M"}
	s, _ := New(cfg, nil, nil, zap.NewNop())

	val := s.buildPHPValue()
	assert.Contains(t, val, "memory_limit=256M")
}

// ─── Internal routing ────────────────────────────────────────────────────────

func TestCreateHandler_InternalEndpoints(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	s.RegisterInternalHandler("/phpcloud/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	handler := s.createHandler()
	req := httptest.NewRequest(http.MethodGet, "/phpcloud/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTeapot, rec.Code)
}

// ─── generateRequestID ──────────────────────────────────────────────────────

func TestGenerateRequestID_Unique(t *testing.T) {
	ids := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id := generateRequestID()
		assert.NotEmpty(t, id)
		ids[id] = struct{}{}
	}
	// Most IDs should be unique (time + PID; may collide within same nanosecond)
	assert.Greater(t, len(ids), 1)
}

// ─── addRequestID ────────────────────────────────────────────────────────────

func TestAddRequestID_PreservesExisting(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "existing-id")

	updated := s.addRequestID(req)
	assert.Equal(t, "existing-id", updated.Header.Get("X-Request-ID"))
}

func TestAddRequestID_GeneratesWhenMissing(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	updated := s.addRequestID(req)
	assert.NotEmpty(t, updated.Header.Get("X-Request-ID"))
}

// ─── servePHP ────────────────────────────────────────────────────────────────

func TestServePHP_NoHandler_Returns503(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())
	// phpHandler is nil because PHPFPM.Enabled is false

	req := httptest.NewRequest(http.MethodGet, "/index.php", nil)
	rec := httptest.NewRecorder()
	s.servePHP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// ─── Stop ────────────────────────────────────────────────────────────────────

func TestStop_WhenNeverStarted(t *testing.T) {
	cfg := newTestConfig(t)
	s, _ := New(cfg, nil, nil, zap.NewNop())
	assert.NoError(t, s.Stop(), "Stop before Start must not error")
}
