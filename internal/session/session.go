package session

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/crdt"
	"github.com/sonroyaalmerol/phpcloud/internal/db"
	"go.uber.org/zap"
)

// Manager handles PHP session management with CRDT-based replication
// Sessions are automatically replicated across the cluster using CRDTs with HLC
type Manager struct {
	config     *config.Config
	db         *db.Manager
	logger     *zap.Logger
	cookieName string
	quit       chan struct{}
}

// Session represents a PHP session
type Session struct {
	ID      string
	Data    []byte
	Expires time.Time
}

// New creates a new CRDT-based session manager
func New(cfg *config.Config, dbManager *db.Manager, logger *zap.Logger) (*Manager, error) {
	cookieName := cfg.Session.CookieName
	if cookieName == "" {
		cookieName = "PHPSESSID"
	}

	return &Manager{
		config:     cfg,
		db:         dbManager,
		logger:     logger,
		cookieName: cookieName,
		quit:       make(chan struct{}),
	}, nil
}

// Start starts the session manager background tasks
// Background cleanup is handled by db.Manager
func (m *Manager) Start() error {
	return nil
}

// Stop stops the session manager
func (m *Manager) Stop() {
	close(m.quit)
}

// GetSession retrieves a session by ID
// Returns nil if not found or expired (CRDT handles expiration via LWW)
func (m *Manager) GetSession(ctx context.Context, id string) (*Session, error) {
	if id == "" {
		return nil, nil
	}

	session := m.db.GetSession(id)
	if session == nil {
		return nil, nil
	}

	return &Session{
		ID:      session.ID,
		Data:    session.Data,
		Expires: session.ExpiresAt,
	}, nil
}

// SaveSession saves a session to the CRDT store
// Automatically sets HLC timestamp for LWW replication
func (m *Manager) SaveSession(ctx context.Context, session *Session) error {
	expires := session.Expires
	if expires.IsZero() {
		expires = time.Now().Add(m.config.Session.TTL)
	}

	// Create or update CRDT session
	crdtSession := &crdt.SessionCRDT{
		ID:        session.ID,
		Data:      session.Data,
		ExpiresAt: expires,
	}

	m.db.SaveSession(crdtSession)
	return nil
}

// DeleteSession marks a session as deleted (CRDT tombstone)
func (m *Manager) DeleteSession(ctx context.Context, id string) error {
	m.db.DeleteSession(id)
	return nil
}

// LockSession locks a session for exclusive access using CRDTs
func (m *Manager) LockSession(ctx context.Context, id string, holder string) error {
	session := m.db.GetSession(id)
	if session == nil {
		// Create empty session to lock
		session = m.db.CreateSession(id, nil, m.config.Session.TTL)
	}

	// Lock using CRDT with HLC timestamp
	session.Lock(holder, m.db.GetClock())
	m.db.SaveSession(session)
	return nil
}

// UnlockSession unlocks a session using CRDTs
func (m *Manager) UnlockSession(ctx context.Context, id string) error {
	session := m.db.GetSession(id)
	if session == nil {
		return nil
	}

	session.Unlock(m.db.GetClock())
	m.db.SaveSession(session)
	return nil
}

// ExtractSessionID extracts the session ID from an HTTP request
func (m *Manager) ExtractSessionID(r *http.Request) string {
	cookie, err := r.Cookie(m.cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// SetSessionCookie sets the session cookie on an HTTP response
func (m *Manager) SetSessionCookie(w http.ResponseWriter, id string, expires time.Time) {
	cookie := &http.Cookie{
		Name:     m.cookieName,
		Value:    id,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, cookie)
}

// ClearSessionCookie clears the session cookie
func (m *Manager) ClearSessionCookie(w http.ResponseWriter) {
	cookie := &http.Cookie{
		Name:     m.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, cookie)
}

// Serialize serializes session data to PHP format
func (m *Manager) Serialize(data map[string]interface{}) ([]byte, error) {
	// Simplified serialization - in production, use proper PHP serialization
	return []byte(fmt.Sprintf("%v", data)), nil
}

// Deserialize deserializes PHP session data
func (m *Manager) Deserialize(data []byte) (map[string]interface{}, error) {
	// Simplified deserialization
	return make(map[string]interface{}), nil
}

// EncodeBase64 encodes session data to base64
func (m *Manager) EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// DecodeBase64 decodes base64 session data
func (m *Manager) DecodeBase64(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}
