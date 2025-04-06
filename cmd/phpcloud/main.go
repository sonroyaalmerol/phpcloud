package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sonroyaalmerol/phpcloud/internal/bootstrap"
	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	configPath string
	logLevel   string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "phpcloud",
		Short: "PHPCloud Runtime - Drop-in PHP runtime for Kubernetes",
		Long: `PHPCloud is a Go-based runtime shim for legacy PHP applications.
It replaces Apache/nginx+mod_php as the entry point in a Docker container,
manages PHP-FPM as a subprocess, and provides horizontal scalability features.`,
		RunE: run,
	}

	rootCmd.PersistentFlags().StringVar(&configPath, "config", "/phpcloud/phpcloud.yaml", "Path to configuration file")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	// Initialize logger
	logger, err := initLogger(logLevel)
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer func() {
		_ = logger.Sync()
	}()

	// Load configuration
	cfg, err := config.Load(configPath, logger)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Info("PHPCloud Runtime starting",
		zap.String("version", "0.1.0"),
		zap.String("profile", cfg.AppProfile),
	)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	// Initialize and start the runtime
	engine, err := bootstrap.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize engine: %w", err)
	}

	// Start in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- engine.Start(ctx)
	}()

	// Wait for signals or errors
	for {
		select {
		case sig := <-sigChan:
			logger.Info("Received signal", zap.String("signal", sig.String()))

			switch sig {
			case syscall.SIGHUP:
				// Reload configuration
				if err := engine.Reload(); err != nil {
					logger.Error("Failed to reload", zap.Error(err))
				}
			default:
				// Graceful shutdown
				logger.Info("Shutting down gracefully...")
				cancel()
				return nil
			}

		case err := <-errChan:
			if err != nil {
				return fmt.Errorf("engine error: %w", err)
			}
			return nil
		}
	}
}

func initLogger(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()

	switch level {
	case "debug":
		cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		cfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	}

	return cfg.Build()
}
