package publisher

import (
	"context"

	"github.com/slapec93/pg-outboxer/internal/source"
)

// PublishResult contains the outcome of a publish attempt
type PublishResult struct {
	Success   bool   // true if event was successfully delivered
	Retryable bool   // if false, event should be moved to dead letter immediately
	ErrorMsg  string // error message if Success is false
}

// Publisher is the interface for publishing events to external systems
type Publisher interface {
	// Publish delivers an event to the configured destination.
	// Returns PublishResult indicating success, retryability, and error details.
	Publish(ctx context.Context, event source.Event) PublishResult

	// Name returns a human-readable name for this publisher (for logging/metrics)
	Name() string

	// Close performs cleanup (closing connections, etc.)
	Close() error
}
