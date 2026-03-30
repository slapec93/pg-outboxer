//go:build integration

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testWebhookServer is a test HTTP server that receives and records webhook events
type testWebhookServer struct {
	server   *httptest.Server
	mu       sync.Mutex
	events   []webhookPayload
	failNext int // Number of requests to fail
}

type webhookPayload struct {
	ID            string                 `json:"id"`
	Type          string                 `json:"type"`
	Created       int64                  `json:"created"`
	AggregateType string                 `json:"aggregate_type"`
	AggregateID   string                 `json:"aggregate_id"`
	Data          map[string]interface{} `json:"data"`
	Headers       map[string]string      `json:"headers,omitempty"`
}

func newTestWebhookServer() *testWebhookServer {
	ws := &testWebhookServer{
		events: make([]webhookPayload, 0),
	}

	ws.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws.mu.Lock()
		defer ws.mu.Unlock()

		// Simulate failure if requested
		if ws.failNext > 0 {
			ws.failNext--
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var payload webhookPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		ws.events = append(ws.events, payload)
		w.WriteHeader(http.StatusOK)
	}))

	return ws
}

func (ws *testWebhookServer) Close() {
	ws.server.Close()
}

func (ws *testWebhookServer) URL() string {
	return ws.server.URL
}

func (ws *testWebhookServer) GetEvents() []webhookPayload {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return append([]webhookPayload{}, ws.events...)
}

func (ws *testWebhookServer) SetFailNext(n int) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.failNext = n
}

func (ws *testWebhookServer) EventCount() int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return len(ws.events)
}

// setupPostgres creates a PostgreSQL container with standard outbox tables
func setupPostgres(t *testing.T, ctx context.Context) (*postgres.PostgresContainer, string) {
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create tables
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			aggregate_type VARCHAR(255) NOT NULL,
			aggregate_id VARCHAR(255) NOT NULL,
			event_type VARCHAR(255) NOT NULL,
			payload JSONB NOT NULL,
			headers JSONB,
			status VARCHAR(50) DEFAULT 'pending',
			retry_count INT DEFAULT 0,
			retry_after TIMESTAMP,
			last_error TEXT,
			created_at TIMESTAMP DEFAULT NOW(),
			processed_at TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox(created_at ASC) WHERE status = 'pending';

		CREATE TABLE IF NOT EXISTS outbox_dead_letter (
			id UUID PRIMARY KEY,
			aggregate_type VARCHAR(255) NOT NULL,
			aggregate_id VARCHAR(255) NOT NULL,
			event_type VARCHAR(255) NOT NULL,
			payload JSONB NOT NULL,
			headers JSONB,
			created_at TIMESTAMP NOT NULL,
			last_error TEXT NOT NULL,
			retry_count INT NOT NULL,
			moved_at TIMESTAMP DEFAULT NOW()
		);
	`)
	require.NoError(t, err)

	return pgContainer, connStr
}

// setupPostgresWithCDC creates a PostgreSQL container with logical replication enabled
func setupPostgresWithCDC(t *testing.T, ctx context.Context) (*postgres.PostgresContainer, string) {
	// Use postgres image with custom config for logical replication
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
		// Enable logical replication via command
		postgres.WithConfigFile("testdata/postgresql.conf"),
	)

	// If config file doesn't exist, try setting via ALTER SYSTEM
	if err != nil {
		// Try without config file
		pgContainer, err = postgres.Run(ctx,
			"postgres:15-alpine",
			postgres.WithDatabase("testdb"),
			postgres.WithUsername("testuser"),
			postgres.WithPassword("testpass"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second)),
		)
		require.NoError(t, err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create tables and setup CDC
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	// Enable logical replication (requires restart in production, but may work in test)
	_, _ = db.Exec("ALTER SYSTEM SET wal_level = logical")
	_, _ = db.Exec("ALTER SYSTEM SET max_replication_slots = 4")
	_, _ = db.Exec("SELECT pg_reload_conf()")

	// Create tables
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			aggregate_type VARCHAR(255) NOT NULL,
			aggregate_id VARCHAR(255) NOT NULL,
			event_type VARCHAR(255) NOT NULL,
			payload JSONB NOT NULL,
			headers JSONB,
			status VARCHAR(50) DEFAULT 'pending',
			retry_count INT DEFAULT 0,
			retry_after TIMESTAMP,
			last_error TEXT,
			created_at TIMESTAMP DEFAULT NOW(),
			processed_at TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS outbox_dead_letter (
			id UUID PRIMARY KEY,
			aggregate_type VARCHAR(255) NOT NULL,
			aggregate_id VARCHAR(255) NOT NULL,
			event_type VARCHAR(255) NOT NULL,
			payload JSONB NOT NULL,
			headers JSONB,
			created_at TIMESTAMP NOT NULL,
			last_error TEXT NOT NULL,
			retry_count INT NOT NULL,
			moved_at TIMESTAMP DEFAULT NOW()
		);

		-- Create publication for CDC
		CREATE PUBLICATION pg_outboxer_test_pub FOR TABLE outbox;
	`)
	require.NoError(t, err)

	return pgContainer, connStr
}
