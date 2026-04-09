package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/cluster"
	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/cron"
	"github.com/sonroyaalmerol/phpcloud/internal/db"
	"github.com/sonroyaalmerol/phpcloud/internal/fpm"
	"github.com/sonroyaalmerol/phpcloud/internal/gateway"
	"github.com/sonroyaalmerol/phpcloud/internal/metrics"
	"github.com/sonroyaalmerol/phpcloud/internal/migration"
	"github.com/sonroyaalmerol/phpcloud/internal/session"
	"github.com/sonroyaalmerol/phpcloud/internal/sqlproxy"
	"go.uber.org/zap"
)

// Engine is the main runtime engine that orchestrates all components
type Engine struct {
	config *config.Config
	logger *zap.Logger

	dbManager  *db.Manager
	fpmManager *fpm.Manager
	sessionMgr *session.Manager
	clusterMgr *cluster.Manager
	migrator   *migration.Migrator
	cronMgr    *cron.Manager
	gateway    *gateway.Server
	metricsSrv *metrics.Server
	sqlProxy   *sqlproxy.Proxy

	// State
	isLeader   bool
	isReady    bool
	shutdownCh chan struct{}
}

// New creates a new Engine instance
func New(cfg *config.Config, logger *zap.Logger) (*Engine, error) {
	engine := &Engine{
		config:     cfg,
		logger:     logger,
		shutdownCh: make(chan struct{}),
	}

	return engine, nil
}

// Start initializes and starts all components
func (e *Engine) Start(ctx context.Context) error {
	e.logger.Info("Starting PHPCloud Runtime...")

	// Step 1: Initialize database connection
	if err := e.initDatabase(); err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}

	// Step 1b: Initialize SQL proxy (if enabled)
	if err := e.initSQLProxy(); err != nil {
		return fmt.Errorf("SQL proxy initialization failed: %w", err)
	}

	// Step 2: Initialize cluster coordination
	if err := e.initCluster(); err != nil {
		return fmt.Errorf("cluster initialization failed: %w", err)
	}

	// Step 3: Run migrations (if leader)
	if err := e.runMigrations(ctx); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	// Step 4: Initialize session manager
	if err := e.initSessionManager(); err != nil {
		return fmt.Errorf("session manager initialization failed: %w", err)
	}

	// Step 6: Start PHP-FPM (if enabled)
	if e.config.PHPFPM.Enabled {
		if err := e.initFPMManager(); err != nil {
			return fmt.Errorf("PHP-FPM initialization failed: %w", err)
		}
	} else {
		e.logger.Info("PHP-FPM disabled, skipping initialization")
	}

	// Step 7: Start cron scheduler (if leader)
	if err := e.initCronManager(); err != nil {
		return fmt.Errorf("cron manager initialization failed: %w", err)
	}

	// Step 8: Start metrics server
	if err := e.initMetricsServer(); err != nil {
		return fmt.Errorf("metrics server initialization failed: %w", err)
	}

	// Step 9: Start HTTP gateway (accept traffic)
	if err := e.initGateway(); err != nil {
		return fmt.Errorf("gateway initialization failed: %w", err)
	}

	e.isReady = true
	e.logger.Info("PHPCloud Runtime is ready and accepting traffic")

	// Wait for shutdown signal
	select {
	case <-ctx.Done():
		return e.Shutdown()
	case <-e.shutdownCh:
		return e.Shutdown()
	}
}

// Reload reloads configuration and components
func (e *Engine) Reload() error {
	e.logger.Info("Reloading configuration...")

	// Reload FPM configuration
	if e.fpmManager != nil {
		if err := e.fpmManager.Reload(); err != nil {
			e.logger.Error("Failed to reload FPM", zap.Error(err))
			return err
		}
	}

	e.logger.Info("Reload complete")
	return nil
}

