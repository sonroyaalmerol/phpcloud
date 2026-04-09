// Package sqlproxy provides a MySQL proxy for migration-aware read-only mode
package sqlproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"go.uber.org/zap"
)

// State represents the proxy state
type State int

const (
	StateNormal State = iota
	StateMigrating
)

func (s State) String() string {
	switch s {
	case StateNormal:
		return "normal"
	case StateMigrating:
		return "migrating(read-only)"
	default:
		return "unknown"
	}
}

// Proxy is a TCP proxy for MySQL with query inspection
type Proxy struct {
	config   *config.Config
	logger   *zap.Logger
	listener net.Listener

	// Target database
	targetHost string
	targetPort int

	// State management
	state   State
	stateMu sync.RWMutex

	// Metrics — use atomics to avoid data races from concurrent goroutines
	connectionsTotal  atomic.Int64
	connectionsActive atomic.Int64
	queriesBlocked    atomic.Int64
	queriesAllowed    atomic.Int64
	migrationsStarted atomic.Int64

	// Control
	// acceptWg tracks the acceptLoop goroutine; connWg tracks per-connection goroutines.
	// We must wait for acceptLoop to exit before calling connWg.Wait() because
	// acceptLoop calls connWg.Add(1) — mixing Add and Wait is a data race.
	ctx      context.Context
	cancel   context.CancelFunc
	acceptWg sync.WaitGroup
	connWg   sync.WaitGroup
}

// New creates a new SQL proxy
func New(cfg *config.Config, logger *zap.Logger, targetHost string, targetPort int) (*Proxy, error) {
	ctx, cancel := context.WithCancel(context.Background())

	return &Proxy{
		config:     cfg,
		logger:     logger,
		targetHost: targetHost,
		targetPort: targetPort,
		state:      StateNormal,
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// Start starts the SQL proxy
func (p *Proxy) Start(listenAddr string) error {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listenAddr, err)
	}

	p.listener = listener

	p.logger.Info("SQL proxy started",
		zap.String("listen_addr", listenAddr),
		zap.String("target", fmt.Sprintf("%s:%d", p.targetHost, p.targetPort)),
	)

	p.acceptWg.Add(1)
	go p.acceptLoop()

	return nil
}

// acceptLoop accepts incoming connections
func (p *Proxy) acceptLoop() {
	defer p.acceptWg.Done()
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				return // Shutting down
			default:
				p.logger.Error("Failed to accept connection", zap.Error(err))
				continue
			}
		}

		p.connWg.Add(1)
		go p.handleConnection(conn)
	}
}

// handleConnection handles a single client connection
func (p *Proxy) handleConnection(clientConn net.Conn) {
	defer p.connWg.Done()

	p.connectionsTotal.Add(1)
	p.connectionsActive.Add(1)
	defer p.connectionsActive.Add(-1)

	// Connect to target database
	targetConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", p.targetHost, p.targetPort))
	if err != nil {
		p.logger.Error("Failed to connect to target database", zap.Error(err))
		clientConn.Close()
		return
	}
	defer targetConn.Close()

	// Start bidirectional proxy: client→target with inspection, target→client plain
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		p.proxyWithInspection(clientConn, targetConn, "client->target")
	}()

	go func() {
		defer wg.Done()
		p.proxyWithInspection(targetConn, clientConn, "target->client")
	}()

	wg.Wait()
}

// proxyWithInspection proxies data from src to dst, inspecting MySQL packets
func (p *Proxy) proxyWithInspection(src, dst net.Conn, direction string) {
	defer src.Close()
	defer dst.Close()

	buffer := make([]byte, 65536)

	for {
		n, err := src.Read(buffer)
		if err != nil {
			if err != io.EOF {
				p.logger.Debug("Read error", zap.String("direction", direction), zap.Error(err))
			}
			return
		}

		data := buffer[:n]

		// Inspect MySQL query if in migration mode
		if p.isMigrating() && direction == "client->target" {
			if p.isWriteQuery(data) && !p.isMigrationQuery(data) {
				p.queriesBlocked.Add(1)
				p.logger.Warn("Blocked write query during migration",
					zap.String("src", src.RemoteAddr().String()),
					zap.Int("bytes", n),
				)
				errorPacket := p.createMySQLError("ERROR 1290 (HY000): Database is in read-only mode for migration")
				_, _ = src.Write(errorPacket)
				continue
			}
		}

		p.queriesAllowed.Add(1)

		_, err = dst.Write(data)
		if err != nil {
			p.logger.Debug("Write error", zap.String("direction", direction), zap.Error(err))
			return
		}
	}
}

