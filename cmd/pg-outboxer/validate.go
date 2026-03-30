package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/slapec93/pg-outboxer/internal/config"
)

var validateCmd = &cobra.Command{
	Use:   "validate-config",
	Short: "Validate configuration file",
	Long: `Validates the configuration file without starting the daemon.

Checks for:
- Valid YAML syntax
- Required fields present
- Valid enum values (source.type, publisher.type, etc.)
- Env var interpolation works`,
	RunE: validateConfig,
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

func validateConfig(*cobra.Command, []string) error {
	fmt.Printf("Validating config file: %s\n\n", cfgFile)

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("❌ Invalid config: %w", err)
	}

	// Basic validation
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("❌ Validation failed: %w", err)
	}

	// Print summary
	fmt.Println("✓ Config is valid")
	fmt.Println()
	fmt.Printf("Source:\n")
	fmt.Printf("  Type:  %s\n", cfg.Source.Type)
	fmt.Printf("  Table: %s\n", cfg.Source.Table)

	if cfg.Source.Type == "cdc" {
		fmt.Printf("  Slot:  %s\n", cfg.Source.SlotName)
		fmt.Printf("  Pub:   %s\n", cfg.Source.Publication)
	} else {
		fmt.Printf("  Poll:  %s\n", cfg.Source.PollInterval)
		fmt.Printf("  Batch: %d\n", cfg.Source.BatchSize)
	}

	fmt.Println()
	fmt.Printf("Publishers (%d):\n", len(cfg.Publishers))
	for i, pub := range cfg.Publishers {
		fmt.Printf("  [%d] %s:\n", i+1, pub.Name)
		fmt.Printf("      Type:    %s\n", pub.Type)
		if pub.URL != "" {
			fmt.Printf("      URL:     %s\n", pub.URL)
		}
		fmt.Printf("      Timeout: %s\n", pub.Timeout)
	}

	fmt.Println()
	fmt.Printf("Delivery:\n")
	fmt.Printf("  Workers:     %d\n", cfg.Delivery.Workers)
	fmt.Printf("  Max retries: %d\n", cfg.Delivery.MaxRetries)

	fmt.Println()
	fmt.Printf("Observability:\n")
	fmt.Printf("  Metrics port: %d\n", cfg.Observability.MetricsPort)
	fmt.Printf("  Log level:    %s\n", cfg.Observability.LogLevel)
	fmt.Printf("  Log format:   %s\n", cfg.Observability.LogFormat)

	return nil
}
