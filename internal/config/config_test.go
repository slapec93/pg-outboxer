package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfig_SetDefaults(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		verify func(*testing.T, *Config)
	}{
		{
			name: "source polling defaults",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
					DSN:  "postgres://localhost/test",
				},
			},
			verify: func(t *testing.T, c *Config) {
				if c.Source.Table != DefaultOutboxTable {
					t.Errorf("expected table=%s, got %s", DefaultOutboxTable, c.Source.Table)
				}
				if c.Source.PollInterval != DefaultPollInterval {
					t.Errorf("expected poll_interval=%v, got %v", DefaultPollInterval, c.Source.PollInterval)
				}
				if c.Source.BatchSize != DefaultBatchSize {
					t.Errorf("expected batch_size=%d, got %d", DefaultBatchSize, c.Source.BatchSize)
				}
			},
		},
		{
			name: "source cdc defaults",
			config: &Config{
				Source: SourceConfig{
					Type: "cdc",
					DSN:  "postgres://localhost/test",
				},
			},
			verify: func(t *testing.T, c *Config) {
				if c.Source.SlotName != DefaultSlotName {
					t.Errorf("expected slot_name=%s, got %s", DefaultSlotName, c.Source.SlotName)
				}
				if c.Source.Publication != DefaultPublication {
					t.Errorf("expected publication=%s, got %s", DefaultPublication, c.Source.Publication)
				}
			},
		},
		{
			name: "publisher defaults",
			config: &Config{
				Publishers: []PublisherConfig{
					{Type: "webhook", URL: "http://example.com"},
					{Type: "webhook", URL: "http://example2.com"},
				},
			},
			verify: func(t *testing.T, c *Config) {
				for i, pub := range c.Publishers {
					if pub.Timeout != DefaultPublisherTimeout {
						t.Errorf("publisher[%d]: expected timeout=%v, got %v", i, DefaultPublisherTimeout, pub.Timeout)
					}
					expectedName := "publisher-1"
					if i == 1 {
						expectedName = "publisher-2"
					}
					if pub.Name != expectedName {
						t.Errorf("publisher[%d]: expected name=%s, got %s", i, expectedName, pub.Name)
					}
				}
			},
		},
		{
			name: "delivery defaults",
			config: &Config{
				Source: SourceConfig{Type: "polling", DSN: "test"},
			},
			verify: func(t *testing.T, c *Config) {
				if c.Delivery.Workers != DefaultWorkers {
					t.Errorf("expected workers=%d, got %d", DefaultWorkers, c.Delivery.Workers)
				}
				if c.Delivery.WorkerBufferSize != DefaultWorkerBufferSize {
					t.Errorf("expected worker_buffer_size=%d, got %d", DefaultWorkerBufferSize, c.Delivery.WorkerBufferSize)
				}
				if c.Delivery.MaxRetries != DefaultMaxRetries {
					t.Errorf("expected max_retries=%d, got %d", DefaultMaxRetries, c.Delivery.MaxRetries)
				}
				if c.Delivery.DeadLetterTable != DefaultDeadLetterTable {
					t.Errorf("expected dead_letter_table=%s, got %s", DefaultDeadLetterTable, c.Delivery.DeadLetterTable)
				}
			},
		},
		{
			name: "observability defaults",
			config: &Config{
				Source: SourceConfig{Type: "polling", DSN: "test"},
			},
			verify: func(t *testing.T, c *Config) {
				if c.Observability.MetricsPort != DefaultMetricsPort {
					t.Errorf("expected metrics_port=%d, got %d", DefaultMetricsPort, c.Observability.MetricsPort)
				}
				if c.Observability.LogLevel != DefaultLogLevel {
					t.Errorf("expected log_level=%s, got %s", DefaultLogLevel, c.Observability.LogLevel)
				}
				if c.Observability.LogFormat != DefaultLogFormat {
					t.Errorf("expected log_format=%s, got %s", DefaultLogFormat, c.Observability.LogFormat)
				}
			},
		},
		{
			name: "custom values not overridden",
			config: &Config{
				Source: SourceConfig{
					Type:         "polling",
					DSN:          "postgres://localhost/test",
					Table:        "custom_outbox",
					PollInterval: 1 * time.Second,
					BatchSize:    50,
				},
				Delivery: DeliveryConfig{
					Workers:          8,
					WorkerBufferSize: 20,
					MaxRetries:       5,
					DeadLetterTable:  "custom_dlq",
				},
				Observability: ObservabilityConfig{
					MetricsPort: 8080,
					LogLevel:    "debug",
					LogFormat:   "text",
				},
			},
			verify: func(t *testing.T, c *Config) {
				// Verify custom values are preserved
				if c.Source.Table != "custom_outbox" {
					t.Errorf("expected custom table to be preserved")
				}
				if c.Source.PollInterval != 1*time.Second {
					t.Errorf("expected custom poll interval to be preserved")
				}
				if c.Source.BatchSize != 50 {
					t.Errorf("expected custom batch size to be preserved")
				}
				if c.Delivery.Workers != 8 {
					t.Errorf("expected custom workers to be preserved")
				}
				if c.Delivery.WorkerBufferSize != 20 {
					t.Errorf("expected custom buffer size to be preserved")
				}
				if c.Delivery.MaxRetries != 5 {
					t.Errorf("expected custom max retries to be preserved")
				}
				if c.Delivery.DeadLetterTable != "custom_dlq" {
					t.Errorf("expected custom dead letter table to be preserved")
				}
				if c.Observability.MetricsPort != 8080 {
					t.Errorf("expected custom metrics port to be preserved")
				}
				if c.Observability.LogLevel != "debug" {
					t.Errorf("expected custom log level to be preserved")
				}
				if c.Observability.LogFormat != "text" {
					t.Errorf("expected custom log format to be preserved")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.setDefaults()
			tt.verify(t, tt.config)
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid polling config",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
					DSN:  "postgres://localhost/test",
				},
				Publishers: []PublisherConfig{
					{Name: "webhook", Type: "webhook", URL: "http://example.com"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid cdc config",
			config: &Config{
				Source: SourceConfig{
					Type:        "cdc",
					DSN:         "postgres://localhost/test",
					SlotName:    "test_slot",
					Publication: "test_pub",
				},
				Publishers: []PublisherConfig{
					{Name: "webhook", Type: "webhook", URL: "http://example.com"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid source type",
			config: &Config{
				Source: SourceConfig{
					Type: "invalid",
					DSN:  "postgres://localhost/test",
				},
			},
			wantErr: true,
			errMsg:  "source.type must be 'cdc' or 'polling'",
		},
		{
			name: "missing dsn",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
				},
			},
			wantErr: true,
			errMsg:  "source.dsn is required",
		},
		{
			name: "no publishers",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
					DSN:  "postgres://localhost/test",
				},
				Publishers: []PublisherConfig{},
			},
			wantErr: true,
			errMsg:  "at least one publisher must be configured",
		},
		{
			name: "duplicate publisher names",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
					DSN:  "postgres://localhost/test",
				},
				Publishers: []PublisherConfig{
					{Name: "webhook", Type: "webhook", URL: "http://example.com"},
					{Name: "webhook", Type: "webhook", URL: "http://example2.com"},
				},
			},
			wantErr: true,
			errMsg:  "duplicate publisher name",
		},
		{
			name: "invalid publisher type",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
					DSN:  "postgres://localhost/test",
				},
				Publishers: []PublisherConfig{
					{Name: "test", Type: "invalid", URL: "http://example.com"},
				},
			},
			wantErr: true,
			errMsg:  "publishers[0].type must be one of",
		},
		{
			name: "webhook without url",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
					DSN:  "postgres://localhost/test",
				},
				Publishers: []PublisherConfig{
					{Name: "test", Type: "webhook"},
				},
			},
			wantErr: true,
			errMsg:  "publishers[0].url is required",
		},
		{
			name: "invalid workers count",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
					DSN:  "postgres://localhost/test",
				},
				Publishers: []PublisherConfig{
					{Name: "test", Type: "webhook", URL: "http://example.com"},
				},
				Delivery: DeliveryConfig{
					Workers: -1, // Negative to bypass defaults
				},
			},
			wantErr: true,
			errMsg:  "delivery.workers must be >= 1",
		},
		{
			name: "invalid log level",
			config: &Config{
				Source: SourceConfig{
					Type: "polling",
					DSN:  "postgres://localhost/test",
				},
				Publishers: []PublisherConfig{
					{Name: "test", Type: "webhook", URL: "http://example.com"},
				},
				Observability: ObservabilityConfig{
					LogLevel: "invalid",
				},
			},
			wantErr: true,
			errMsg:  "observability.log_level must be one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply defaults first
			tt.config.setDefaults()

			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.errMsg)
				} else if tt.errMsg != "" {
					// Check if error message contains expected substring
					if !strings.Contains(err.Error(), tt.errMsg) {
						t.Errorf("expected error containing '%s', got '%s'", tt.errMsg, err.Error())
					}
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestConfig_LoadFromFile(t *testing.T) {
	// Create a temporary config file
	content := `
source:
  type: polling
  dsn: postgres://localhost/test
  poll_interval: 1s
  batch_size: 50

publishers:
  - name: test-webhook
    type: webhook
    url: https://example.com/webhook
    timeout: 5s

delivery:
  workers: 2
  worker_buffer_size: 5
  max_retries: 3

observability:
  metrics_port: 8080
  log_level: debug
  log_format: text
`

	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	tmpFile.Close()

	// Load config
	cfg, err := Load(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify loaded values
	if cfg.Source.Type != "polling" {
		t.Errorf("expected source.type=polling, got %s", cfg.Source.Type)
	}
	if cfg.Source.PollInterval != 1*time.Second {
		t.Errorf("expected poll_interval=1s, got %v", cfg.Source.PollInterval)
	}
	if cfg.Source.BatchSize != 50 {
		t.Errorf("expected batch_size=50, got %d", cfg.Source.BatchSize)
	}
	if len(cfg.Publishers) != 1 {
		t.Fatalf("expected 1 publisher, got %d", len(cfg.Publishers))
	}
	if cfg.Publishers[0].Name != "test-webhook" {
		t.Errorf("expected publisher name=test-webhook, got %s", cfg.Publishers[0].Name)
	}
	if cfg.Delivery.Workers != 2 {
		t.Errorf("expected workers=2, got %d", cfg.Delivery.Workers)
	}
	if cfg.Delivery.WorkerBufferSize != 5 {
		t.Errorf("expected worker_buffer_size=5, got %d", cfg.Delivery.WorkerBufferSize)
	}
	if cfg.Observability.LogLevel != "debug" {
		t.Errorf("expected log_level=debug, got %s", cfg.Observability.LogLevel)
	}
}
