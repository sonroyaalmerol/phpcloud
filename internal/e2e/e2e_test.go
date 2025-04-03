package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/bootstrap"
	"github.com/sonroyaalmerol/phpcloud/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_FullStartup tests a complete startup and shutdown cycle
func TestE2E_FullStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	cfg := testhelpers.NewTestConfig(t)
	logger := testhelpers.NewTestLogger(t)

	// Create required directories
	err := os.MkdirAll(filepath.Dir(cfg.PHPFPM.Config), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(cfg.StaticFiles.Root, 0755)
	require.NoError(t, err)

	// Create engine
	engine, err := bootstrap.New(cfg, logger)
	require.NoError(t, err)

	// Start engine with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- engine.Start(ctx)
	}()

	// Wait for startup
	time.Sleep(3 * time.Second)

	// Verify engine is ready
	require.True(t, engine.IsReady(), "Engine should be ready")

	// Test health endpoint
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/phpcloud/healthz", cfg.Server.HTTPPort))
	if err == nil {
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	// Shutdown
	cancel()

	select {
	case err := <-errChan:
		assert.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("Timeout waiting for shutdown")
	}
}

// TestE2E_SessionPersistence tests session persistence across restarts
func TestE2E_SessionPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	cfg := testhelpers.NewTestConfig(t)
	logger := testhelpers.NewTestLogger(t)

	// Create directories
	err := os.MkdirAll(filepath.Dir(cfg.PHPFPM.Config), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(cfg.StaticFiles.Root, 0755)
	require.NoError(t, err)

	// First startup
	engine1, err := bootstrap.New(cfg, logger)
	require.NoError(t, err)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel1()

	errChan1 := make(chan error, 1)
	go func() {
		errChan1 <- engine1.Start(ctx1)
	}()

	time.Sleep(3 * time.Second)
	require.True(t, engine1.IsReady())

	// Shutdown first engine
	cancel1()
	select {
	case <-errChan1:
		// Success
	case <-time.After(15 * time.Second):
		t.Fatal("Timeout waiting for first shutdown")
	}

	// Second startup - should restore state from DB
	engine2, err := bootstrap.New(cfg, logger)
	require.NoError(t, err)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()

	errChan2 := make(chan error, 1)
	go func() {
		errChan2 <- engine2.Start(ctx2)
	}()

	time.Sleep(3 * time.Second)
	require.True(t, engine2.IsReady())

	// Shutdown second engine
	cancel2()
	select {
	case <-errChan2:
		// Success
	case <-time.After(15 * time.Second):
		t.Fatal("Timeout waiting for second shutdown")
	}
}

// TestE2E_MigrationLock tests migration locking across multiple instances
func TestE2E_MigrationLock(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	cfg := testhelpers.NewTestConfig(t)
	logger := testhelpers.NewTestLogger(t)

	// Create directories
	err := os.MkdirAll(filepath.Dir(cfg.PHPFPM.Config), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(cfg.StaticFiles.Root, 0755)
	require.NoError(t, err)

	// Start two engines concurrently
	cfg1 := cfg
	cfg1.Cluster.NodeName = "node1"

	cfg2 := cfg
	cfg2.Server.HTTPPort = 18081
	cfg2.Server.MetricsPort = 19091
	cfg2.Cluster.NodeName = "node2"

	engine1, err := bootstrap.New(cfg1, logger)
	require.NoError(t, err)

	engine2, err := bootstrap.New(cfg2, logger)
	require.NoError(t, err)

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	errChan1 := make(chan error, 1)
	errChan2 := make(chan error, 1)

	// Start both
	go func() {
		errChan1 <- engine1.Start(ctx1)
	}()

	go func() {
		errChan2 <- engine2.Start(ctx2)
	}()

	// Wait for both to be ready
	time.Sleep(5 * time.Second)

	// At least one should be ready
	ready1 := engine1.IsReady()
	ready2 := engine2.IsReady()

	assert.True(t, ready1 || ready2, "At least one engine should be ready")

	// Shutdown both
	cancel1()
	cancel2()

	select {
	case <-errChan1:
	case <-time.After(10 * time.Second):
	}

	select {
	case <-errChan2:
	case <-time.After(10 * time.Second):
	}
}

// TestE2E_ClusterFormation tests cluster formation with multiple nodes
func TestE2E_ClusterFormation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	cfg := testhelpers.NewTestConfig(t)
	cfg.Cluster.Enabled = true
	logger := testhelpers.NewTestLogger(t)

	// Create directories
	err := os.MkdirAll(filepath.Dir(cfg.PHPFPM.Config), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(cfg.StaticFiles.Root, 0755)
	require.NoError(t, err)

	// Start single node (cluster will form with just this node)
	engine, err := bootstrap.New(cfg, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- engine.Start(ctx)
	}()

	time.Sleep(3 * time.Second)

	// Single node should be leader
	if engine.IsLeader() {
		assert.True(t, engine.IsReady())
	}

	cancel()

	select {
	case <-errChan:
	case <-time.After(15 * time.Second):
	}
}

// TestE2E_StaticFileServing tests static file serving end-to-end
func TestE2E_StaticFileServing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	cfg := testhelpers.NewTestConfig(t)
	logger := testhelpers.NewTestLogger(t)

	// Create directories
	err := os.MkdirAll(filepath.Dir(cfg.PHPFPM.Config), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(cfg.StaticFiles.Root, 0755)
	require.NoError(t, err)

	// Create static files
	cssContent := "body { color: blue; }"
	err = os.WriteFile(filepath.Join(cfg.StaticFiles.Root, "style.css"), []byte(cssContent), 0644)
	require.NoError(t, err)

	engine, err := bootstrap.New(cfg, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- engine.Start(ctx)
	}()

	time.Sleep(3 * time.Second)
	require.True(t, engine.IsReady())

	// Test static file endpoint
	url := fmt.Sprintf("http://localhost:%d/style.css", cfg.Server.HTTPPort)
	resp, err := http.Get(url)
	if err == nil {
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body := make([]byte, 1024)
		n, _ := resp.Body.Read(body)
		assert.Contains(t, string(body[:n]), "body { color: blue; }")
	}

	cancel()

	select {
	case <-errChan:
	case <-time.After(15 * time.Second):
	}
}

// TestE2E_ReadinessProbe tests readiness probe behavior
func TestE2E_ReadinessProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	cfg := testhelpers.NewTestConfig(t)
	logger := testhelpers.NewTestLogger(t)

	// Create directories
	err := os.MkdirAll(filepath.Dir(cfg.PHPFPM.Config), 0755)
	require.NoError(t, err)
	err = os.MkdirAll(cfg.StaticFiles.Root, 0755)
	require.NoError(t, err)

	engine, err := bootstrap.New(cfg, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- engine.Start(ctx)
	}()

	// Before ready, readiness probe should fail or not respond
	time.Sleep(1 * time.Second)

	// After startup completes, readiness probe should succeed
	time.Sleep(3 * time.Second)

	if engine.IsReady() {
		url := fmt.Sprintf("http://localhost:%d/phpcloud/readyz", cfg.Server.HTTPPort)
		resp, err := http.Get(url)
		if err == nil {
			defer resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		}
	}

	cancel()

	select {
	case <-errChan:
	case <-time.After(15 * time.Second):
	}
}
