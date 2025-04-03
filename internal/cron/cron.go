package cron

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"go.uber.org/zap"
)

// Manager handles scheduled cron jobs
type Manager struct {
	config *config.Config
	logger *zap.Logger
	cron   *cron.Cron
	jobs   map[string]cron.EntryID
}

// New creates a new cron manager
func New(cfg *config.Config, logger *zap.Logger) *Manager {
	return &Manager{
		config: cfg,
		logger: logger,
		cron:   cron.New(),
		jobs:   make(map[string]cron.EntryID),
	}
}

// Start starts the cron scheduler
func (m *Manager) Start() error {
	m.logger.Info("Starting cron scheduler...")

	// Register jobs from configuration
	for _, job := range m.config.Cron.Jobs {
		if err := m.registerJob(job); err != nil {
			m.logger.Error("Failed to register cron job",
				zap.String("name", job.Name),
				zap.Error(err),
			)
		}
	}

	// Start the cron scheduler
	m.cron.Start()

	m.logger.Info("Cron scheduler started", zap.Int("jobs", len(m.jobs)))
	return nil
}

// Stop stops the cron scheduler
func (m *Manager) Stop() {
	m.logger.Info("Stopping cron scheduler...")
	ctx := m.cron.Stop()
	<-ctx.Done()
}

// registerJob registers a single cron job
func (m *Manager) registerJob(job config.CronJob) error {
	m.logger.Info("Registering cron job",
		zap.String("name", job.Name),
		zap.String("schedule", job.Schedule),
		zap.String("type", job.Type),
	)

	var jobFunc func()

	switch job.Type {
	case "http":
		jobFunc = m.createHTTPJob(job)
	case "cli":
		jobFunc = m.createCLIJob(job)
	default:
		return fmt.Errorf("unknown job type: %s", job.Type)
	}

	entryID, err := m.cron.AddFunc(job.Schedule, jobFunc)
	if err != nil {
		return fmt.Errorf("failed to add cron job: %w", err)
	}

	m.jobs[job.Name] = entryID
	return nil
}

// createHTTPJob creates an HTTP-based cron job
func (m *Manager) createHTTPJob(job config.CronJob) func() {
	return func() {
		m.logger.Info("Running HTTP cron job", zap.String("name", job.Name))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", job.Path, nil)
		if err != nil {
			m.logger.Error("Failed to create HTTP request",
				zap.String("job", job.Name),
				zap.Error(err),
			)
			return
		}

		client := &http.Client{Timeout: 5 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			m.logger.Error("HTTP cron job failed",
				zap.String("job", job.Name),
				zap.Error(err),
			)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			m.logger.Info("HTTP cron job completed",
				zap.String("job", job.Name),
				zap.Int("status", resp.StatusCode),
			)
		} else {
			m.logger.Error("HTTP cron job returned error status",
				zap.String("job", job.Name),
				zap.Int("status", resp.StatusCode),
			)
		}
	}
}

// createCLIJob creates a CLI-based cron job
func (m *Manager) createCLIJob(job config.CronJob) func() {
	return func() {
		m.logger.Info("Running CLI cron job", zap.String("name", job.Name))

		if len(job.Command) == 0 {
			m.logger.Error("CLI job has no command", zap.String("job", job.Name))
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(ctx, job.Command[0], job.Command[1:]...)
		output, err := cmd.CombinedOutput()

		if err != nil {
			m.logger.Error("CLI cron job failed",
				zap.String("job", job.Name),
				zap.Error(err),
				zap.String("output", string(output)),
			)
		} else {
			m.logger.Info("CLI cron job completed",
				zap.String("job", job.Name),
				zap.String("output", string(output)),
			)
		}
	}
}

// ListJobs returns the list of registered jobs
func (m *Manager) ListJobs() []string {
	jobs := make([]string, 0, len(m.jobs))
	for name := range m.jobs {
		jobs = append(jobs, name)
	}
	return jobs
}
