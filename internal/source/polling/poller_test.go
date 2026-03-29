package polling

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/slapec93/pg-outboxer/internal/source"
)

func TestPoller_New(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.cleanup(t)

	cfg := &config.Config{
		Source: config.SourceConfig{
			DSN:          testDB.dsn,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
		Delivery: config.DeliveryConfig{
			DeadLetterTable: "outbox_dead_letter",
			MaxRetries:      5,
		},
	}

	poller, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create poller: %v", err)
	}
	defer poller.Close()

	if poller.db == nil {
		t.Error("expected db to be initialized")
	}
	if poller.pollInterval != 100*time.Millisecond {
		t.Errorf("expected poll interval 100ms, got %v", poller.pollInterval)
	}
	if poller.batchSize != 10 {
		t.Errorf("expected batch size 10, got %d", poller.batchSize)
	}
}

func TestPoller_Poll(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.cleanup(t)

	cfg := &config.Config{
		Source: config.SourceConfig{
			DSN:          testDB.dsn,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
		Delivery: config.DeliveryConfig{
			DeadLetterTable: "outbox_dead_letter",
			MaxRetries:      5,
		},
	}

	poller, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create poller: %v", err)
	}
	defer poller.Close()

	// Insert test events
	id1 := insertTestEvent(t, testDB.db, "order", "order-1", "order.created")
	id2 := insertTestEvent(t, testDB.db, "order", "order-2", "order.created")

	// Poll events
	ctx := context.Background()
	eventChan := make(chan source.Event, 10)

	err = poller.poll(ctx, eventChan)
	if err != nil {
		t.Fatalf("poll failed: %v", err)
	}

	// Check we got both events
	if len(eventChan) != 2 {
		t.Fatalf("expected 2 events, got %d", len(eventChan))
	}

	// Verify events
	event1 := <-eventChan
	event2 := <-eventChan

	if event1.ID != id1 && event1.ID != id2 {
		t.Errorf("unexpected event ID: %s", event1.ID)
	}
	if event1.EventType != "order.created" {
		t.Errorf("expected event type 'order.created', got '%s'", event1.EventType)
	}
	if event2.EventType != "order.created" {
		t.Errorf("expected event type 'order.created', got '%s'", event2.EventType)
	}

	// Verify status changed to 'processing'
	status1 := getEventStatus(t, testDB.db, id1)
	status2 := getEventStatus(t, testDB.db, id2)

	if status1 != "processing" {
		t.Errorf("expected status 'processing', got '%s'", status1)
	}
	if status2 != "processing" {
		t.Errorf("expected status 'processing', got '%s'", status2)
	}
}

func TestPoller_Ack(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.cleanup(t)

	cfg := &config.Config{
		Source: config.SourceConfig{
			DSN:          testDB.dsn,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
		Delivery: config.DeliveryConfig{
			DeadLetterTable: "outbox_dead_letter",
			MaxRetries:      5,
		},
	}

	poller, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create poller: %v", err)
	}
	defer poller.Close()

	// Insert and poll event
	eventID := insertTestEvent(t, testDB.db, "order", "order-1", "order.created")

	ctx := context.Background()
	eventChan := make(chan source.Event, 10)
	poller.poll(ctx, eventChan)

	// Ack the event
	err = poller.Ack(ctx, eventID)
	if err != nil {
		t.Fatalf("ack failed: %v", err)
	}

	// Verify status is 'delivered'
	status := getEventStatus(t, testDB.db, eventID)
	if status != "delivered" {
		t.Errorf("expected status 'delivered', got '%s'", status)
	}

	// Verify processed_at is set
	var processedAt sql.NullTime
	err = testDB.db.QueryRow("SELECT processed_at FROM outbox WHERE id = $1", eventID).Scan(&processedAt)
	if err != nil {
		t.Fatalf("failed to query processed_at: %v", err)
	}
	if !processedAt.Valid {
		t.Error("expected processed_at to be set")
	}
}

func TestPoller_Nack_Retryable(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.cleanup(t)

	cfg := &config.Config{
		Source: config.SourceConfig{
			DSN:          testDB.dsn,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
		Delivery: config.DeliveryConfig{
			DeadLetterTable: "outbox_dead_letter",
			MaxRetries:      5,
		},
	}

	poller, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create poller: %v", err)
	}
	defer poller.Close()

	// Insert and poll event
	eventID := insertTestEvent(t, testDB.db, "order", "order-1", "order.created")

	ctx := context.Background()
	eventChan := make(chan source.Event, 10)
	poller.poll(ctx, eventChan)

	// Nack with retryable error
	err = poller.Nack(ctx, eventID, fmt.Errorf("temporary error"), true)
	if err != nil {
		t.Fatalf("nack failed: %v", err)
	}

	// Verify status is back to 'pending'
	status := getEventStatus(t, testDB.db, eventID)
	if status != "pending" {
		t.Errorf("expected status 'pending', got '%s'", status)
	}

	// Verify retry_count increased
	var retryCount int
	var retryAfter sql.NullTime
	var lastError sql.NullString
	err = testDB.db.QueryRow(`
		SELECT retry_count, retry_after, last_error
		FROM outbox WHERE id = $1
	`, eventID).Scan(&retryCount, &retryAfter, &lastError)
	if err != nil {
		t.Fatalf("failed to query retry info: %v", err)
	}

	if retryCount != 1 {
		t.Errorf("expected retry_count 1, got %d", retryCount)
	}
	if !retryAfter.Valid {
		t.Error("expected retry_after to be set")
	}
	if !lastError.Valid || lastError.String != "temporary error" {
		t.Errorf("expected last_error 'temporary error', got '%s'", lastError.String)
	}
}