// Shutdown gracefully shuts down all components
func (e *Engine) Shutdown() error {
	e.logger.Info("Shutting down gracefully...")
	e.isReady = false

	// Stop accepting new connections
	if e.gateway != nil {
		e.logger.Info("Stopping HTTP gateway...")
		if err := e.gateway.Stop(); err != nil {
			e.logger.Error("Failed to stop gateway", zap.Error(err))
		}
	}

	// Stop cron scheduler
	if e.cronMgr != nil {
		e.logger.Info("Stopping cron scheduler...")
		e.cronMgr.Stop()
	}

	// Leave cluster
	if e.clusterMgr != nil {
		e.logger.Info("Leaving cluster...")
		if err := e.clusterMgr.Leave(); err != nil {
			e.logger.Error("Failed to leave cluster", zap.Error(err))
		}
	}

	// Stop PHP-FPM
	if e.fpmManager != nil {
		e.logger.Info("Stopping PHP-FPM...")
		if err := e.fpmManager.Stop(); err != nil {
			e.logger.Error("Failed to stop FPM", zap.Error(err))
		}
	}

	// Stop SQL proxy
	if e.sqlProxy != nil {
		e.logger.Info("Stopping SQL proxy...")
		if err := e.sqlProxy.Stop(); err != nil {
			e.logger.Error("Failed to stop SQL proxy", zap.Error(err))
		}
	}

	// Close database connections
	if e.dbManager != nil {
		e.logger.Info("Closing database connections...")
		e.dbManager.Close()
	}

	// Stop metrics server
	if e.metricsSrv != nil {
		if err := e.metricsSrv.Stop(); err != nil {
			e.logger.Error("Failed to stop metrics server", zap.Error(err))
		}
	}

	e.logger.Info("Shutdown complete")
	return nil
}

// IsLeader returns true if this node is the cluster leader
func (e *Engine) IsLeader() bool {
	return e.isLeader
}

// IsReady returns true if the runtime is ready to accept traffic
func (e *Engine) IsReady() bool {
	return e.isReady
}

// nodeName returns the configured node name, falling back to the OS hostname.
func (e *Engine) nodeName() string {
	if name := e.config.Cluster.NodeName; name != "" {
		return name
	}
	name, _ := os.Hostname()
	return name
}

// initDatabase initializes the CRDT storage
func (e *Engine) initDatabase() error {
	e.logger.Info("Initializing CRDT storage...")

	nodeID := e.nodeName()

	dbManager, err := db.New(e.config, nodeID, e.logger)
	if err != nil {
		return err
	}

	e.logger.Info("CRDT storage initialized with HLC",
		zap.String("node_id", nodeID),
		zap.String("hlc_version", dbManager.GetClock().GetLatest().String()))

	e.dbManager = dbManager
	return nil
}

// initSQLProxy initializes the SQL proxy for database migrations
func (e *Engine) initSQLProxy() error {
	if !e.config.SQLProxy.Enabled {
		e.logger.Info("SQL proxy disabled")
		return nil
	}

	e.logger.Info("Initializing SQL proxy...",
		zap.String("listen_addr", e.config.SQLProxy.ListenAddr),
		zap.String("target", fmt.Sprintf("%s:%d", e.config.SQLProxy.TargetHost, e.config.SQLProxy.TargetPort)))

	proxy, err := sqlproxy.New(e.config, e.logger, e.config.SQLProxy.TargetHost, e.config.SQLProxy.TargetPort)
	if err != nil {
		return fmt.Errorf("failed to create SQL proxy: %w", err)
	}

	if err := proxy.Start(e.config.SQLProxy.ListenAddr); err != nil {
		return fmt.Errorf("failed to start SQL proxy: %w", err)
	}

	e.sqlProxy = proxy
	e.logger.Info("SQL proxy started successfully")

	return nil
}

// initCluster initializes cluster coordination
func (e *Engine) initCluster() error {
	if !e.config.Cluster.Enabled {
		e.logger.Info("Cluster coordination disabled")
		return nil
	}

	e.logger.Info("Initializing cluster coordination...")

	clusterMgr, err := cluster.New(e.config, e.nodeName(), e.logger)
	if err != nil {
		return err
	}

	if err := clusterMgr.Start(); err != nil {
		return err
	}

	e.clusterMgr = clusterMgr

	// Check leadership
	e.checkLeadership()

	// Watch for leadership changes
	go e.watchLeadership()

	return nil
}

// runMigrations runs application migrations if this node is the leader
func (e *Engine) runMigrations(ctx context.Context) error {
	if !e.config.Migration.Enabled {
		e.logger.Info("Migrations disabled")
		return nil
	}

	e.logger.Info("Checking for migrations...")

	migrator := migration.New(e.config, e.dbManager, e.logger)

	// Try to acquire migration lock
	locked, err := migrator.AcquireLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire migration lock: %w", err)
	}

	if locked {
		e.logger.Info("Acquired migration lock, running migrations...")

		if err := migrator.Run(ctx); err != nil {
			_ = migrator.ReleaseLock()
			return fmt.Errorf("migration failed: %w", err)
		}

		e.logger.Info("Migrations complete")

		if err := migrator.ReleaseLock(); err != nil {
			e.logger.Error("Failed to release migration lock", zap.Error(err))
		}
	} else {
		e.logger.Info("Migration lock held by another node, waiting...")

		// Wait for migration to complete
		if err := migrator.WaitForCompletion(ctx); err != nil {
			return fmt.Errorf("migration wait failed: %w", err)
		}

		e.logger.Info("Migration complete on leader")
	}

	e.migrator = migrator
	return nil
}

