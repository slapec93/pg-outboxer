package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				assert.Equal(t, DefaultOutboxTable, c.Source.Table, "should set default table")
				assert.Equal(t, DefaultPollInterval, c.Source.PollInterval, "should set default poll interval")
				assert.Equal(t, DefaultBatchSize, c.Source.BatchSize, "should set default batch size")
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
				assert.Equal(t, DefaultSlotName, c.Source.SlotName, "should set default slot name")
				assert.Equal(t, DefaultPublication, c.Source.Publication, "should set default publication")
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
					assert.Equal(t, DefaultPublisherTimeout, pub.Timeout, "publisher[%d] should have default timeout", i)
					expectedName := "publisher-1"
					if i == 1 {
						expectedName = "publisher-2"
					}
					assert.Equal(t, expectedName, pub.Name, "publisher[%d] should have auto-generated name", i)
				}
			},
		},
		{
			name: "delivery defaults",
			config: &Config{
				Source: SourceConfig{Type: "polling", DSN: "test"},
			},
			verify: func(t *testing.T, c *Config) {
				assert.Equal(t, DefaultWorkers, c.Delivery.Workers, "should set default workers")
				assert.Equal(t, DefaultWorkerBufferSize, c.Delivery.WorkerBufferSize, "should set default worker buffer size")
				assert.Equal(t, DefaultMaxRetries, c.Delivery.MaxRetries, "should set default max retries")
				assert.Equal(t, DefaultDeadLetterTable, c.Delivery.DeadLetterTable, "should set default dead letter table")
			},
		},
		{
			name: "observability defaults",
			config: &Config{
				Source: SourceConfig{Type: "polling", DSN: "test"},
			},
			verify: func(t *testing.T, c *Config) {
				assert.Equal(t, DefaultMetricsPort, c.Observability.MetricsPort, "should set default metrics port")
				assert.Equal(t, DefaultLogLevel, c.Observability.LogLevel, "should set default log level")
				assert.Equal(t, DefaultLogFormat, c.Observability.LogFormat, "should set default log format")
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
				assert.Equal(t, "custom_outbox", c.Source.Table, "custom table should be preserved")
				assert.Equal(t, 1*time.Second, c.Source.PollInterval, "custom poll interval should be preserved")
				assert.Equal(t, 50, c.Source.BatchSize, "custom batch size should be preserved")
				assert.Equal(t, 8, c.Delivery.Workers, "custom workers should be preserved")
				assert.Equal(t, 20, c.Delivery.WorkerBufferSize, "custom buffer size should be preserved")
				assert.Equal(t, 5, c.Delivery.MaxRetries, "custom max retries should be preserved")
				assert.Equal(t, "custom_dlq", c.Delivery.DeadLetterTable, "custom dead letter table should be preserved")
				assert.Equal(t, 8080, c.Observability.MetricsPort, "custom metrics port should be preserved")
				assert.Equal(t, "debug", c.Observability.LogLevel, "custom log level should be preserved")
				assert.Equal(t, "text", c.Observability.LogFormat, "custom log format should be preserved")
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
				require.Error(t, err, "should return validation error")
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg, "error should contain expected message")
				}
			} else {
				assert.NoError(t, err, "should not return error for valid config")
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
	require.NoError(t, err, "should create temp file")
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString(content)
	require.NoError(t, err, "should write config")
	_ = tmpFile.Close()

	// Load config
	cfg, err := Load(tmpFile.Name())
	require.NoError(t, err, "should load config")

	// Verify loaded values
	assert.Equal(t, "polling", cfg.Source.Type, "should load source type")
	assert.Equal(t, 1*time.Second, cfg.Source.PollInterval, "should load poll interval")
	assert.Equal(t, 50, cfg.Source.BatchSize, "should load batch size")
	require.Len(t, cfg.Publishers, 1, "should have 1 publisher")
	assert.Equal(t, "test-webhook", cfg.Publishers[0].Name, "should load publisher name")
	assert.Equal(t, 2, cfg.Delivery.Workers, "should load workers")
	assert.Equal(t, 5, cfg.Delivery.WorkerBufferSize, "should load worker buffer size")
	assert.Equal(t, "debug", cfg.Observability.LogLevel, "should load log level")
}
