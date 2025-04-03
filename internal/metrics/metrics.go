package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"go.uber.org/zap"
)

// Server handles Prometheus metrics
type Server struct {
	config *config.Config
	logger *zap.Logger
	server *http.Server

	// Metrics
	RequestsTotal    prometheus.Counter
	RequestDuration  prometheus.Histogram
	SessionOpsTotal  *prometheus.CounterVec
	FPMWorkersActive prometheus.Gauge
	ClusterMembers   prometheus.Gauge
	ClusterIsLeader  prometheus.Gauge
}

// New creates a new metrics server
func New(cfg *config.Config, logger *zap.Logger) *Server {
	s := &Server{
		config: cfg,
		logger: logger,
	}

	// Initialize metrics
	s.RequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "phpcloud_requests_total",
		Help: "Total number of HTTP requests",
	})

	s.RequestDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "phpcloud_request_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: prometheus.DefBuckets,
	})

	s.SessionOpsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "phpcloud_session_operations_total",
		Help: "Total session operations",
	}, []string{"operation"})

	s.FPMWorkersActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "phpcloud_fpm_workers_active",
		Help: "Number of active FPM workers",
	})

	s.ClusterMembers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "phpcloud_cluster_members_count",
		Help: "Number of cluster members",
	})

	s.ClusterIsLeader = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "phpcloud_cluster_is_leader",
		Help: "1 if this node is the cluster leader",
	})

	return s
}

// Start starts the metrics server
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.Server.MetricsPort)

	mux := http.NewServeMux()
	mux.Handle(s.config.Metrics.Path, promhttp.Handler())

	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	s.logger.Info("Starting metrics server", zap.String("addr", addr))

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Metrics server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop stops the metrics server
func (s *Server) Stop() error {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}

// RecordRequest records a request metric
func (s *Server) RecordRequest(duration time.Duration) {
	s.RequestsTotal.Inc()
	s.RequestDuration.Observe(duration.Seconds())
}

// RecordSessionOp records a session operation
func (s *Server) RecordSessionOp(op string) {
	s.SessionOpsTotal.WithLabelValues(op).Inc()
}

// SetFPMWorkers sets the active FPM workers count
func (s *Server) SetFPMWorkers(count int) {
	s.FPMWorkersActive.Set(float64(count))
}

// SetClusterMembers sets the cluster member count
func (s *Server) SetClusterMembers(count int) {
	s.ClusterMembers.Set(float64(count))
}

// SetIsLeader sets the leader status
func (s *Server) SetIsLeader(isLeader bool) {
	if isLeader {
		s.ClusterIsLeader.Set(1)
	} else {
		s.ClusterIsLeader.Set(0)
	}
}