// initSessionManager initializes the session manager
func (e *Engine) initSessionManager() error {
	if !e.config.Session.Enabled {
		e.logger.Info("Session management disabled")
		return nil
	}

	e.logger.Info("Initializing session manager...")

	sessionMgr, err := session.New(e.config, e.dbManager, e.logger)
	if err != nil {
		return err
	}

	if err := sessionMgr.Start(); err != nil {
		return err
	}

	e.sessionMgr = sessionMgr
	return nil
}

// initFPMManager initializes the PHP-FPM manager
func (e *Engine) initFPMManager() error {
	e.logger.Info("Initializing PHP-FPM manager...")

	fpmManager, err := fpm.New(e.config, e.logger)
	if err != nil {
		return err
	}

	if err := fpmManager.Start(); err != nil {
		return err
	}

	e.fpmManager = fpmManager
	return nil
}

// initCronManager initializes the cron manager
func (e *Engine) initCronManager() error {
	if !e.config.Cron.Enabled {
		e.logger.Info("Cron scheduler disabled")
		return nil
	}

	if e.config.Cron.LeaderOnly && !e.isLeader {
		e.logger.Info("Cron scheduler enabled but not leader, skipping")
		return nil
	}

	e.logger.Info("Initializing cron scheduler...")

	cronMgr := cron.New(e.config, e.logger)

	if err := cronMgr.Start(); err != nil {
		return err
	}

	e.cronMgr = cronMgr
	return nil
}

// initMetricsServer initializes the metrics server
func (e *Engine) initMetricsServer() error {
	if !e.config.Metrics.Enabled {
		e.logger.Info("Metrics server disabled")
		return nil
	}

	e.logger.Info("Initializing metrics server...")

	metricsSrv := metrics.New(e.config, e.logger)

	if err := metricsSrv.Start(); err != nil {
		return err
	}

	e.metricsSrv = metricsSrv
	return nil
}

// initGateway initializes the HTTP gateway
func (e *Engine) initGateway() error {
	e.logger.Info("Initializing HTTP gateway...")

	gw, err := gateway.New(e.config, e.fpmManager, e.sessionMgr, e.logger)
	if err != nil {
		return err
	}

	// Register health endpoints
	gw.RegisterInternalHandler("/phpcloud/healthz", e.healthHandler)
	gw.RegisterInternalHandler("/phpcloud/readyz", e.readyHandler)

	if err := gw.Start(); err != nil {
		return err
	}

	e.gateway = gw
	return nil
}

// checkLeadership checks if this node is the leader
func (e *Engine) checkLeadership() {
	if e.clusterMgr == nil {
		e.isLeader = true // Single node mode
		return
	}

	wasLeader := e.isLeader
	e.isLeader = e.clusterMgr.IsLeader()

	if wasLeader != e.isLeader {
		if e.isLeader {
			e.logger.Info("This node is now the cluster leader")
			e.onLeadershipAcquired()
		} else {
			e.logger.Info("This node is no longer the cluster leader")
			e.onLeadershipLost()
		}
	}
}

// watchLeadership watches for leadership changes
func (e *Engine) watchLeadership() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.checkLeadership()
		case <-e.shutdownCh:
			return
		}
	}
}

// onLeadershipAcquired handles leadership acquisition
func (e *Engine) onLeadershipAcquired() {
	// Start cron scheduler if enabled
	if e.config.Cron.Enabled && e.config.Cron.LeaderOnly && e.cronMgr == nil {
		cronMgr := cron.New(e.config, e.logger)
		if err := cronMgr.Start(); err != nil {
			e.logger.Error("Failed to start cron scheduler", zap.Error(err))
		} else {
			e.cronMgr = cronMgr
		}
	}
}

// onLeadershipLost handles leadership loss
func (e *Engine) onLeadershipLost() {
	// Stop cron scheduler if leader-only
	if e.config.Cron.Enabled && e.config.Cron.LeaderOnly && e.cronMgr != nil {
		e.cronMgr.Stop()
		e.cronMgr = nil
	}
}

// healthHandler handles health check requests
func (e *Engine) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("healthy"))
}

// readyHandler handles readiness check requests
func (e *Engine) readyHandler(w http.ResponseWriter, r *http.Request) {
	if !e.isReady {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}
