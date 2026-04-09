package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/sonroyaalmerol/phpcloud/internal/fpm"
	"github.com/sonroyaalmerol/phpcloud/internal/session"
	"github.com/yookoala/gofast"
	"go.uber.org/zap"
)

// Server is the HTTP gateway that forwards requests to PHP-FPM
type Server struct {
	config      *config.Config
	fpmManager  *fpm.Manager
	sessionMgr  *session.Manager
	logger      *zap.Logger
	server      *http.Server
	internalMux *http.ServeMux
	phpHandler  http.Handler
}

// New creates a new gateway server
func New(cfg *config.Config, fpmMgr *fpm.Manager, sessionMgr *session.Manager, logger *zap.Logger) (*Server, error) {
	s := &Server{
		config:      cfg,
		fpmManager:  fpmMgr,
		sessionMgr:  sessionMgr,
		logger:      logger,
		internalMux: http.NewServeMux(),
	}

	// Only create PHP handler if FPM is enabled
	if cfg.PHPFPM.Enabled {
		// Create FastCGI handler
		network, address := cfg.PHPFPM.ParseSocket()

		// Create gofast client factory and handler
		connFactory := gofast.SimpleConnFactory(network, address)
		clientFactory := gofast.SimpleClientFactory(connFactory)

		// Create the PHP file handler with middleware chain
		// NewPHPFS middleware handles PHP file mapping
		sessionHandler := gofast.Chain(
			gofast.NewPHPFS(cfg.StaticFiles.Root),
		)(gofast.BasicSession)

		s.phpHandler = gofast.NewHandler(sessionHandler, clientFactory)
	}

	return s, nil
}

// Start starts the HTTP server
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.Server.HTTPPort)

	// Main handler
	handler := s.createHandler()

	s.server = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  s.config.Server.ReadTimeout,
		WriteTimeout: s.config.Server.WriteTimeout,
	}

	s.logger.Info("Starting HTTP gateway", zap.String("addr", addr))

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop stops the HTTP server
func (s *Server) Stop() error {
	if s.server != nil {
		s.logger.Info("Stopping HTTP gateway...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}

// RegisterInternalHandler registers a handler for internal endpoints
func (s *Server) RegisterInternalHandler(path string, handler http.HandlerFunc) {
	s.internalMux.HandleFunc(path, handler)
}

// createHandler creates the main HTTP handler with middleware
func (s *Server) createHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Add request ID
		r = s.addRequestID(r)

		// Log request
		s.logger.Debug("HTTP request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("remote", r.RemoteAddr),
		)

		// Check for internal endpoints
		if strings.HasPrefix(r.URL.Path, "/phpcloud/") {
			s.internalMux.ServeHTTP(w, r)
			return
		}

		// Check for static files
		if s.isStaticFile(r.URL.Path) {
			s.serveStatic(w, r)
			return
		}

		// Forward to PHP-FPM
		s.servePHP(w, r)

		// Log duration
		duration := time.Since(start)
		s.logger.Debug("Request completed",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Duration("duration", duration),
		)
	})
}

// addRequestID adds a unique request ID to the context
func (s *Server) addRequestID(r *http.Request) *http.Request {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = generateRequestID()
		r.Header.Set("X-Request-ID", requestID)
	}
	return r
}

// isStaticFile checks if the request is for a static file
func (s *Server) isStaticFile(path string) bool {
	if !s.config.StaticFiles.Enabled {
		return false
	}

	ext := filepath.Ext(path)
	for _, allowedExt := range s.config.StaticFiles.Extensions {
		if ext == allowedExt {
			return true
		}
	}
	return false
}

// serveStatic serves static files directly
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.config.StaticFiles.Root, r.URL.Path)

	// Security check: prevent directory traversal
	if !strings.HasPrefix(path, s.config.StaticFiles.Root) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	// Serve the file
	http.ServeFile(w, r, path)
}

// servePHP forwards the request to PHP-FPM
func (s *Server) servePHP(w http.ResponseWriter, r *http.Request) {
	// Check if PHP handler is available
	if s.phpHandler == nil {
		http.Error(w, "PHP processing disabled", http.StatusServiceUnavailable)
		return
	}

	if s.fpmManager == nil || !s.fpmManager.IsRunning() {
		http.Error(w, "PHP-FPM not available", http.StatusServiceUnavailable)
		return
	}

	// Build FastCGI parameters
	params := s.buildFastCGIParams(r)

	// Handle session
	if s.sessionMgr != nil {
		sessionID := s.sessionMgr.ExtractSessionID(r)
		if sessionID != "" {
			params["PHPSESSID"] = sessionID
		}
	}

	// Forward to PHP-FPM
	s.phpHandler.ServeHTTP(w, r)
}

// buildFastCGIParams builds FastCGI parameters from the HTTP request
func (s *Server) buildFastCGIParams(r *http.Request) map[string]string {
	params := make(map[string]string)

	// Standard CGI variables
	params["REQUEST_METHOD"] = r.Method
	params["REQUEST_URI"] = r.URL.RequestURI()
	params["SCRIPT_NAME"] = r.URL.Path
	params["PATH_INFO"] = ""
	params["QUERY_STRING"] = r.URL.RawQuery
	params["SERVER_NAME"] = r.Host
	params["SERVER_PORT"] = "8080"
	params["SERVER_PROTOCOL"] = r.Proto
	params["CONTENT_TYPE"] = r.Header.Get("Content-Type")
	params["CONTENT_LENGTH"] = r.Header.Get("Content-Length")
	params["REMOTE_ADDR"] = r.RemoteAddr
	params["REMOTE_PORT"] = "0"
	params["DOCUMENT_ROOT"] = s.config.StaticFiles.Root
	params["SCRIPT_FILENAME"] = filepath.Join(s.config.StaticFiles.Root, r.URL.Path)

	// HTTP headers
	for name, values := range r.Header {
		headerName := "HTTP_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		params[headerName] = values[0]
	}

	// PHP configuration
	params["PHP_VALUE"] = s.buildPHPValue()

	return params
}

// buildPHPValue builds the PHP_VALUE string with configuration overrides
func (s *Server) buildPHPValue() string {
	values := make([]string, 0)

	// Add session configuration
	if s.config.Session.Enabled {
		values = append(values, "session.save_handler=user")
		values = append(values, fmt.Sprintf("session.name=%s", s.config.Session.CookieName))
	}

	// Add configured overrides
	for key, val := range s.config.PHPFPM.PHPINIOverrides {
		values = append(values, fmt.Sprintf("%s=%s", key, val))
	}

	return strings.Join(values, "\n")
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
}
