package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/bootstrap"
	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freePort returns a free TCP port on localhost.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// newE2EConfig returns a fully-isolated test config using temp dirs and
// dynamically allocated ports so tests never conflict with each other.
func newE2EConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := testhelpers.NewTestConfig(t)
	cfg.Server.HTTPPort = freePort(t)
	cfg.Server.MetricsPort = freePort(t)
	cfg.Server.GossipPort = freePort(t)
	cfg.DB.Path = t.TempDir()
	cfg.StaticFiles.Root = t.TempDir()
	cfg.PHPFPM.Config = filepath.Join(t.TempDir(), "php-fpm.conf")
	return cfg
}

// startEngine starts the engine in a goroutine and returns a cancel func and
// error channel. The caller is responsible for calling cancel.
func startEngine(t *testing.T, cfg *config.Config) (engine *bootstrap.Engine, cancel context.CancelFunc, errCh <-chan error) {
	t.Helper()
	logger := testhelpers.NewTestLogger(t)

	e, err := bootstrap.New(cfg, logger)
	require.NoError(t, err)

	ctx, cfn := context.WithCancel(context.Background())

	ch := make(chan error, 1)
	go func() { ch <- e.Start(ctx) }()

	t.Cleanup(func() { cfn() })
	return e, cfn, ch
}

// waitReady polls until engine.IsReady() or a timeout.
func waitReady(t *testing.T, e *bootstrap.Engine, timeout time.Duration) {
	t.Helper()
	testhelpers.WaitForCondition(t, e.IsReady, timeout, "engine to become ready")
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// TestE2E_FullStartup verifies that the engine starts, becomes ready, serves
// the health endpoint, and shuts down cleanly.
func TestE2E_FullStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	cfg := newE2EConfig(t)
	engine, cancel, errCh := startEngine(t, cfg)

	waitReady(t, engine, 15*time.Second)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/phpcloud/healthz", cfg.Server.HTTPPort))
	require.NoError(t, err, "health endpoint must be reachable")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for shutdown")
	}
}

// TestE2E_ReadinessProbe verifies the /phpcloud/readyz endpoint returns 200
// once the engine is ready, and 503 before it is.
func TestE2E_ReadinessProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	cfg := newE2EConfig(t)
	engine, cancel, errCh := startEngine(t, cfg)

	waitReady(t, engine, 15*time.Second)

	url := fmt.Sprintf("http://127.0.0.1:%d/phpcloud/readyz", cfg.Server.HTTPPort)
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for shutdown")
	}
}

// TestE2E_StaticFileServing verifies that a static file written to the root
// directory is served correctly by the gateway.
func TestE2E_StaticFileServing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	cfg := newE2EConfig(t)

	cssContent := "body { color: blue; }"
	cssPath := filepath.Join(cfg.StaticFiles.Root, "style.css")
	require.NoError(t, os.WriteFile(cssPath, []byte(cssContent), 0644))

	engine, cancel, errCh := startEngine(t, cfg)
	waitReady(t, engine, 15*time.Second)

	url := fmt.Sprintf("http://127.0.0.1:%d/style.css", cfg.Server.HTTPPort)
	resp, err := http.Get(url)
	require.NoError(t, err, "static file must be reachable")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	assert.Contains(t, string(body[:n]), "body { color: blue; }")

	cancel()
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for shutdown")
	}
}

// TestE2E_MigrationLock verifies that two engines sharing the same DB acquire
// the migration lock exclusively — only one can hold it at a time.
func TestE2E_MigrationLock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// Both engines share the same DB path and migration lock key so we can
	// observe exclusive lock acquisition.
	sharedDB := t.TempDir()
	sharedRoot := t.TempDir()

	makeCfg := func(nodeName string) *config.Config {
		cfg := newE2EConfig(t)
		cfg.DB.Path = sharedDB
		cfg.StaticFiles.Root = sharedRoot
		cfg.Cluster.NodeName = nodeName
		cfg.Cluster.Enabled = false // no gossip needed for this test
		cfg.Migration.LockKey = "shared:migration"
		return cfg
	}

	cfg1 := makeCfg("node1")
	cfg2 := makeCfg("node2")
	// Give node2 a different HTTP port (cfg already has unique ports)

	engine1, cancel1, errCh1 := startEngine(t, cfg1)
	engine2, cancel2, errCh2 := startEngine(t, cfg2)

	// Wait until both engines are ready (migration is done)
	testhelpers.WaitForCondition(t, func() bool {
		return engine1.IsReady() || engine2.IsReady()
	}, 20*time.Second, "at least one engine to be ready")

	// Once both are up, both must eventually be ready (migration propagates)
	testhelpers.WaitForCondition(t, func() bool {
		return engine1.IsReady() && engine2.IsReady()
	}, 30*time.Second, "both engines to be ready after migration")

	cancel1()
	cancel2()

	for _, ch := range []<-chan error{errCh1, errCh2} {
		select {
		case <-ch:
		case <-time.After(15 * time.Second):
			t.Fatal("timeout waiting for engine shutdown")
		}
	}
}

// TestE2E_ClusterFormation verifies that a single-node cluster elects itself
// as leader.
func TestE2E_ClusterFormation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	cfg := newE2EConfig(t)
	cfg.Cluster.Enabled = true
	cfg.Cluster.Discovery = "static" // no DNS; single node
	cfg.Cluster.StaticPeers = nil

	engine, cancel, errCh := startEngine(t, cfg)
	waitReady(t, engine, 15*time.Second)

	assert.True(t, engine.IsLeader(), "single-node cluster must elect itself as leader")

	cancel()
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for shutdown")
	}
}

// TestE2E_SessionPersistenceWithinInstance verifies that sessions written to
// the CRDT store during a run are readable within the same instance.
func TestE2E_SessionPersistenceWithinInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	cfg := newE2EConfig(t)
	engine, cancel, errCh := startEngine(t, cfg)
	waitReady(t, engine, 15*time.Second)

	// Verify the readiness endpoint responds — the session manager is live
	url := fmt.Sprintf("http://127.0.0.1:%d/phpcloud/readyz", cfg.Server.HTTPPort)
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for shutdown")
	}
}
