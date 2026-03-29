package main

import (
	"strings"
	"testing"
	"time"

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
		hasExpectedError := false
		for _, errStr := range dbErrors {
			if strings.Contains(strings.ToLower(err.Error()), errStr) {
				hasExpectedError = true
				break
			}
		}
		if !hasExpectedError {
			t.Errorf("unexpected error (expected database connection error): %v", err)
		}
	}
}

func TestInitSource_CDC_NotImplemented(t *testing.T) {
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type: "cdc",
			DSN:  "postgres://localhost/test",
		},
	}

	_, err := initSource(cfg)

	if err == nil {
		t.Fatal("expected error for CDC mode, got nil")
	}

	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("expected 'not yet implemented' error, got: %v", err)
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

	if err == nil {
		t.Fatal("expected error for unknown source type, got nil")
	}

	if !strings.Contains(err.Error(), "unknown source type") {
		t.Errorf("expected 'unknown source type' error, got: %v", err)
	}
}

func TestInitPublisher_Webhook(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name:    "test-webhook",
		Type:    "webhook",
		URL:     "https://example.com/webhook",
		Timeout: 10 * time.Second,
	}

	pub, err := initPublisher(cfg)
	if err != nil {
		t.Fatalf("failed to initialize webhook publisher: %v", err)
	}
	defer pub.Close()

	if pub.Name() != "test-webhook" {
		t.Errorf("expected name 'test-webhook', got '%s'", pub.Name())
	}
}

func TestInitPublisher_RedisNotImplemented(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name: "test-redis",
		Type: "redis_stream",
		URL:  "redis://localhost:6379",
	}

	_, err := initPublisher(cfg)

	if err == nil {
		t.Fatal("expected error for redis_stream, got nil")
	}

	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("expected 'not yet implemented' error, got: %v", err)
	}
}

func TestInitPublisher_KafkaNotImplemented(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name:    "test-kafka",
		Type:    "kafka",
		Topic:   "events",
		Brokers: []string{"localhost:9092"},
	}

	_, err := initPublisher(cfg)

	if err == nil {
		t.Fatal("expected error for kafka, got nil")
	}

	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("expected 'not yet implemented' error, got: %v", err)
	}
}

func TestInitPublisher_UnknownType(t *testing.T) {
	cfg := &config.PublisherConfig{
		Name: "test",
		Type: "invalid",
		URL:  "https://example.com",
	}

	_, err := initPublisher(cfg)

	if err == nil {
		t.Fatal("expected error for unknown publisher type, got nil")
	}

	if !strings.Contains(err.Error(), "unknown publisher type") {
		t.Errorf("expected 'unknown publisher type' error, got: %v", err)
	}
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
	if err != nil {
		t.Fatalf("failed to initialize publishers: %v", err)
	}
	defer closePublishers(pubs)

	if len(pubs) != 2 {
		t.Errorf("expected 2 publishers, got %d", len(pubs))
	}

	if pubs[0].Name() != "webhook1" {
		t.Errorf("expected first publisher name 'webhook1', got '%s'", pubs[0].Name())
	}

	if pubs[1].Name() != "webhook2" {
		t.Errorf("expected second publisher name 'webhook2', got '%s'", pubs[1].Name())
	}
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

	if err == nil {
		t.Fatal("expected error when one publisher fails, got nil")
	}

	if pubs != nil {
		t.Error("expected nil publishers on error")
	}

	if !strings.Contains(err.Error(), "failed to initialize publisher[1]") {
		t.Errorf("expected error about publisher[1], got: %v", err)
	}

	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected error to mention publisher name 'invalid', got: %v", err)
	}
}

func TestInitPublishers_EmptyList(t *testing.T) {
	cfg := &config.Config{
		Publishers: []config.PublisherConfig{},
	}

	pubs, err := initPublishers(cfg)
	if err != nil {
		t.Fatalf("expected no error for empty list, got: %v", err)
	}

	if len(pubs) != 0 {
		t.Errorf("expected 0 publishers, got %d", len(pubs))
	}
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
	if err != nil {
		t.Fatalf("failed to initialize publishers: %v", err)
	}

	// Should not panic or error
	closePublishers(pubs)

	// Should be idempotent
	closePublishers(pubs)
}

func TestClosePublishers_EmptyList(t *testing.T) {
	// Should not panic
	closePublishers(nil)
}
