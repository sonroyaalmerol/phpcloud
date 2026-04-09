// Package testhelpers provides utilities for testing
package testhelpers

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap"
)

// NewTestConfig creates a test configuration.
// Ports are NOT randomised here — callers that need isolation should use
// FreePort() to override Server.HTTPPort, Server.MetricsPort, and
// Server.GossipPort before starting an engine.
func NewTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Server: config.ServerConfig{
			HTTPPort:     18080,
			GossipPort:   17946,
			MetricsPort:  19090,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 120 * time.Second,
		},
		PHPFPM: config.PHPFPMConfig{
			Enabled:     false, // no real PHP-FPM in tests
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

// NewTestLogger creates a test logger that writes to t.Log.
func NewTestLogger(t *testing.T) *zap.Logger {
	return zaptest.NewLogger(t)
}

// FreePort returns a free TCP port on localhost.
func FreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// WaitForCondition polls condition every 10ms until it returns true or timeout
// is reached, at which point the test is failed with msg.
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Timeout waiting for: %s", msg)
}

// TempFile creates a temporary file with the given content and registers
// cleanup with t.
func TempFile(t *testing.T, pattern string, content string) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", pattern)
	require.NoError(t, err)

	_, err = tmpFile.WriteString(content)
	require.NoError(t, err)

	err = tmpFile.Close()
	require.NoError(t, err)

	t.Cleanup(func() { os.Remove(tmpFile.Name()) })
	return tmpFile.Name()
}

// TempDir creates a temporary directory and registers cleanup with t.
func TempDir(t *testing.T, pattern string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", pattern)
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
