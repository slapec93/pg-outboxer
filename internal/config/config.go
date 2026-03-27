package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config represents the complete application configuration
type Config struct {
	Source        SourceConfig        `mapstructure:"source"`
	Publisher     PublisherConfig     `mapstructure:"publisher"`
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
		c.Source.Table = "outbox"
	}
	if c.Source.Type == "polling" {
		if c.Source.PollInterval == 0 {
			c.Source.PollInterval = 500 * time.Millisecond
		}
		if c.Source.BatchSize == 0 {
			c.Source.BatchSize = 100
		}
	}
	if c.Source.Type == "cdc" {
		if c.Source.SlotName == "" {
			c.Source.SlotName = "pg_outboxer_slot"
		}
		if c.Source.Publication == "" {
			c.Source.Publication = "pg_outboxer_pub"
		}
	}

	// Publisher defaults
	if c.Publisher.Timeout == 0 {
		c.Publisher.Timeout = 10 * time.Second
	}

	// Delivery defaults
	if c.Delivery.Workers == 0 {
		c.Delivery.Workers = 4
	}
	if c.Delivery.MaxRetries == 0 {
		c.Delivery.MaxRetries = 10
	}
	if c.Delivery.DeadLetterTable == "" {
		c.Delivery.DeadLetterTable = "outbox_dead_letter"
	}

	// Observability defaults
	if c.Observability.MetricsPort == 0 {
		c.Observability.MetricsPort = 9090
	}
	if c.Observability.LogLevel == "" {
		c.Observability.LogLevel = "info"
	}
	if c.Observability.LogFormat == "" {
		c.Observability.LogFormat = "json"
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

	// Validate publisher
	validPublisherTypes := []string{"webhook", "redis_stream", "kafka"}
	if !contains(validPublisherTypes, c.Publisher.Type) {
		return fmt.Errorf("publisher.type must be one of %v, got: %s",
			validPublisherTypes, c.Publisher.Type)
	}

	// Publisher-specific validation
	switch c.Publisher.Type {
	case "webhook":
		if c.Publisher.URL == "" {
			return fmt.Errorf("publisher.url is required for webhook publisher")
		}
	case "redis_stream":
		if c.Publisher.URL == "" {
			return fmt.Errorf("publisher.url is required for redis_stream publisher")
		}
		if c.Publisher.StreamName == "" {
			return fmt.Errorf("publisher.stream_name is required for redis_stream publisher")
		}
	case "kafka":
		if c.Publisher.Topic == "" {
			return fmt.Errorf("publisher.topic is required for kafka publisher")
		}
		if len(c.Publisher.Brokers) == 0 {
			return fmt.Errorf("publisher.brokers is required for kafka publisher")
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
