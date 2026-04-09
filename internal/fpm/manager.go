package fpm

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"go.uber.org/zap"
)

// Manager handles PHP-FPM lifecycle
type Manager struct {
	config     *config.Config
	logger     *zap.Logger
	cmd        *exec.Cmd
	mu         sync.RWMutex
	running    bool
	socketAddr string // address part of the socket URL (no scheme prefix)
	external   bool   // If true, FPM is external (not managed by us)
}

// New creates a new FPM manager
func New(cfg *config.Config, logger *zap.Logger) (*Manager, error) {
	_, socketAddr := cfg.PHPFPM.ParseSocket()
	return &Manager{
		config:     cfg,
		logger:     logger,
		socketAddr: socketAddr,
		external:   cfg.PHPFPM.External,
	}, nil
}

// Start starts the PHP-FPM subprocess (or connects to external FPM)
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("PHP-FPM is already running")
	}

	// External mode: just wait for existing FPM to be ready
	if m.external {
		m.logger.Info("Connecting to external PHP-FPM...", zap.String("socket", m.config.PHPFPM.Socket))

		// Wait for external FPM to be ready
		if err := m.waitForReady(); err != nil {
			return fmt.Errorf("external PHP-FPM is not ready: %w", err)
		}

		m.running = true
		m.logger.Info("Connected to external PHP-FPM successfully")
		return nil
	}

	// Managed mode: spawn and manage FPM process
	m.logger.Info("Starting PHP-FPM...")

	// Ensure socket directory exists (only meaningful for unix sockets)
	if network, _ := m.config.PHPFPM.ParseSocket(); network == "unix" {
		socketDir := filepath.Dir(m.socketAddr)
		if err := os.MkdirAll(socketDir, 0755); err != nil {
			return fmt.Errorf("failed to create socket directory: %w", err)
		}
	}

	// Generate FPM config
	if err := m.generateConfig(); err != nil {
		return fmt.Errorf("failed to generate FPM config: %w", err)
	}

	// Start FPM process
	m.cmd = exec.Command(m.config.PHPFPM.Binary, "-y", m.config.PHPFPM.Config, "-F")
	m.cmd.Stdout = os.Stdout
	m.cmd.Stderr = os.Stderr

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start PHP-FPM: %w", err)
	}

	m.running = true

	// Wait for FPM to be ready
	if err := m.waitForReady(); err != nil {
		_ = m.cmd.Process.Kill()
		return fmt.Errorf("PHP-FPM failed to become ready: %w", err)
	}

	// Start monitor goroutine
	go m.monitor()

	m.logger.Info("PHP-FPM started successfully", zap.Int("pid", m.cmd.Process.Pid))
	return nil
}

// Stop stops the PHP-FPM subprocess gracefully (or disconnects from external FPM)
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	// External mode: just mark as not running, don't kill external FPM
	if m.external {
		m.logger.Info("Disconnecting from external PHP-FPM")
		m.running = false
		return nil
	}

	m.logger.Info("Stopping PHP-FPM...")

	// Send SIGQUIT for graceful shutdown
	if m.cmd != nil && m.cmd.Process != nil {
		if err := m.cmd.Process.Signal(os.Signal(syscall.SIGQUIT)); err != nil {
			m.logger.Warn("Failed to send SIGQUIT to FPM", zap.Error(err))
			// Force kill
			_ = m.cmd.Process.Kill()
		}

		// Wait for process to exit
		done := make(chan error, 1)
		go func() {
			done <- m.cmd.Wait()
		}()

		select {
		case <-done:
			m.logger.Info("PHP-FPM stopped gracefully")
		case <-time.After(30 * time.Second):
			m.logger.Warn("PHP-FPM graceful shutdown timeout, forcing kill")
			_ = m.cmd.Process.Kill()
			_ = m.cmd.Wait()
		}
	}

	m.running = false
	return nil
}

// Reload reloads PHP-FPM configuration
func (m *Manager) Reload() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.running {
		return fmt.Errorf("PHP-FPM is not running")
	}

	m.logger.Info("Reloading PHP-FPM configuration...")

	// Regenerate config
	if err := m.generateConfig(); err != nil {
		return fmt.Errorf("failed to regenerate config: %w", err)
	}

	// Send SIGUSR2 for graceful reload
	if m.cmd != nil && m.cmd.Process != nil {
		if err := m.cmd.Process.Signal(syscall.SIGUSR2); err != nil {
			return fmt.Errorf("failed to send SIGUSR2: %w", err)
		}
	}

	// Wait for FPM to be ready again
	if err := m.waitForReady(); err != nil {
		return fmt.Errorf("PHP-FPM failed to reload: %w", err)
	}

	m.logger.Info("PHP-FPM reloaded successfully")
	return nil
}

// IsRunning returns true if FPM is running
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// GetSocketAddr returns the FPM socket address (without scheme prefix)
func (m *Manager) GetSocketAddr() string {
	return m.socketAddr
}

// generateConfig generates the PHP-FPM pool configuration
func (m *Manager) generateConfig() error {
	configDir := filepath.Dir(m.config.PHPFPM.Config)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	configContent := fmt.Sprintf(`[global]
error_log = /proc/self/fd/2
daemonize = no

[www]
listen = %s
listen.owner = www-data
listen.group = www-data
listen.mode = 0660

pm = dynamic
pm.max_children = %d
pm.start_servers = %d
pm.min_spare_servers = %d
pm.max_spare_servers = %d
pm.max_requests = 1000

access.log = /proc/self/fd/2
slowlog = /proc/self/fd/2
request_slowlog_timeout = 10s

php_admin_value[memory_limit] = 512M
`,
		m.socketAddr,
		m.config.PHPFPM.PoolSizeMax,
		m.config.PHPFPM.PoolSizeMin,
		m.config.PHPFPM.PoolSizeMin,
		m.config.PHPFPM.PoolSizeMax,
	)

	return os.WriteFile(m.config.PHPFPM.Config, []byte(configContent), 0644)
}

// waitForReady waits for PHP-FPM to be ready
func (m *Manager) waitForReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	network, address := m.config.PHPFPM.ParseSocket()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if network == "unix" {
				if _, err := os.Stat(address); os.IsNotExist(err) {
					continue
				}
			}
			conn, err := net.Dial(network, address)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}

// monitor monitors the FPM process and restarts if needed
func (m *Manager) monitor() {
	if m.cmd == nil {
		return
	}

	err := m.cmd.Wait()

	m.mu.Lock()
	m.running = false
	m.mu.Unlock()

	m.logger.Error("PHP-FPM exited unexpectedly", zap.Error(err))

	// Attempt restart with backoff
	time.Sleep(2 * time.Second)

	if err := m.Start(); err != nil {
		m.logger.Error("Failed to restart PHP-FPM", zap.Error(err))
	}
}
