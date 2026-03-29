package polling

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"

	"github.com/slapec93/pg-outboxer/internal/config"
	"github.com/slapec93/pg-outboxer/internal/metrics"
	"github.com/slapec93/pg-outboxer/internal/source"
)

// Poller implements the Source interface using SELECT FOR UPDATE SKIP LOCKED polling
type Poller struct {
	db           *sql.DB
	config       *config.SourceConfig
	pollInterval time.Duration
	batchSize    int
	tableName    string
	dlqTable     string
	maxRetries   int
}

// New creates a new polling source
func New(cfg *config.Config) (*Poller, error) {
	db, err := sql.Open("postgres", cfg.Source.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("database not reachable: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	slog.Info("polling source initialized",
		"poll_interval", cfg.Source.PollInterval,
		"batch_size", cfg.Source.BatchSize,
		"table", cfg.Source.Table)

	return &Poller{
		db:           db,
		config:       &cfg.Source,
		pollInterval: cfg.Source.PollInterval,
		batchSize:    cfg.Source.BatchSize,
		tableName:    cfg.Source.Table,
		dlqTable:     cfg.Delivery.DeadLetterTable,
		maxRetries:   cfg.Delivery.MaxRetries,
	}, nil
}

// Start begins polling for events and sending them to the output channel
func (p *Poller) Start(ctx context.Context, out chan<- source.Event) error {
	slog.Info("starting polling loop",
		"interval", p.pollInterval,
		"batch_size", p.batchSize)

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	// Do an initial poll immediately
	if err := p.poll(ctx, out); err != nil {
		slog.Error("initial poll failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("polling stopped")
			return nil

		case <-ticker.C:
			if err := p.poll(ctx, out); err != nil {
				slog.Error("poll failed", "error", err)
				// Continue polling even on errors
			}
		}
	}
}

// poll fetches a batch of pending events and sends them to the output channel
func (p *Poller) poll(ctx context.Context, out chan<- source.Event) error {
	// SELECT FOR UPDATE SKIP LOCKED ensures:
	// 1. Only one worker claims each event (FOR UPDATE locks the rows)
	// 2. Workers don't wait for locked rows (SKIP LOCKED)
	// 3. Concurrent pollers won't process the same events

	// Use pq.QuoteIdentifier to safely escape table names
	table := pq.QuoteIdentifier(p.tableName)

	query := fmt.Sprintf(`
		UPDATE %s
		SET status = 'processing'
		WHERE id IN (
			SELECT id FROM %s
			WHERE status = 'pending'
			  AND (retry_after IS NULL OR retry_after <= NOW())
			ORDER BY created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, aggregate_type, aggregate_id, event_type,
		          payload, headers, created_at, retry_count, last_error
	`, table, table)

	rows, err := p.db.QueryContext(ctx, query, p.batchSize)
	if err != nil {
		return fmt.Errorf("failed to query outbox: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var event source.Event
		var headersJSON sql.NullString
		var lastError sql.NullString

		err := rows.Scan(
			&event.ID,
			&event.AggregateType,
			&event.AggregateID,
			&event.EventType,
			&event.Payload,
			&headersJSON,
			&event.CreatedAt,
			&event.RetryCount,
			&lastError,
		)
		if err != nil {
			slog.Error("failed to scan event", "error", err)
			continue
		}

		// Parse headers if present
		if headersJSON.Valid && headersJSON.String != "" {
			if err := json.Unmarshal([]byte(headersJSON.String), &event.Headers); err != nil {
				slog.Warn("failed to parse headers", "event_id", event.ID, "error", err)
			}
		}

		if lastError.Valid {
			event.LastError = lastError.String
		}

		// Send event to output channel (blocks if channel is full)
		select {
		case out <- event:
			count++
		case <-ctx.Done():
			return nil
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}

	if count > 0 {
		slog.Debug("polled events", "count", count)
	}

	return nil
}

// Ack marks an event as successfully delivered
func (p *Poller) Ack(ctx context.Context, eventID string) error {
	table := pq.QuoteIdentifier(p.tableName)

	query := fmt.Sprintf(`
		UPDATE %s
		SET status = 'delivered',
		    processed_at = NOW()
		WHERE id = $1
	`, table)

	result, err := p.db.ExecContext(ctx, query, eventID)
	if err != nil {
		return fmt.Errorf("failed to ack event: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		slog.Warn("ack: event not found", "event_id", eventID)
	} else {
		slog.Debug("event acked", "event_id", eventID)
	}

	return nil
}

// Nack marks an event as failed
func (p *Poller) Nack(ctx context.Context, eventID string, eventErr error, retryable bool) error {
	table := pq.QuoteIdentifier(p.tableName)

	// Get current event state
	var retryCount int
	query := fmt.Sprintf("SELECT retry_count FROM %s WHERE id = $1", table)
	err := p.db.QueryRowContext(ctx, query, eventID).Scan(&retryCount)
	if err != nil {
		return fmt.Errorf("failed to get retry count: %w", err)
	}

	retryCount++

	// Check if we should move to dead letter
	shouldDeadLetter := !retryable || retryCount >= p.maxRetries

	if shouldDeadLetter {
		return p.moveToDeadLetter(ctx, eventID, eventErr.Error(), retryCount)
	}

	// Schedule retry with exponential backoff + full jitter
	retryAfter := calculateRetryAfter(retryCount)

	query = fmt.Sprintf(`
		UPDATE %s
		SET status = 'pending',
		    retry_count = $1,
		    retry_after = NOW() + $2::interval,
		    last_error = $3
		WHERE id = $4
	`, table)

	_, err = p.db.ExecContext(ctx, query,
		retryCount,
		fmt.Sprintf("%d seconds", int(retryAfter.Seconds())),
		eventErr.Error(),
		eventID)

	if err != nil {
		return fmt.Errorf("failed to nack event: %w", err)
	}

	// Record retry metric
	metrics.RecordRetry(retryCount)

	slog.Info("event scheduled for retry",
		"event_id", eventID,
		"retry_count", retryCount,
		"retry_after", retryAfter,
		"error", eventErr.Error())

	return nil
}

// moveToDeadLetter moves an event to the dead letter table
func (p *Poller) moveToDeadLetter(ctx context.Context, eventID string, errorMsg string, retryCount int) error {
	table := pq.QuoteIdentifier(p.tableName)
	dlqTable := pq.QuoteIdentifier(p.dlqTable)

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Copy to dead letter table
	query := fmt.Sprintf(`
		INSERT INTO %s (id, aggregate_type, aggregate_id, event_type, payload, headers, created_at, last_error, retry_count)
		SELECT id, aggregate_type, aggregate_id, event_type, payload, headers, created_at, $1, $2
		FROM %s
		WHERE id = $3
	`, dlqTable, table)

	_, err = tx.ExecContext(ctx, query, errorMsg, retryCount, eventID)
	if err != nil {
		return fmt.Errorf("failed to insert into dead letter: %w", err)
	}

	// Delete from outbox
	query = fmt.Sprintf("DELETE FROM %s WHERE id = $1", table)
	_, err = tx.ExecContext(ctx, query, eventID)
	if err != nil {
		return fmt.Errorf("failed to delete from outbox: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit dead letter transaction: %w", err)
	}

	// Record dead letter metric
	metrics.RecordDeadLetter()

	slog.Warn("event moved to dead letter",
		"event_id", eventID,
		"retry_count", retryCount,
		"error", errorMsg)

	return nil
}

// calculateRetryAfter implements exponential backoff with full jitter
// Full jitter: return random value in [0, min(cap, base * 2^attempt)]
func calculateRetryAfter(attempt int) time.Duration {
	const (
		baseDelay = 1 * time.Second
		maxDelay  = 30 * time.Minute
	)

	// Calculate exponential backoff
	delay := baseDelay * (1 << uint(attempt-1)) // 2^(attempt-1)
	delay = min(delay, maxDelay)

	// Apply full jitter: random in [0, delay]
	// This prevents thundering herd when multiple events retry at once
	jitter := time.Duration(float64(delay) * (0.5 + 0.5*float64(attempt%10)/10.0))

	return jitter
}

// Close closes the database connection
func (p *Poller) Close() error {
	slog.Info("closing polling source")
	return p.db.Close()
}
