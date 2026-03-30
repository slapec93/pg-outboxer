package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/slapec93/pg-outboxer/internal/delivery"
	"github.com/slapec93/pg-outboxer/internal/metrics"
	"github.com/slapec93/pg-outboxer/internal/publisher"
	"github.com/slapec93/pg-outboxer/internal/publisher/webhook"
	"github.com/slapec93/pg-outboxer/internal/source"
	"github.com/slapec93/pg-outboxer/internal/source/cdc"
	"github.com/slapec93/pg-outboxer/internal/source/polling"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the outbox relay daemon",
	Long: `Starts the pg-outboxer relay daemon that reads from the outbox table
and publishes events to the configured destination (webhook, Redis, Kafka).

The daemon runs until interrupted (Ctrl+C) or receives a termination signal.`,
	RunE: runDaemon,
}

func init() {
	rootCmd.AddCommand(runCmd)
}

func runDaemon(*cobra.Command, []string) error {
	slog.Info("starting pg-outboxer", "config", cfgFile)

	// Load and validate config
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	slog.Info("configuration loaded",
		"source_type", cfg.Source.Type,
		"num_publishers", len(cfg.Publishers),
		"workers", cfg.Delivery.Workers)

	// Setup context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Initialize source
	src, err := initSource(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize source: %w", err)
	}
	defer func() { _ = src.Close() }()
	slog.Info("source initialized", "type", cfg.Source.Type)

	// Initialize publishers
	pubs, err := initPublishers(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize publishers: %w", err)
	}
	defer closePublishers(pubs)
	slog.Info("publishers initialized", "count", len(pubs))

	// Wrap publishers in multi-publisher
	multiPub := publisher.NewMulti(pubs)

	// Initialize dispatcher
	dispatcher := delivery.New(
		src,
		multiPub,
		cfg.Delivery.Workers,
		cfg.Delivery.WorkerBufferSize,
	)
	slog.Info("dispatcher initialized",
		"workers", cfg.Delivery.Workers,
		"buffer_size", cfg.Delivery.WorkerBufferSize)

	// Start metrics server
	metricsServer := metrics.NewServer(cfg.Observability.MetricsPort)
	go func() {
		if err := metricsServer.Start(); err != nil {
			slog.Error("metrics server error", "error", err)
		}
	}()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("failed to shutdown metrics server", "error", err)
		}
	}()
	slog.Info("metrics server started", "port", cfg.Observability.MetricsPort)

	slog.Info("pg-outboxer started successfully")

	// Start the relay (blocks until context is cancelled)
	if err := dispatcher.Start(ctx); err != nil && err != context.Canceled {
		return fmt.Errorf("dispatcher error: %w", err)
	}

	slog.Info("shutdown complete")

	return nil
}

// initSource creates and initializes the event source based on config
func initSource(cfg *config.Config) (source.Source, error) {
	switch cfg.Source.Type {
	case "polling":
		return polling.New(cfg)
	case "cdc":
		return cdc.New(cfg)
	default:
		return nil, fmt.Errorf("unknown source type: %s", cfg.Source.Type)
	}
}

// initPublishers creates all configured publishers
func initPublishers(cfg *config.Config) ([]publisher.Publisher, error) {
	publishers := make([]publisher.Publisher, 0, len(cfg.Publishers))

	for i, pubCfg := range cfg.Publishers {
		pub, err := initPublisher(&pubCfg)
		if err != nil {
			// Close already initialized publishers on error
			closePublishers(publishers)
			return nil, fmt.Errorf("failed to initialize publisher[%d] (%s): %w", i, pubCfg.Name, err)
		}
		publishers = append(publishers, pub)
	}

	return publishers, nil
}

// initPublisher creates a single publisher based on its type
func initPublisher(cfg *config.PublisherConfig) (publisher.Publisher, error) {
	switch cfg.Type {
	case "webhook":
		return webhook.New(cfg)
	case "redis_stream":
		return nil, fmt.Errorf("redis_stream publisher not yet implemented")
	case "kafka":
		return nil, fmt.Errorf("kafka publisher not yet implemented")
	default:
		return nil, fmt.Errorf("unknown publisher type: %s", cfg.Type)
	}
}

// closePublishers closes all publishers, logging any errors
func closePublishers(publishers []publisher.Publisher) {
	for _, pub := range publishers {
		if err := pub.Close(); err != nil {
			slog.Error("failed to close publisher",
				"name", pub.Name(),
				"error", err)
		}
	}
}
