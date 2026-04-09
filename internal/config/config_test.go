package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ─── ParseSocket ─────────────────────────────────────────────────────────────

func TestPHPFPMConfig_ParseSocket_UnixWithPrefix(t *testing.T) {
	cfg := PHPFPMConfig{Socket: "unix:///run/php-fpm.sock"}
	net, addr := cfg.ParseSocket()
	assert.Equal(t, "unix", net)
	assert.Equal(t, "/run/php-fpm.sock", addr)
}

func TestPHPFPMConfig_ParseSocket_TCPWithPrefix(t *testing.T) {
	cfg := PHPFPMConfig{Socket: "tcp://127.0.0.1:9000"}
	net, addr := cfg.ParseSocket()
	assert.Equal(t, "tcp", net)
	assert.Equal(t, "127.0.0.1:9000", addr)
}

func TestPHPFPMConfig_ParseSocket_BarePathDefaultsToUnix(t *testing.T) {
	cfg := PHPFPMConfig{Socket: "/tmp/fpm.sock"}
	net, addr := cfg.ParseSocket()
	assert.Equal(t, "unix", net)
	assert.Equal(t, "/tmp/fpm.sock", addr)
}

// ─── Load ────────────────────────────────────────────────────────────────────

func TestLoad_Defaults_WhenNoFileExists(t *testing.T) {
	logger := zap.NewNop()
	cfg, err := Load("/nonexistent/path.yaml", logger)
	require.NoError(t, err)

	assert.Equal(t, 8080, cfg.Server.HTTPPort)
	assert.Equal(t, "unix:///run/php-fpm.sock", cfg.PHPFPM.Socket)
	assert.Equal(t, "PHPSESSID", cfg.Session.CookieName)
	assert.Equal(t, "db", cfg.Session.Backend)
	assert.True(t, cfg.PHPFPM.Enabled)
	assert.True(t, cfg.Session.Enabled)
}

func TestLoad_FromYAMLFile(t *testing.T) {
	content := `
server:
  http_port: 9999
session:
  cookie_name: MySESSID
db:
  path: /tmp/testdb
`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.yaml")
	err := os.WriteFile(cfgPath, []byte(content), 0644)
	require.NoError(t, err)

	logger := zap.NewNop()
	cfg, err := Load(cfgPath, logger)
	require.NoError(t, err)

	assert.Equal(t, 9999, cfg.Server.HTTPPort)
	assert.Equal(t, "MySESSID", cfg.Session.CookieName)
	assert.Equal(t, "/tmp/testdb", cfg.DB.Path)
}

func TestLoad_InvalidYAML_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yaml")
	err := os.WriteFile(cfgPath, []byte("{ invalid yaml :::"), 0644)
	require.NoError(t, err)

	_, err = Load(cfgPath, zap.NewNop())
	assert.Error(t, err)
}

func TestLoad_InvalidBackend_ReturnsError(t *testing.T) {
	content := `
session:
  backend: redis
db:
  path: /tmp/testdb
`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0644))

	_, err := Load(cfgPath, zap.NewNop())
	assert.Error(t, err, "unsupported backend should fail validation")
}

func TestLoad_EmptyDBPath_ReturnsError(t *testing.T) {
	content := `
db:
  path: ""
`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0644))

	_, err := Load(cfgPath, zap.NewNop())
	assert.Error(t, err, "empty db path should fail validation")
}

// ─── setDefaults ─────────────────────────────────────────────────────────────

func TestSetDefaults(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)

	assert.Equal(t, 8080, cfg.Server.HTTPPort)
	assert.Equal(t, 7946, cfg.Server.GossipPort)
	assert.Equal(t, 9090, cfg.Server.MetricsPort)
	assert.True(t, cfg.PHPFPM.Enabled)
	assert.NotEmpty(t, cfg.StaticFiles.Extensions)
	assert.True(t, cfg.Metrics.Enabled)
	assert.True(t, cfg.Migration.Enabled)
}

// ─── mergeProfile ────────────────────────────────────────────────────────────

func TestMergeProfile_CookieName(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)

	profile := &AppProfile{
		Session: ProfileSessionConfig{CookieName: "WP_SESSION"},
	}
	mergeProfile(cfg, profile)

	assert.Equal(t, "WP_SESSION", cfg.Session.CookieName)
}

func TestMergeProfile_CookieName_NotOverwrittenWhenEmpty(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)
	original := cfg.Session.CookieName

	mergeProfile(cfg, &AppProfile{})

	assert.Equal(t, original, cfg.Session.CookieName, "empty profile should not overwrite cookie name")
}

func TestMergeProfile_PHPINIOverrides(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)
	cfg.PHPFPM.PHPINIOverrides = map[string]string{"a": "1"}

	profile := &AppProfile{
		PHPINIOverrides: map[string]string{"b": "2"},
		Session:         ProfileSessionConfig{PHPINI: map[string]string{"c": "3"}},
	}
	mergeProfile(cfg, profile)

	assert.Equal(t, "1", cfg.PHPFPM.PHPINIOverrides["a"])
	assert.Equal(t, "2", cfg.PHPFPM.PHPINIOverrides["b"])
	assert.Equal(t, "3", cfg.PHPFPM.PHPINIOverrides["c"])
}

func TestMergeProfile_StaticExtensions_NoDuplicates(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg) // includes .css

	profile := &AppProfile{
		StaticFiles: ProfileStaticConfig{
			AdditionalExtensions: []string{".css", ".pdf"}, // .css already exists
		},
	}
	mergeProfile(cfg, profile)

	count := 0
	for _, ext := range cfg.StaticFiles.Extensions {
		if ext == ".css" {
			count++
		}
	}
	assert.Equal(t, 1, count, ".css should appear exactly once after merge")
	assert.Contains(t, cfg.StaticFiles.Extensions, ".pdf")
}

func TestMergeProfile_MigrationCommand(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)

	profile := &AppProfile{
		Migration: ProfileMigrationConfig{
			Command: [][]string{{"php", "artisan", "migrate"}},
		},
	}
	mergeProfile(cfg, profile)

	assert.Equal(t, []string{"php", "artisan", "migrate"}, cfg.Migration.Command)
}

func TestMergeProfile_MigrationCommand_NotOverwrittenIfSet(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)
	cfg.Migration.Command = []string{"my", "custom", "migrate"}

	profile := &AppProfile{
		Migration: ProfileMigrationConfig{
			Command: [][]string{{"php", "artisan", "migrate"}},
		},
	}
	mergeProfile(cfg, profile)

	assert.Equal(t, []string{"my", "custom", "migrate"}, cfg.Migration.Command,
		"existing command should not be overwritten by profile")
}
