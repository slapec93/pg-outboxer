package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/slapec93/pg-outboxer/internal/config"
)

func TestInitSource_Polling(t *testing.T) {
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:         "polling",
			DSN:          "postgres://localhost/test",
			Table:        "outbox",
			PollInterval: 500 * time.Millisecond,
			BatchSize:    100,
		},
		Delivery: config.DeliveryConfig{
			DeadLetterTable: "outbox_dead_letter",
			MaxRetries:      10,
		},
	}

	// Note: This will fail if no database is available, but that's expected
	// The test verifies the code path and proper error handling
	_, err := initSource(cfg)

	// We expect either success or a database connection error
	// What we don't want is a panic or "unknown source type" error
	if err != nil {
		// Check for expected database-related errors
		dbErrors := []string{"connect", "connection", "dial", "database", "does not exist", "not reachable"}
		errLower := strings.ToLower(err.Error())
		hasExpectedError := false
		for _, errStr := range dbErrors {
			if strings.Contains(errLower, errStr) {
				hasExpectedError = true
				break
			}
		}
		assert.True(t, hasExpectedError, "should fail with database connection error, got: %v", err)
	}
}

func TestInitSource_CDC(t *testing.T) {
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:        "cdc",
			DSN:         "postgres://localhost/test",
			Table:       "outbox",
			SlotName:    "test_slot",
			Publication: "test_pub",
		},
		Delivery: config.DeliveryConfig{
			DeadLetterTable: "outbox_dead_letter",
			MaxRetries:      10,
		},
	}

	// Note: This will fail if no database is available, but that's expected
	// The test verifies the code path and proper error handling
	_, err := initSource(cfg)

	// We expect a database connection error (CDC tries to connect)
	if err != nil {
		dbErrors := []string{"connect", "connection", "dial", "database", "does not exist", "tls", "replication"}
		errLower := strings.ToLower(err.Error())
		hasExpectedError := false
		for _, errStr := range dbErrors {
			if strings.Contains(errLower, errStr) {
				hasExpectedError = true
				break
			}
		}
		assert.True(t, hasExpectedError, "should fail with database/connection error, got: %v", err)
	}
}

func TestInitSource_UnknownType(t *testing.T) {
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type: "invalid",
			DSN:  "postgres://localhost/test",
		},
	}

	_, err := initSource(cfg)

	require.Error(t, err, "unknown source type should return error")
	assert.Contains(t, err.Error(), "unknown source type", "should indicate unknown type")
}

func TestInitPublisher_Webhook(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name:    "test-webhook",
		Type:    "webhook",
		URL:     "https://example.com/webhook",
		Timeout: 10 * time.Second,
	}

	pub, err := initPublisher(cfg)
	require.NoError(t, err, "should initialize webhook publisher")
	defer func() { _ = pub.Close() }()

	assert.Equal(t, "test-webhook", pub.Name(), "publisher should have correct name")
}

func TestInitPublisher_RedisNotImplemented(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name: "test-redis",
		Type: "redis_stream",
		URL:  "redis://localhost:6379",
	}

	_, err := initPublisher(cfg)

	require.Error(t, err, "redis_stream should return error")
	assert.Contains(t, err.Error(), "not yet implemented", "should indicate redis not implemented")
}

func TestInitPublisher_KafkaNotImplemented(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name:    "test-kafka",
		Type:    "kafka",
		Topic:   "events",
		Brokers: []string{"localhost:9092"},
	}

	_, err := initPublisher(cfg)

	require.Error(t, err, "kafka should return error")
	assert.Contains(t, err.Error(), "not yet implemented", "should indicate kafka not implemented")
}

func TestInitPublisher_UnknownType(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name: "test",
		Type: "invalid",
		URL:  "https://example.com",
	}

	_, err := initPublisher(cfg)

	require.Error(t, err, "unknown publisher type should return error")
	assert.Contains(t, err.Error(), "unknown publisher type", "should indicate unknown type")
}

func TestInitPublishers_Success(t *testing.T) {
	cfg := &config.Config{
		Publishers: []config.PublisherConfig{
			{
				Name:    "webhook1",
				Type:    "webhook",
				URL:     "https://example.com/webhook1",
				Timeout: 10 * time.Second,
			},
			{
				Name:    "webhook2",
				Type:    "webhook",
				URL:     "https://example.com/webhook2",
				Timeout: 5 * time.Second,
			},
		},
	}

	pubs, err := initPublishers(cfg)
	require.NoError(t, err, "should initialize publishers")
	defer closePublishers(pubs)

	require.Len(t, pubs, 2, "should create 2 publishers")
	assert.Equal(t, "webhook1", pubs[0].Name(), "first publisher should have correct name")
	assert.Equal(t, "webhook2", pubs[1].Name(), "second publisher should have correct name")
}

func TestInitPublishers_FailureCleanup(t *testing.T) {
	cfg := &config.Config{
		Publishers: []config.PublisherConfig{
			{
				Name:    "webhook1",
				Type:    "webhook",
				URL:     "https://example.com/webhook1",
				Timeout: 10 * time.Second,
			},
			{
				Name: "invalid",
				Type: "unknown_type", // This will fail
				URL:  "https://example.com",
			},
		},
	}

	pubs, err := initPublishers(cfg)

	require.Error(t, err, "should fail when one publisher fails")
	assert.Nil(t, pubs, "should return nil publishers on error")
	assert.Contains(t, err.Error(), "failed to initialize publisher[1]", "should indicate which publisher failed")
	assert.Contains(t, err.Error(), "invalid", "should mention publisher name")
}

func TestInitPublishers_EmptyList(t *testing.T) {
	cfg := &config.Config{
		Publishers: []config.PublisherConfig{},
	}

	pubs, err := initPublishers(cfg)
	require.NoError(t, err, "should handle empty list without error")
	assert.Empty(t, pubs, "should return empty list")
}

func TestClosePublishers(t *testing.T) {
	cfg := &config.Config{
		Publishers: []config.PublisherConfig{
			{
				Name:    "webhook1",
				Type:    "webhook",
				URL:     "https://example.com/webhook1",
				Timeout: 10 * time.Second,
			},
			{
				Name:    "webhook2",
				Type:    "webhook",
				URL:     "https://example.com/webhook2",
				Timeout: 5 * time.Second,
			},
		},
	}

	pubs, err := initPublishers(cfg)
	require.NoError(t, err, "should initialize publishers")

	// Should not panic
	assert.NotPanics(t, func() { closePublishers(pubs) }, "should close without panic")

	// Should be idempotent
	assert.NotPanics(t, func() { closePublishers(pubs) }, "should be idempotent")
}

func TestClosePublishers_EmptyList(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("closePublishers panicked with nil list: %v", r)
		}
	}()

	// Should not panic with nil list
	closePublishers(nil)
}
