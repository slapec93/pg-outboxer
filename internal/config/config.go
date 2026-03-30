// Package config handles configuration loading and validation.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Default configuration values
const (
	// Source defaults
	DefaultOutboxTable  = "outbox"
	DefaultPollInterval = 500 * time.Millisecond
	DefaultBatchSize    = 100
	DefaultSlotName     = "pg_outboxer_slot"
	DefaultPublication  = "pg_outboxer_pub"

	// Publisher defaults
	DefaultPublisherTimeout = 10 * time.Second

	// Delivery defaults
	DefaultWorkers          = 4
	DefaultWorkerBufferSize = 10
	DefaultMaxRetries       = 10
	DefaultDeadLetterTable  = "outbox_dead_letter"

	// Observability defaults
	DefaultMetricsPort = 9090
	DefaultLogLevel    = "info"
	DefaultLogFormat   = "json"
)

// Config represents the complete application configuration
type Config struct {
	Source        SourceConfig        `mapstructure:"source"`
	Publishers    []PublisherConfig   `mapstructure:"publishers"`
	Delivery      DeliveryConfig      `mapstructure:"delivery"`
	Observability ObservabilityConfig `mapstructure:"observability"`
}

// SourceConfig configures the event source (polling or CDC)
type SourceConfig struct {
	Type     string `mapstructure:"type"` // "cdc" or "polling"
	DSN      string `mapstructure:"dsn"`
	Table    string `mapstructure:"table"`
	Database string `mapstructure:"database"` // optional, extracted from DSN if not set

	// CDC-specific
	SlotName    string `mapstructure:"slot_name"`
	Publication string `mapstructure:"publication"`

	// Polling-specific
	PollInterval time.Duration `mapstructure:"poll_interval"`
	BatchSize    int           `mapstructure:"batch_size"`
}

// PublisherConfig configures the event publisher
type PublisherConfig struct {
	Name    string            `mapstructure:"name"` // Publisher name (e.g., "stripe", "chartmogul")
	Type    string            `mapstructure:"type"` // "webhook", "redis_stream", "kafka"
	URL     string            `mapstructure:"url"`
	Timeout time.Duration     `mapstructure:"timeout"`
	Headers map[string]string `mapstructure:"headers"`

	// Webhook-specific
	SigningSecret string `mapstructure:"signing_secret"`

	// Redis-specific
	StreamName string `mapstructure:"stream_name"`

	// Kafka-specific
	Topic   string   `mapstructure:"topic"`
	Brokers []string `mapstructure:"brokers"`
}

// DeliveryConfig configures delivery behavior
type DeliveryConfig struct {
	Workers          int    `mapstructure:"workers"`
	WorkerBufferSize int    `mapstructure:"worker_buffer_size"`
	MaxRetries       int    `mapstructure:"max_retries"`
	DeadLetterTable  string `mapstructure:"dead_letter_table"`
}

// ObservabilityConfig configures logging and metrics
type ObservabilityConfig struct {
	MetricsPort int    `mapstructure:"metrics_port"`
	LogLevel    string `mapstructure:"log_level"`
	LogFormat   string `mapstructure:"log_format"` // "json" or "text"
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	// Enable environment variable interpolation
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Set defaults
	cfg.setDefaults()

	return &cfg, nil
}

