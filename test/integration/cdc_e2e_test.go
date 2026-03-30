//go:build integration

package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/slapec93/pg-outboxer/internal/delivery"
	"github.com/slapec93/pg-outboxer/internal/publisher/webhook"
	"github.com/slapec93/pg-outboxer/internal/source/cdc"
)

// CDC-specific integration tests
// Note: These tests require PostgreSQL with wal_level=logical

func TestCDC_HappyPath(t *testing.T) {
	t.Skip("CDC requires wal_level=logical which needs container restart - skip for now")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup PostgreSQL with CDC
	pgContainer, connStr := setupPostgresWithCDC(t, ctx)
	defer pgContainer.Terminate(ctx)

	// Setup webhook server
	webhookServer := newTestWebhookServer()
	defer webhookServer.Close()

	// Create CDC config
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:        "cdc",
			DSN:         connStr,
			Table:       "outbox",
			SlotName:    "test_cdc_slot",
			Publication: "pg_outboxer_test_pub",
		},
	}

	// Create CDC source
	src, err := cdc.New(cfg)
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

	// Start dispatcher
	go disp.Start(ctx)

	// Wait for CDC to be ready
	time.Sleep(2 * time.Second)

	// Insert test event
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	eventID := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload, headers)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, eventID, "order", "order-cdc-123", "order.created", `{"amount": 150}`, `{"source": "cdc"}`)
	require.NoError(t, err)

	// CDC should deliver within milliseconds
	assert.Eventually(t, func() bool {
		return webhookServer.EventCount() == 1
	}, 5*time.Second, 10*time.Millisecond, "Expected CDC to deliver event quickly")

	// Verify webhook received correct data
	events := webhookServer.GetEvents()
	require.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, eventID, event.ID)
	assert.Equal(t, "order.created", event.Type)
	assert.Equal(t, "order", event.AggregateType)
	assert.Equal(t, "order-cdc-123", event.AggregateID)
	assert.Equal(t, "cdc", event.Headers["source"])
}

func TestCDC_Metrics(t *testing.T) {
	t.Skip("CDC requires wal_level=logical which needs container restart - skip for now")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgContainer, connStr := setupPostgresWithCDC(t, ctx)
	defer pgContainer.Terminate(ctx)

	webhookServer := newTestWebhookServer()
	defer webhookServer.Close()

	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:        "cdc",
			DSN:         connStr,
			Table:       "outbox",
			SlotName:    "test_metrics_slot",
			Publication: "pg_outboxer_test_pub",
		},
	}

	src, err := cdc.New(cfg)
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

	time.Sleep(2 * time.Second)

	// Check CDC metrics are initialized
	// Note: In a real test, we'd scrape /metrics endpoint
	// For now, just verify the code paths work

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	// Insert multiple events
	for i := 0; i < 5; i++ {
		_, err = db.Exec(`
			INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
			VALUES ($1, $2, $3, $4)
		`, "order", uuid.New().String(), "order.created", `{"amount": 100}`)
		require.NoError(t, err)
	}

	// Wait for delivery
	assert.Eventually(t, func() bool {
		return webhookServer.EventCount() == 5
	}, 5*time.Second, 10*time.Millisecond, "Expected 5 events delivered via CDC")

	// Verify metrics were recorded (these would be checked via Prometheus in production)
	// metrics.ReplicationSlotActive should be 1
	// metrics.CDCEventsDecoded should be 5
	// metrics.WALMessagesReceived should be > 0
}

func TestCDC_LowLatency(t *testing.T) {
	t.Skip("CDC requires wal_level=logical which needs container restart - skip for now")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgContainer, connStr := setupPostgresWithCDC(t, ctx)
	defer pgContainer.Terminate(ctx)

	webhookServer := newTestWebhookServer()
	defer webhookServer.Close()

	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:        "cdc",
			DSN:         connStr,
			Table:       "outbox",
			SlotName:    "test_latency_slot",
			Publication: "pg_outboxer_test_pub",
		},
	}

	src, err := cdc.New(cfg)
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

	time.Sleep(2 * time.Second)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	// Measure latency from INSERT to webhook delivery
	start := time.Now()

	eventID := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, eventID, "order", "latency-test", "order.created", `{"test": true}`)
	require.NoError(t, err)

	// Wait for delivery
	delivered := make(chan time.Duration, 1)
	go func() {
		for {
			if webhookServer.EventCount() > 0 {
				delivered <- time.Since(start)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	select {
	case latency := <-delivered:
		// CDC should deliver in < 100ms (typically 1-10ms)
		assert.Less(t, latency, 100*time.Millisecond,
			"CDC latency should be sub-100ms, got %v", latency)
		t.Logf("CDC latency: %v", latency)

	case <-time.After(5 * time.Second):
		t.Fatal("Event not delivered within 5 seconds")
	}
}

func TestCDC_ReplicationSlot(t *testing.T) {
	t.Skip("CDC requires wal_level=logical which needs container restart - skip for now")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgContainer, connStr := setupPostgresWithCDC(t, ctx)
	defer pgContainer.Terminate(ctx)

	// Verify publication exists
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	var pubExists bool
	err = db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM pg_publication WHERE pubname = 'pg_outboxer_test_pub'
		)
	`).Scan(&pubExists)
	require.NoError(t, err)
	assert.True(t, pubExists, "Publication should exist")

	// Create CDC source (should create replication slot)
	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:        "cdc",
			DSN:         connStr,
			Table:       "outbox",
			SlotName:    "test_slot_mgmt",
			Publication: "pg_outboxer_test_pub",
		},
	}

	src, err := cdc.New(cfg)
	require.NoError(t, err)

	// Start to trigger slot creation
	go func() {
		// Start will create the slot
		_ = src.Start(ctx, nil)
	}()

	time.Sleep(2 * time.Second)

	// Verify replication slot was created
	var slotExists bool
	err = db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM pg_replication_slots WHERE slot_name = 'test_slot_mgmt'
		)
	`).Scan(&slotExists)
	require.NoError(t, err)
	assert.True(t, slotExists, "Replication slot should be created")

	src.Close()

	// Verify metrics
	// After close, replication should be marked inactive
	// metrics.ReplicationSlotActive should be 0
}

func TestCDC_MultipleInserts(t *testing.T) {
	t.Skip("CDC requires wal_level=logical which needs container restart - skip for now")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgContainer, connStr := setupPostgresWithCDC(t, ctx)
	defer pgContainer.Terminate(ctx)

	webhookServer := newTestWebhookServer()
	defer webhookServer.Close()

	cfg := &config.Config{
		Source: config.SourceConfig{
			Type:        "cdc",
			DSN:         connStr,
			Table:       "outbox",
			SlotName:    "test_multi_slot",
			Publication: "pg_outboxer_test_pub",
		},
	}

	src, err := cdc.New(cfg)
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

	time.Sleep(2 * time.Second)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	// Insert 50 events rapidly
	for i := 0; i < 50; i++ {
		_, err = db.Exec(`
			INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
			VALUES ($1, $2, $3, $4)
		`, "order", uuid.New().String(), "order.created", `{"batch": true}`)
		require.NoError(t, err)
	}

	// CDC should stream all events quickly
	assert.Eventually(t, func() bool {
		return webhookServer.EventCount() == 50
	}, 10*time.Second, 50*time.Millisecond, "Expected 50 events delivered via CDC")

	// Verify metrics show all WAL messages processed
	// metrics.CDCEventsDecoded should be 50
	// metrics.WALMessagesReceived{type="insert"} should be 50
}
