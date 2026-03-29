package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var (
	cfgFile  string
	logLevel string
)

var rootCmd = &cobra.Command{
	Use:   "pg-outboxer",
	Short: "Transactional outbox relay for PostgreSQL",
	Long: `pg-outboxer is a lightweight sidecar that implements the
Transactional Outbox Pattern for PostgreSQL.

Delivers events to webhooks, Redis Streams, or Kafka with at-least-once
guarantees and ordered delivery per aggregate.

No Kafka required. No JVM required. Just a single binary.`,
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "config.yaml",
		"config file path")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info",
		"log level (debug|info|warn|error)")

	cobra.OnInitialize(initLogging)
}

func initLogging() {
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(logger)
}