// setDefaults applies default values for optional fields
func (c *Config) setDefaults() {
	// Source defaults
	if c.Source.Table == "" {
		c.Source.Table = DefaultOutboxTable
	}
	if c.Source.Type == "polling" {
		if c.Source.PollInterval == 0 {
			c.Source.PollInterval = DefaultPollInterval
		}
		if c.Source.BatchSize == 0 {
			c.Source.BatchSize = DefaultBatchSize
		}
	}
	if c.Source.Type == "cdc" {
		if c.Source.SlotName == "" {
			c.Source.SlotName = DefaultSlotName
		}
		if c.Source.Publication == "" {
			c.Source.Publication = DefaultPublication
		}
	}

	// Publisher defaults
	for i := range c.Publishers {
		if c.Publishers[i].Timeout == 0 {
			c.Publishers[i].Timeout = DefaultPublisherTimeout
		}
		// Default name if not provided
		if c.Publishers[i].Name == "" {
			c.Publishers[i].Name = fmt.Sprintf("publisher-%d", i+1)
		}
	}

	// Delivery defaults
	if c.Delivery.Workers == 0 {
		c.Delivery.Workers = DefaultWorkers
	}
	if c.Delivery.WorkerBufferSize == 0 {
		c.Delivery.WorkerBufferSize = DefaultWorkerBufferSize
	}
	if c.Delivery.MaxRetries == 0 {
		c.Delivery.MaxRetries = DefaultMaxRetries
	}
	if c.Delivery.DeadLetterTable == "" {
		c.Delivery.DeadLetterTable = DefaultDeadLetterTable
	}

	// Observability defaults
	if c.Observability.MetricsPort == 0 {
		c.Observability.MetricsPort = DefaultMetricsPort
	}
	if c.Observability.LogLevel == "" {
		c.Observability.LogLevel = DefaultLogLevel
	}
	if c.Observability.LogFormat == "" {
		c.Observability.LogFormat = DefaultLogFormat
	}
}

// Validate checks that the configuration is valid
func (c *Config) Validate() error {
	// Validate source
	if c.Source.Type != "cdc" && c.Source.Type != "polling" {
		return fmt.Errorf("source.type must be 'cdc' or 'polling', got: %s", c.Source.Type)
	}
	if c.Source.DSN == "" {
		return fmt.Errorf("source.dsn is required")
	}

	// Validate CDC-specific fields
	if c.Source.Type == "cdc" {
		if c.Source.SlotName == "" {
			return fmt.Errorf("source.slot_name is required for CDC mode")
		}
		if c.Source.Publication == "" {
			return fmt.Errorf("source.publication is required for CDC mode")
		}
	}

	// Validate publishers
	if len(c.Publishers) == 0 {
		return fmt.Errorf("at least one publisher must be configured")
	}

	validPublisherTypes := []string{"webhook", "redis_stream", "kafka"}
	publisherNames := make(map[string]bool)

	for i, pub := range c.Publishers {
		// Check for duplicate names
		if publisherNames[pub.Name] {
			return fmt.Errorf("duplicate publisher name: %s", pub.Name)
		}
		publisherNames[pub.Name] = true

		// Validate type
		if !contains(validPublisherTypes, pub.Type) {
			return fmt.Errorf("publishers[%d].type must be one of %v, got: %s",
				i, validPublisherTypes, pub.Type)
		}

		// Publisher-specific validation
		switch pub.Type {
		case "webhook":
			if pub.URL == "" {
				return fmt.Errorf("publishers[%d].url is required for webhook publisher", i)
			}
		case "redis_stream":
			if pub.URL == "" {
				return fmt.Errorf("publishers[%d].url is required for redis_stream publisher", i)
			}
			if pub.StreamName == "" {
				return fmt.Errorf("publishers[%d].stream_name is required for redis_stream publisher", i)
			}
		case "kafka":
			if pub.Topic == "" {
				return fmt.Errorf("publishers[%d].topic is required for kafka publisher", i)
			}
			if len(pub.Brokers) == 0 {
				return fmt.Errorf("publishers[%d].brokers is required for kafka publisher", i)
			}
		}
	}

	// Validate delivery
	if c.Delivery.Workers < 1 {
		return fmt.Errorf("delivery.workers must be >= 1, got: %d", c.Delivery.Workers)
	}

	// Validate observability
	validLogLevels := []string{"debug", "info", "warn", "error"}
	if !contains(validLogLevels, c.Observability.LogLevel) {
		return fmt.Errorf("observability.log_level must be one of %v, got: %s",
			validLogLevels, c.Observability.LogLevel)
	}

	validLogFormats := []string{"json", "text"}
	if !contains(validLogFormats, c.Observability.LogFormat) {
		return fmt.Errorf("observability.log_format must be one of %v, got: %s",
			validLogFormats, c.Observability.LogFormat)
	}

	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