// isWriteQuery checks if the MySQL packet contains a write query
func (p *Proxy) isWriteQuery(data []byte) bool {
	// MySQL packet structure: 4 bytes header (3 length + 1 sequence) + payload
	if len(data) < 5 {
		return false
	}

	// Extract payload (skip 4-byte header)
	payload := string(data[4:])

	// Check for write operations
	writeOps := []string{
		"INSERT", "UPDATE", "DELETE", "REPLACE",
		"CREATE", "ALTER", "DROP", "TRUNCATE",
		"GRANT", "REVOKE", "LOCK", "UNLOCK",
	}

	upperPayload := strings.ToUpper(payload)
	for _, op := range writeOps {
		if strings.Contains(upperPayload, op) {
			return true
		}
	}

	return false
}

// isMigrationQuery checks if this is a migration-related query
func (p *Proxy) isMigrationQuery(data []byte) bool {
	if len(data) < 5 {
		return false
	}

	payload := strings.ToUpper(string(data[4:]))

	// Migration queries we allow even in read-only mode
	migrationOps := []string{
		"CREATE TABLE", "ALTER TABLE", "DROP TABLE",
		"CREATE INDEX", "DROP INDEX",
		"RENAME TABLE",
	}

	for _, op := range migrationOps {
		if strings.Contains(payload, op) {
			return true
		}
	}

	return false
}

// createMySQLError creates a MySQL error packet
func (p *Proxy) createMySQLError(message string) []byte {
	// Simplified MySQL error packet
	// In production, use proper MySQL protocol implementation
	packet := make([]byte, 0, 256)

	// Error packet header
	packet = append(packet, 0xFF) // Error indicator

	// Error code (1290 = ER_READ_ONLY_MODE)
	packet = append(packet, 0x0A, 0x05) // Error code 1290 in little-endian

	// SQL State marker
	packet = append(packet, '#')

	// SQL State (HY000)
	packet = append(packet, []byte("HY000")...)

	// Error message
	packet = append(packet, []byte(message)...)

	// Prepend length (3 bytes little-endian)
	length := len(packet)
	result := make([]byte, 0, length+4)
	result = append(result, byte(length), byte(length>>8), byte(length>>16))
	result = append(result, 1) // Sequence number
	result = append(result, packet...)

	return result
}

// isMigrating returns true if we're in migration mode
func (p *Proxy) isMigrating() bool {
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()
	return p.state == StateMigrating
}

// StartMigration enters migration (read-only) mode
func (p *Proxy) StartMigration() {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	if p.state == StateMigrating {
		return
	}

	p.state = StateMigrating
	p.migrationsStarted.Add(1)

	p.logger.Info("SQL proxy entering read-only mode for migration",
		zap.String("target", fmt.Sprintf("%s:%d", p.targetHost, p.targetPort)),
	)
}

// EndMigration exits migration mode
func (p *Proxy) EndMigration() {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	if p.state == StateNormal {
		return
	}

	p.state = StateNormal

	p.logger.Info("SQL proxy exiting read-only mode",
		zap.Int64("queries_blocked", p.queriesBlocked.Load()),
		zap.Int64("queries_allowed", p.queriesAllowed.Load()),
	)
}

// GetStats returns proxy statistics
func (p *Proxy) GetStats() map[string]int64 {
	return map[string]int64{
		"connections_total":  p.connectionsTotal.Load(),
		"connections_active": p.connectionsActive.Load(),
		"queries_blocked":    p.queriesBlocked.Load(),
		"queries_allowed":    p.queriesAllowed.Load(),
		"migrations_started": p.migrationsStarted.Load(),
	}
}

// Stop stops the proxy
func (p *Proxy) Stop() error {
	p.cancel()

	if p.listener != nil {
		p.listener.Close()
	}

	// Wait for acceptLoop to finish first (it calls connWg.Add, so we must
	// drain it before calling connWg.Wait to avoid a data race).
	acceptDone := make(chan struct{})
	go func() {
		p.acceptWg.Wait()
		close(acceptDone)
	}()

	select {
	case <-acceptDone:
	case <-time.After(5 * time.Second):
		p.logger.Warn("Timeout waiting for accept loop to stop")
	}

	// Now safe to wait for all connection goroutines to finish
	connDone := make(chan struct{})
	go func() {
		p.connWg.Wait()
		close(connDone)
	}()

	select {
	case <-connDone:
	case <-time.After(30 * time.Second):
		p.logger.Warn("Timeout waiting for connections to close")
	}

	return nil
}
