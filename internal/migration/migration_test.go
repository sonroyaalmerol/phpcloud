package migration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestMigrator(t *testing.T, cfg *config.Config) (*Migrator, *db.Manager) {
	t.Helper()
	dbMgr, err := db.New(cfg, "test-node", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { dbMgr.Close() })

	m := New(cfg, dbMgr, zap.NewNop())
	m.holder = "test-holder" // deterministic holder for tests
	return m, dbMgr
}

func baseCfg(t *testing.T) *config.Config {
	return &config.Config{
		DB: config.DBConfig{Path: t.TempDir()},
		Migration: config.MigrationConfig{
			Enabled:     true,
			LockKey:     "test:migration",
			LockTimeout: time.Minute,
			QueueSize:   100,
		},
	}
}

// ─── Lock operations ─────────────────────────────────────────────────────────

func TestMigrator_AcquireAndReleaseLock(t *testing.T) {
	m, _ := newTestMigrator(t, baseCfg(t))
	ctx := context.Background()

	acquired, err := m.AcquireLock(ctx)
	require.NoError(t, err)
	assert.True(t, acquired)

	err = m.ReleaseLock()
	require.NoError(t, err)
}

func TestMigrator_AcquireLock_FailsWhenHeld(t *testing.T) {
	cfg := baseCfg(t)
	m1, dbMgr := newTestMigrator(t, cfg)
	m2 := New(cfg, dbMgr, zap.NewNop())
	m2.holder = "other-holder"

	ctx := context.Background()

	acquired, err := m1.AcquireLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Different holder cannot acquire while m1 holds the lock
	again, err := m2.AcquireLock(ctx)
	require.NoError(t, err)
	assert.False(t, again, "second migrator must not acquire held lock")
}

// ─── needsMigration ──────────────────────────────────────────────────────────

func TestMigrator_NeedsMigration_NoVersionConfig(t *testing.T) {
	// No version file or query configured → always needs migration
	m, _ := newTestMigrator(t, baseCfg(t))
	needs, err := m.needsMigration()
	require.NoError(t, err)
	assert.True(t, needs)
}

func TestMigrator_NeedsMigration_VersionFileMismatch(t *testing.T) {
	cfg := baseCfg(t)
	vf := filepath.Join(t.TempDir(), "version.txt")
	require.NoError(t, os.WriteFile(vf, []byte("v2.0"), 0644))
	cfg.Migration.VersionFile = vf

	m, dbMgr := newTestMigrator(t, cfg)

	// Stored version is "v1.0" (different from file "v2.0")
	dbMgr.SetMeta("app_version", "v1.0")

	needs, err := m.needsMigration()
	require.NoError(t, err)
	assert.True(t, needs)
}

func TestMigrator_NeedsMigration_VersionFileMatch(t *testing.T) {
	cfg := baseCfg(t)
	vf := filepath.Join(t.TempDir(), "version.txt")
	require.NoError(t, os.WriteFile(vf, []byte("v1.0"), 0644))
	cfg.Migration.VersionFile = vf

	m, dbMgr := newTestMigrator(t, cfg)
	dbMgr.SetMeta("app_version", "v1.0")

	needs, err := m.needsMigration()
	require.NoError(t, err)
	assert.False(t, needs, "matching versions should not require migration")
}

func TestMigrator_NeedsMigration_VersionFileMissing_ReturnsError(t *testing.T) {
	cfg := baseCfg(t)
	cfg.Migration.VersionFile = "/nonexistent/version.txt"

	m, _ := newTestMigrator(t, cfg)
	_, err := m.needsMigration()
	assert.Error(t, err)
}

// ─── Run ─────────────────────────────────────────────────────────────────────

func TestMigrator_Run_NoMigrationNeeded(t *testing.T) {
	cfg := baseCfg(t)
	vf := filepath.Join(t.TempDir(), "version.txt")
	require.NoError(t, os.WriteFile(vf, []byte("v1"), 0644))
	cfg.Migration.VersionFile = vf

	m, dbMgr := newTestMigrator(t, cfg)
	dbMgr.SetMeta("app_version", "v1")

	err := m.Run(context.Background())
	assert.NoError(t, err)
}

func TestMigrator_Run_ExecutesMigrationCommand(t *testing.T) {
	cfg := baseCfg(t)
	cfg.Migration.Command = []string{"true"} // succeeds instantly

	m, _ := newTestMigrator(t, cfg)

	err := m.Run(context.Background())
	assert.NoError(t, err)
}

func TestMigrator_Run_FailsOnBadCommand(t *testing.T) {
	cfg := baseCfg(t)
	cfg.Migration.Command = []string{"false"} // always exits with non-zero

	m, _ := newTestMigrator(t, cfg)

	err := m.Run(context.Background())
	assert.Error(t, err)
}

// ─── WaitForCompletion ───────────────────────────────────────────────────────

func TestMigrator_WaitForCompletion_ReturnsWhenLockReleased(t *testing.T) {
	cfg := baseCfg(t)
	m, dbMgr := newTestMigrator(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Acquire lock, then release it shortly after
	acquired, err := m.AcquireLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = m.ReleaseLock()
	}()

	_ = dbMgr // suppress unused warning
	err = m.WaitForCompletion(ctx)
	assert.NoError(t, err)
}

func TestMigrator_WaitForCompletion_RespectsContextCancellation(t *testing.T) {
	cfg := baseCfg(t)
	m, _ := newTestMigrator(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Acquire lock so WaitForCompletion will block
	acquired, err := m.AcquireLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Cancel context shortly after
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	err = m.WaitForCompletion(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}
