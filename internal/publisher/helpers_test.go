// Package publisher test helpers.
package publisher

import (
	"context"

	"github.com/slapec93/pg-outboxer/internal/source"
)

// mockPublisher is a test double for the Publisher interface
type mockPublisher struct {
	name   string
	result PublishResult
	closed bool
}

func (m *mockPublisher) Publish(_ context.Context, _ source.Event) PublishResult {
	return m.result
}

func (m *mockPublisher) Name() string {
	return m.name
}

func (m *mockPublisher) Close() error {
	m.closed = true
	return nil
}
