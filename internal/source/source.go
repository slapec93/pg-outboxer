// Package source defines the interface for outbox event sources.
package source

import (
	"context"
	"time"
)

// Event represents an outbox event to be published
type Event struct {
	ID            string            `json:"id"`
	AggregateType string            `json:"aggregate_type"`
	AggregateID   string            `json:"aggregate_id"`
	EventType     string            `json:"event_type"`
	Payload       []byte            `json:"payload"`
	Headers       map[string]string `json:"headers,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`

	// Internal tracking (not sent to publisher)
	RetryCount int    `json:"-"`
	LastError  string `json:"-"`
}

// Source is the interface for reading events from the outbox
type Source interface {
	// Start begins streaming events to the provided channel.
	// Implementations must respect ctx cancellation and close the channel when done.
	Start(ctx context.Context, out chan<- Event) error

	// Ack marks an event as successfully delivered.
	// The event is deleted or marked as processed in the outbox.
	Ack(ctx context.Context, eventID string) error

	// Nack marks an event as failed.
	// If retryable is true, the event will be retried after a backoff.
	// If retryable is false, the event is moved to the dead letter table immediately.
	Nack(ctx context.Context, eventID string, err error, retryable bool) error

	// Close performs cleanup (closing connections, etc.)
	Close() error
}
