package migration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/db"
	"go.uber.org/zap"
)

// Migrator handles application migrations
type Migrator struct {
	config  *config.Config
	db      *db.Manager
	logger  *zap.Logger
	lockKey string
	holder  string
}

// New creates a new migrator
func New(cfg *config.Config, dbManager *db.Manager, logger *zap.Logger) *Migrator {
	holder, _ := os.Hostname()
	if holder == "" {
		holder = fmt.Sprintf("phpcloud-%d", time.Now().Unix())
	}

	return &Migrator{
		config:  cfg,
		db:      dbManager,
		logger:  logger,
		lockKey: cfg.Migration.LockKey,
		holder:  holder,
	}
}

// AcquireLock attempts to acquire the migration lock
func (m *Migrator) AcquireLock(ctx context.Context) (bool, error) {
	return m.db.AcquireLock(m.lockKey, m.holder, m.config.Migration.LockTimeout)
}

// ReleaseLock releases the migration lock
func (m *Migrator) ReleaseLock() error {
	return m.db.ReleaseLock(m.lockKey, m.holder)
}

// Run runs the migration if needed
func (m *Migrator) Run(ctx context.Context) error {
	// Check if migration is needed
	needsMigration, err := m.needsMigration()
	if err != nil {
		return fmt.Errorf("failed to check migration status: %w", err)
	}

	if !needsMigration {
		m.logger.Info("No migration needed")
		return nil
	}

	m.logger.Info("Migration needed, running...")

	// Run pre-hooks
	if err := m.runPreHooks(ctx); err != nil {
		return fmt.Errorf("pre-hooks failed: %w", err)
	}

	// Run migration command
	if err := m.runMigrationCommand(ctx); err != nil {
		return fmt.Errorf("migration command failed: %w", err)
	}

	// Run post-hooks
	if err := m.runPostHooks(ctx); err != nil {
		return fmt.Errorf("post-hooks failed: %w", err)
	}

	// Update version in meta
	if err := m.updateVersion(); err != nil {
		return fmt.Errorf("failed to update version: %w", err)
	}

	m.logger.Info("Migration completed successfully")
	return nil
}

// WaitForCompletion waits for migration lock to be released
func (m *Migrator) WaitForCompletion(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Check if lock is still held
			if !m.db.IsLockHeld(m.lockKey) {
				m.logger.Info("Migration lock released, migration complete")
				return nil
			}
			m.logger.Info("Waiting for migration to complete...")
		}
	}
}

// needsMigration checks if migration is needed
func (m *Migrator) needsMigration() (bool, error) {
	// If no version detection is configured, assume migration is needed
	if m.config.Migration.VersionFile == "" && m.config.Migration.VersionQuery == "" {
		return true, nil
	}

	// Get current version from file
	var currentVersion string
	if m.config.Migration.VersionFile != "" {
		data, err := os.ReadFile(m.config.Migration.VersionFile)
		if err != nil {
			return false, fmt.Errorf("failed to read version file: %w", err)
		}
		currentVersion = string(data)
	}

	// Get stored version from CRDT metadata
	storedVersion, _ := m.db.GetMeta("app_version")

	return currentVersion != storedVersion, nil
}

// updateVersion stores the current version in CRDT metadata
func (m *Migrator) updateVersion() error {
	if m.config.Migration.VersionFile == "" {
		return nil
	}

	data, err := os.ReadFile(m.config.Migration.VersionFile)
	if err != nil {
		return fmt.Errorf("failed to read version file: %w", err)
	}

	m.db.SetMeta("app_version", string(data))
	return nil
}

// runPreHooks runs pre-migration hooks
func (m *Migrator) runPreHooks(ctx context.Context) error {
	if m.config.Profile == nil || len(m.config.Profile.Migration.PreHooks) == 0 {
		return nil
	}

	for i, hook := range m.config.Profile.Migration.PreHooks {
		if len(hook) == 0 {
			continue
		}

		m.logger.Info("Running pre-hook", zap.Int("index", i), zap.Strings("command", hook))

		cmd := exec.CommandContext(ctx, hook[0], hook[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("pre-hook %d failed: %w\nOutput: %s", i, err, string(output))
		}
	}

	return nil
}

// runMigrationCommand runs the main migration command
func (m *Migrator) runMigrationCommand(ctx context.Context) error {
	command := m.config.Migration.Command
	if len(command) == 0 && m.config.Profile != nil && len(m.config.Profile.Migration.Command) > 0 {
		command = m.config.Profile.Migration.Command[0]
	}

	if len(command) == 0 {
		m.logger.Info("No migration command configured, skipping")
		return nil
	}

	m.logger.Info("Running migration command", zap.Strings("command", command))

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("migration command failed: %w", err)
	}

	return nil
}

// runPostHooks runs post-migration hooks
func (m *Migrator) runPostHooks(ctx context.Context) error {
	if m.config.Profile == nil || len(m.config.Profile.Migration.PostHooks) == 0 {
		return nil
	}

	for i, hook := range m.config.Profile.Migration.PostHooks {
		if len(hook) == 0 {
			continue
		}

		m.logger.Info("Running post-hook", zap.Int("index", i), zap.Strings("command", hook))

		cmd := exec.CommandContext(ctx, hook[0], hook[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("post-hook %d failed: %w\nOutput: %s", i, err, string(output))
		}
	}

	return nil
}
