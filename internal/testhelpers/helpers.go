// Package testhelpers provides utilities for testing
package testhelpers

import (
	"os"
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// NewTestConfig creates a test configuration
func NewTestConfig(t *testing.T) *config.Config {
	return &config.Config{
		AppProfile: "generic",
		Server: config.ServerConfig{
			HTTPPort:     18080,
			GossipPort:   17946,
			MetricsPort:  19090,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 120 * time.Second,
		},
		PHPFPM: config.PHPFPMConfig{
			Socket:      "unix:///tmp/test-php-fpm.sock",
			Binary:      "php-fpm",
			Config:      "/tmp/test-php-fpm.conf",
			PoolSizeMin: 2,
			PoolSizeMax: 5,
		},
		Session: config.SessionConfig{
			Enabled:     true,
			Backend:     "db",
			CookieName:  "PHPSESSID",
			TTL:         time.Hour,
			LockTimeout: 30 * time.Second,
		},
		Cluster: config.ClusterConfig{
			Enabled:   false,
			Discovery: "static",
			NodeName:  "test-node",
		},
		DB: config.DBConfig{
			Path: "/tmp/phpcloud_test_db",
		},
		Migration: config.MigrationConfig{
			Enabled:     true,
			LockKey:     "test:migration",
			LockTimeout: 5 * time.Minute,
			QueueSize:   1000,
		},
		Cron: config.CronConfig{
			Enabled:    false,
			LeaderOnly: true,
			Jobs:       []config.CronJob{},
		},
		StaticFiles: config.StaticFilesConfig{
			Enabled:    true,
			Root:       "/tmp/test-www",
			Extensions: []string{".css", ".js", ".png"},
		},
		Logging: config.LoggingConfig{
			Level:  "debug",
			Format: "json",
		},
		Metrics: config.MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
	}
}

// NewTestLogger creates a test logger
func NewTestLogger(t *testing.T) *zap.Logger {
	return zaptest.NewLogger(t)
}

// WaitForCondition waits for a condition to be true or timeout
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, msg string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Timeout waiting for condition: %s", msg)
}

// TempFile creates a temporary file with the given content
func TempFile(t *testing.T, pattern string, content string) string {
	tmpFile, err := os.CreateTemp("", pattern)
	require.NoError(t, err)

	_, err = tmpFile.WriteString(content)
	require.NoError(t, err)

	err = tmpFile.Close()
	require.NoError(t, err)

	t.Cleanup(func() {
		os.Remove(tmpFile.Name())
	})

	return tmpFile.Name()
}

// TempDir creates a temporary directory
func TempDir(t *testing.T, pattern string) string {
	dir, err := os.MkdirTemp("", pattern)
	require.NoError(t, err)

	t.Cleanup(func() {
		os.RemoveAll(dir)
	})

	return dir
}

// SkipIfNoPostgres skips the test if PostgreSQL is not available
func SkipIfNoPostgres(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" && os.Getenv("CI") == "" {
		t.Skip("Skipping test: No PostgreSQL connection available. Set TEST_POSTGRES_DSN to run.")
	}
}

// SkipIfNoMySQL skips the test if MySQL is not available
func SkipIfNoMySQL(t *testing.T) {
	if os.Getenv("TEST_MYSQL_DSN") == "" && os.Getenv("CI") == "" {
		t.Skip("Skipping test: No MySQL connection available. Set TEST_MYSQL_DSN to run.")
	}
}
