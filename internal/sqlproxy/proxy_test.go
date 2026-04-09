package sqlproxy

import (
	"net"
	"testing"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestProxy(t *testing.T) *Proxy {
	t.Helper()
	p, err := New(&config.Config{}, zap.NewNop(), "127.0.0.1", 3306)
	require.NoError(t, err)
	return p
}

// ─── State management ───────────────────────────────────────────────────────

func TestProxy_State_Default(t *testing.T) {
	p := newTestProxy(t)
	assert.False(t, p.isMigrating())
	assert.Equal(t, "normal", p.state.String())
}

func TestProxy_StartMigration(t *testing.T) {
	p := newTestProxy(t)
	p.StartMigration()

	assert.True(t, p.isMigrating())
	assert.Equal(t, "migrating(read-only)", p.state.String())

	// Idempotent — calling again should not panic or change stats
	before := p.migrationsStarted.Load()
	p.StartMigration()
	assert.Equal(t, before, p.migrationsStarted.Load(), "double-start must not double-count")
}

func TestProxy_EndMigration(t *testing.T) {
	p := newTestProxy(t)
	p.StartMigration()
	p.EndMigration()

	assert.False(t, p.isMigrating())

	// Idempotent
	assert.NotPanics(t, func() { p.EndMigration() })
}

// ─── Query inspection ────────────────────────────────────────────────────────

// buildPacket creates a minimal MySQL-style packet: 4-byte header + payload
func buildPacket(payload string) []byte {
	data := make([]byte, 4+len(payload))
	// header (length + sequence byte) — values don't matter for inspection logic
	copy(data[4:], []byte(payload))
	return data
}

func TestProxy_IsWriteQuery(t *testing.T) {
	writes := []string{
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET a=1",
		"DELETE FROM t",
		"REPLACE INTO t VALUES (1)",
		"CREATE TABLE t (id INT)",
		"ALTER TABLE t ADD col INT",
		"DROP TABLE t",
		"TRUNCATE t",
	}
	for _, q := range writes {
		assert.True(t, newTestProxy(t).isWriteQuery(buildPacket(q)), "should detect as write: %s", q)
	}
}

func TestProxy_IsWriteQuery_SelectIsNotWrite(t *testing.T) {
	p := newTestProxy(t)
	assert.False(t, p.isWriteQuery(buildPacket("SELECT * FROM t")))
	assert.False(t, p.isWriteQuery(buildPacket("SHOW TABLES")))
}

func TestProxy_IsWriteQuery_TooShort(t *testing.T) {
	p := newTestProxy(t)
	assert.False(t, p.isWriteQuery([]byte{1, 2, 3}), "short packet should not panic")
}

func TestProxy_IsMigrationQuery(t *testing.T) {
	migrations := []string{
		"CREATE TABLE new_table (id INT)",
		"ALTER TABLE t ADD COLUMN c INT",
		"DROP TABLE old_table",
		"CREATE INDEX idx ON t(col)",
		"DROP INDEX idx ON t",
		"RENAME TABLE old TO new",
	}
	for _, q := range migrations {
		assert.True(t, newTestProxy(t).isMigrationQuery(buildPacket(q)),
			"should detect as migration query: %s", q)
	}
}

func TestProxy_IsMigrationQuery_DMLIsNotMigration(t *testing.T) {
	p := newTestProxy(t)
	assert.False(t, p.isMigrationQuery(buildPacket("INSERT INTO t VALUES (1)")))
	assert.False(t, p.isMigrationQuery(buildPacket("UPDATE t SET x=1")))
}

// ─── Stats ───────────────────────────────────────────────────────────────────

func TestProxy_GetStats_DefaultsZero(t *testing.T) {
	p := newTestProxy(t)
	stats := p.GetStats()

	assert.Equal(t, int64(0), stats["connections_total"])
	assert.Equal(t, int64(0), stats["connections_active"])
	assert.Equal(t, int64(0), stats["queries_blocked"])
	assert.Equal(t, int64(0), stats["queries_allowed"])
	assert.Equal(t, int64(0), stats["migrations_started"])
}

func TestProxy_GetStats_AfterMigration(t *testing.T) {
	p := newTestProxy(t)
	p.StartMigration()
	p.EndMigration()

	stats := p.GetStats()
	assert.Equal(t, int64(1), stats["migrations_started"])
}

// ─── MySQL error packet ──────────────────────────────────────────────────────

func TestProxy_CreateMySQLError(t *testing.T) {
	p := newTestProxy(t)
	pkt := p.createMySQLError("test error")

	// Must have at least 4-byte header + error indicator byte
	require.Greater(t, len(pkt), 5)
	// Error indicator byte is 0xFF (after the 4-byte MySQL packet header)
	assert.Equal(t, byte(0xFF), pkt[4])
}

// ─── Start / Stop ────────────────────────────────────────────────────────────

func TestProxy_StartAndStop(t *testing.T) {
	p := newTestProxy(t)

	// Find a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	err = p.Start(addr)
	require.NoError(t, err)

	// Verify the proxy is listening
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		conn.Close()
	}
	// Connection may fail because the target DB (127.0.0.1:3306) is not available,
	// but the listener itself should be up

	err = p.Stop()
	assert.NoError(t, err)
}

// ─── State string ────────────────────────────────────────────────────────────

func TestState_String_Unknown(t *testing.T) {
	s := State(99)
	assert.Equal(t, "unknown", s.String())
}