func TestPoller_Nack_NonRetryable(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.cleanup(t)

	cfg := &config.Config{
		Source: config.SourceConfig{
			DSN:          testDB.dsn,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
		Delivery: config.DeliveryConfig{
			DeadLetterTable: "outbox_dead_letter",
			MaxRetries:      5,
		},
	}

	poller, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create poller: %v", err)
	}
	defer poller.Close()

	// Insert and poll event
	eventID := insertTestEvent(t, testDB.db, "order", "order-1", "order.created")

	ctx := context.Background()
	eventChan := make(chan source.Event, 10)
	poller.poll(ctx, eventChan)

	// Nack with non-retryable error
	err = poller.Nack(ctx, eventID, fmt.Errorf("permanent error"), false)
	if err != nil {
		t.Fatalf("nack failed: %v", err)
	}

	// Verify event moved to dead letter
	status := getEventStatus(t, testDB.db, eventID)
	if status != "deleted" {
		t.Errorf("expected event to be deleted from outbox, got status '%s'", status)
	}

	// Verify event in dead letter table
	var dlqID string
	var lastError string
	err = testDB.db.QueryRow(`
		SELECT id, last_error FROM outbox_dead_letter WHERE id = $1
	`, eventID).Scan(&dlqID, &lastError)
	if err != nil {
		t.Fatalf("event not found in dead letter: %v", err)
	}

	if dlqID != eventID {
		t.Errorf("expected dead letter id '%s', got '%s'", eventID, dlqID)
	}
	if lastError != "permanent error" {
		t.Errorf("expected last_error 'permanent error', got '%s'", lastError)
	}
}

func TestPoller_Nack_MaxRetries(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.cleanup(t)

	cfg := &config.Config{
		Source: config.SourceConfig{
			DSN:          testDB.dsn,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
		Delivery: config.DeliveryConfig{
			DeadLetterTable: "outbox_dead_letter",
			MaxRetries:      3, // Low limit for test
		},
	}

	poller, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create poller: %v", err)
	}
	defer poller.Close()

	// Insert event
	eventID := insertTestEvent(t, testDB.db, "order", "order-1", "order.created")
	ctx := context.Background()

	// Fail it 3 times (should go to dead letter on 3rd)
	for i := 0; i < 3; i++ {
		// Poll
		eventChan := make(chan source.Event, 10)
		poller.poll(ctx, eventChan)

		// Nack with retryable error
		err = poller.Nack(ctx, eventID, fmt.Errorf("retry %d", i+1), true)
		if err != nil {
			t.Fatalf("nack %d failed: %v", i+1, err)
		}

		// For attempts 1-2, should still be in outbox
		if i < 2 {
			status := getEventStatus(t, testDB.db, eventID)
			if status != "pending" {
				t.Errorf("attempt %d: expected status 'pending', got '%s'", i+1, status)
			}
		}
	}

	// After 3rd attempt, should be in dead letter
	status := getEventStatus(t, testDB.db, eventID)
	if status != "deleted" {
		t.Errorf("expected event to be deleted after max retries, got status '%s'", status)
	}

	// Verify in dead letter
	var dlqRetryCount int
	err = testDB.db.QueryRow(`
		SELECT retry_count FROM outbox_dead_letter WHERE id = $1
	`, eventID).Scan(&dlqRetryCount)
	if err != nil {
		t.Fatalf("event not found in dead letter: %v", err)
	}

	if dlqRetryCount != 3 {
		t.Errorf("expected retry_count 3 in dead letter, got %d", dlqRetryCount)
	}
}

func TestCalculateRetryAfter(t *testing.T) {
	tests := []struct {
		attempt int
		minWant time.Duration
		maxWant time.Duration
	}{
		{1, 0, 1 * time.Second},
		{2, 0, 2 * time.Second},
		{3, 0, 4 * time.Second},
		{10, 0, 30 * time.Minute}, // Should cap at 30min
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			got := calculateRetryAfter(tt.attempt)

			if got < tt.minWant || got > tt.maxWant {
				t.Errorf("calculateRetryAfter(%d) = %v, want between %v and %v",
					tt.attempt, got, tt.minWant, tt.maxWant)
			}
		})
	}
}
