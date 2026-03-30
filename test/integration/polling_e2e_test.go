//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/slapec93/pg-outboxer/internal/delivery"
	"github.com/slapec93/pg-outboxer/internal/publisher"
	"github.com/slapec93/pg-outboxer/internal/publisher/webhook"
	"github.com/slapec93/pg-outboxer/internal/source/polling"
)

// Polling-specific integration tests


func TestE2E_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup PostgreSQL
	pgContainer, connStr := setupPostgres(t, ctx)
	defer pgContainer.Terminate(ctx)

	// Setup webhook server
	webhookServer := newTestWebhookServer()
	defer webhookServer.Close()

	// Create full config
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:         "polling",
			DSN:          connStr,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
	}

	// Create source
	src, err := polling.New(cfg)
	require.NoError(t, err)
	defer src.Close()

	// Create publisher
	pub, err := webhook.New(&config.PublisherConfig{
		Name:    "test-webhook",
		Type:    "webhook",
		URL:     webhookServer.URL(),
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer pub.Close()

	// Create dispatcher
	disp := delivery.New(src, pub, 2, 10)

	// Start dispatcher (runs in background)
	go disp.Start(ctx)

	// Insert test event
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	eventID := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload, headers)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, eventID, "order", "order-123", "order.created", `{"amount": 100}`, `{"user_id": "user-456"}`)
	require.NoError(t, err)

	// Wait for event to be processed
	assert.Eventually(t, func() bool {
		return webhookServer.EventCount() == 1
	}, 5*time.Second, 100*time.Millisecond, "Expected 1 event to be received")

	// Verify webhook received correct data
	events := webhookServer.GetEvents()
	require.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, eventID, event.ID)
	assert.Equal(t, "order.created", event.Type)
	assert.Equal(t, "order", event.AggregateType)
	assert.Equal(t, "order-123", event.AggregateID)
	assert.Equal(t, "user-456", event.Headers["user_id"])

	// Verify event marked as delivered in DB
	var status string
	var processedAt sql.NullTime
	err = db.QueryRow(`SELECT status, processed_at FROM outbox WHERE id = $1`, eventID).Scan(&status, &processedAt)
	require.NoError(t, err)
	assert.Equal(t, "delivered", status)
	assert.True(t, processedAt.Valid)
}

