package polling

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testDB holds test database connection and cleanup
type testDB struct {
	container testcontainers.Container
	db        *sql.DB
	dsn       string
}

// setupTestDB creates a Postgres container and initializes the schema
func setupTestDB(t *testing.T) *testDB {
	t.Helper()

	ctx := context.Background()

	// Start Postgres container
	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "test",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	// Get connection details
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("failed to get container port: %v", err)
	}

	dsn := fmt.Sprintf("postgres://test:test@%s:%s/test?sslmode=disable", host, port.Port())

	// Connect to database
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to connect to database: %v", err)
	}

	// Wait for connection to be ready
	for range 30 {
		if err := db.Ping(); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Run migrations
	migrationFiles := []string{
		"../../../cmd/pg-outboxer/migrations/001_outbox.sql",
		"../../../cmd/pg-outboxer/migrations/002_dead_letter.sql",
	}

	for _, file := range migrationFiles {
		migration, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read migration %s: %v", file, err)
		}

		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatalf("failed to run migration %s: %v", file, err)
		}
	}

	return &testDB{
		container: container,
		db:        db,
		dsn:       dsn,
	}
}

func (td *testDB) cleanup(t *testing.T) {
	t.Helper()

	if td.db != nil {
		td.db.Close()
	}
	if td.container != nil {
		if err := td.container.Terminate(context.Background()); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	}
}

// insertTestEvent inserts an event into the outbox
func insertTestEvent(t *testing.T, db *sql.DB, aggType, aggID, eventType string) string {
	t.Helper()

	payload := map[string]string{"test": "data"}
	payloadJSON, _ := json.Marshal(payload)

	var id string
	err := db.QueryRow(`
		INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, aggType, aggID, eventType, payloadJSON).Scan(&id)

	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	return id
}

// getEventStatus returns the status of an event
func getEventStatus(t *testing.T, db *sql.DB, eventID string) string {
	t.Helper()

	var status string
	err := db.QueryRow("SELECT status FROM outbox WHERE id = $1", eventID).Scan(&status)
	if err == sql.ErrNoRows {
		return "deleted"
	}
	if err != nil {
		t.Fatalf("failed to get event status: %v", err)
	}
	return status
}
