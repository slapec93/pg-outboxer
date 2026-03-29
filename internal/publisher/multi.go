package publisher

import (
	"context"
	"fmt"
	"strings"

	"github.com/slapec93/pg-outboxer/internal/source"
)

// Multi wraps multiple publishers and delivers events to all of them.
// It implements the Publisher interface and uses fan-out semantics:
// - Success only if ALL publishers succeed
// - Retryable if ANY publisher fails with a retryable error
// - Fatal only if ALL publishers fail with fatal errors
type Multi struct {
	publishers []Publisher
	name       string
}

// NewMulti creates a new multi-publisher from a slice of publishers
func NewMulti(publishers []Publisher) *Multi {
	// Build a name from all wrapped publishers
	names := make([]string, len(publishers))
	for i, pub := range publishers {
		names[i] = pub.Name()
	}

	return &Multi{
		publishers: publishers,
		name:       fmt.Sprintf("multi[%s]", strings.Join(names, ",")),
	}
}

// Publish sends the event to all configured publishers.
// Returns success only if all publishers succeed.
// Returns retryable if any publisher fails with a retryable error.
// Returns fatal only if all publishers fail with fatal errors.
func (m *Multi) Publish(ctx context.Context, event source.Event) PublishResult {
	if len(m.publishers) == 0 {
		return PublishResult{
			Success:   false,
			Retryable: false,
			ErrorMsg:  "no publishers configured",
		}
	}

	// Single publisher - fast path
	if len(m.publishers) == 1 {
		return m.publishers[0].Publish(ctx, event)
	}

	// Collect results from all publishers
	allSuccess := true
	anyRetryable := false
	errors := []string{}

	for _, pub := range m.publishers {
		result := pub.Publish(ctx, event)

		if !result.Success {
			allSuccess = false
			errors = append(errors, fmt.Sprintf("%s: %s", pub.Name(), result.ErrorMsg))

			if result.Retryable {
				anyRetryable = true
			}
		}
	}

	// All succeeded
	if allSuccess {
		return PublishResult{
			Success:   true,
			Retryable: false,
			ErrorMsg:  "",
		}
	}

	// Some failed - determine if retryable
	errorMsg := strings.Join(errors, "; ")

	if anyRetryable {
		return PublishResult{
			Success:   false,
			Retryable: true,
			ErrorMsg:  errorMsg,
		}
	}

	// All failed with fatal errors
	return PublishResult{
		Success:   false,
		Retryable: false,
		ErrorMsg:  errorMsg,
	}
}

// Name returns the composite name of all wrapped publishers
func (m *Multi) Name() string {
	return m.name
}

// Close closes all wrapped publishers
func (m *Multi) Close() error {
	var errors []string

	for _, pub := range m.publishers {
		if err := pub.Close(); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", pub.Name(), err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to close publishers: %s", strings.Join(errors, "; "))
	}

	return nil
}