func TestE2E_BatchProcessing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgContainer, connStr := setupPostgres(t, ctx)
	defer pgContainer.Terminate(ctx)

	webhookServer := newTestWebhookServer()
	defer webhookServer.Close()

	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:         "polling",
			DSN:          connStr,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    50,
		},
	}

	src, err := polling.New(cfg)
	require.NoError(t, err)
	defer src.Close()

	pub, err := webhook.New(&config.PublisherConfig{
		Name:    "test-webhook",
		Type:    "webhook",
		URL:     webhookServer.URL(),
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer pub.Close()

	disp := delivery.New(src, pub, 4, 20)

	go disp.Start(ctx)

	// Insert 100 events
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	for i := 0; i < 100; i++ {
		_, err = db.Exec(`
			INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
			VALUES ($1, $2, $3, $4)
		`, "order", fmt.Sprintf("order-%d", i), "order.created", fmt.Sprintf(`{"amount": %d}`, i*10))
		require.NoError(t, err)
	}

	// Wait for all events to be processed
	assert.Eventually(t, func() bool {
		return webhookServer.EventCount() == 100
	}, 10*time.Second, 100*time.Millisecond, "Expected 100 events to be received")

	// Verify all processed
	var pending int
	err = db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE status = 'pending'`).Scan(&pending)
	require.NoError(t, err)
	assert.Equal(t, 0, pending)
}

func TestE2E_RetryOnFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgContainer, connStr := setupPostgres(t, ctx)
	defer pgContainer.Terminate(ctx)

	webhookServer := newTestWebhookServer()
	defer webhookServer.Close()

	// Make first 3 attempts fail, then succeed
	webhookServer.SetFailNext(3)

	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:         "polling",
			DSN:          connStr,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
		Delivery: config.DeliveryConfig{
			MaxRetries:      10, // Ensure enough retries
			DeadLetterTable: "outbox_dead_letter",
		},
	}

	src, err := polling.New(cfg)
	require.NoError(t, err)
	defer src.Close()

	pub, err := webhook.New(&config.PublisherConfig{
		Name:    "test-webhook",
		Type:    "webhook",
		URL:     webhookServer.URL(),
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer pub.Close()

	disp := delivery.New(src, pub, 2, 10)

	go disp.Start(ctx)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	eventID := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, "order", "order-retry", "order.created", `{"amount": 200}`)
	require.NoError(t, err)

	// Wait for event to eventually succeed after retries (status = delivered)
	assert.Eventually(t, func() bool {
		var status string
		err := db.QueryRow(`SELECT status FROM outbox WHERE id = $1`, eventID).Scan(&status)
		if err != nil {
			return false
		}
		return status == "delivered"
	}, 10*time.Second, 100*time.Millisecond, "Expected event to be delivered after retries")

	// Verify webhook received the event
	assert.Equal(t, 1, webhookServer.EventCount())

	// Verify retry count was incremented
	var retryCount int
	err = db.QueryRow(`SELECT retry_count FROM outbox WHERE id = $1`, eventID).Scan(&retryCount)
	require.NoError(t, err)
	assert.Equal(t, 3, retryCount, "Expected 3 retry attempts before success")
}

func TestE2E_DeadLetterQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	pgContainer, connStr := setupPostgres(t, ctx)
	defer pgContainer.Terminate(ctx)

	webhookServer := newTestWebhookServer()
	defer webhookServer.Close()

	// Make all requests fail
	webhookServer.SetFailNext(999)

	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:         "polling",
			DSN:          connStr,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
		Delivery: config.DeliveryConfig{
			MaxRetries:      3, // Low retry count for faster test
			DeadLetterTable: "outbox_dead_letter",
		},
	}

	src, err := polling.New(cfg)
	require.NoError(t, err)
	defer src.Close()

	pub, err := webhook.New(&config.PublisherConfig{
		Name:    "test-webhook",
		Type:    "webhook",
		URL:     webhookServer.URL(),
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer pub.Close()

	disp := delivery.New(src, pub, 2, 10)

	go disp.Start(ctx)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	eventID := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, "order", "order-dlq", "order.created", `{"amount": 300}`)
	require.NoError(t, err)

	// Wait for event to be moved to dead letter
	assert.Eventually(t, func() bool {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM outbox_dead_letter WHERE id = $1`, eventID).Scan(&count)
		return err == nil && count == 1
	}, 15*time.Second, 200*time.Millisecond, "Expected event to be moved to dead letter queue")

	// Verify dead letter record
	var dlqID, aggregateType, aggregateID, eventType, lastError string
	var retryCount int
	err = db.QueryRow(`
		SELECT id, aggregate_type, aggregate_id, event_type, retry_count, last_error
		FROM outbox_dead_letter WHERE id = $1
	`, eventID).Scan(&dlqID, &aggregateType, &aggregateID, &eventType, &retryCount, &lastError)
	require.NoError(t, err)

	assert.Equal(t, eventID, dlqID)
	assert.Equal(t, "order", aggregateType)
	assert.Equal(t, "order-dlq", aggregateID)
	assert.Equal(t, "order.created", eventType)
	assert.Equal(t, 3, retryCount)
	assert.NotEmpty(t, lastError)

	// Verify original record was deleted
	var outboxCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE id = $1`, eventID).Scan(&outboxCount)
	require.NoError(t, err)
	assert.Equal(t, 0, outboxCount)

	// Webhook should have received 4 attempts (initial + 3 retries)
	// But we're not asserting exact count since timing can vary
	assert.Equal(t, 0, webhookServer.EventCount(), "No events should have succeeded")
}

func TestE2E_MultiplePublishers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgContainer, connStr := setupPostgres(t, ctx)
	defer pgContainer.Terminate(ctx)

	// Two webhook servers
	webhookServer1 := newTestWebhookServer()
	defer webhookServer1.Close()
	webhookServer2 := newTestWebhookServer()
	defer webhookServer2.Close()

	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:         "polling",
			DSN:          connStr,
			Table:        "outbox",
			PollInterval: 100 * time.Millisecond,
			BatchSize:    10,
		},
	}

	src, err := polling.New(cfg)
	require.NoError(t, err)
	defer src.Close()

	pub1, err := webhook.New(&config.PublisherConfig{
		Name:    "webhook-1",
		Type:    "webhook",
		URL:     webhookServer1.URL(),
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer pub1.Close()

	pub2, err := webhook.New(&config.PublisherConfig{
		Name:    "webhook-2",
		Type:    "webhook",
		URL:     webhookServer2.URL(),
		Timeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer pub2.Close()

	// Multi publisher wraps both
	multiPub := publisher.NewMulti([]publisher.Publisher{pub1, pub2})

	disp := delivery.New(src, multiPub, 2, 10)

	go disp.Start(ctx)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	eventID := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, "order", "order-multi", "order.created", `{"amount": 400}`)
	require.NoError(t, err)

	// Both webhooks should receive the event
	assert.Eventually(t, func() bool {
		return webhookServer1.EventCount() == 1 && webhookServer2.EventCount() == 1
	}, 5*time.Second, 100*time.Millisecond, "Expected both webhooks to receive event")

	// Verify both received same event
	events1 := webhookServer1.GetEvents()
	events2 := webhookServer2.GetEvents()
	require.Len(t, events1, 1)
	require.Len(t, events2, 1)

	assert.Equal(t, eventID, events1[0].ID)
	assert.Equal(t, eventID, events2[0].ID)
	assert.Equal(t, "order.created", events1[0].Type)
	assert.Equal(t, "order.created", events2[0].Type)
}
