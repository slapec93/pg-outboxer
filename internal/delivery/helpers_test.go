package delivery

import (
	"context"
	"sync"

	"github.com/slapec93/pg-outboxer/internal/publisher"
	"github.com/slapec93/pg-outboxer/internal/source"
)

// mockSource implements source.Source for testing
type mockSource struct {
	events     []source.Event
	acked      map[string]bool
	nacked     map[string]nackedEvent
	mu         sync.Mutex
	startCalls int
}

type nackedEvent struct {
	err       error
	retryable bool
}

func (m *mockSource) Start(ctx context.Context, out chan<- source.Event) error {
	m.mu.Lock()
	m.startCalls++
	m.mu.Unlock()

	// Send all events
	for _, event := range m.events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- event:
		}
	}

	// Keep running until context is cancelled
	<-ctx.Done()
	return ctx.Err()
}

func (m *mockSource) Ack(ctx context.Context, eventID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.acked == nil {
		m.acked = make(map[string]bool)
	}
	m.acked[eventID] = true
	return nil
}

func (m *mockSource) Nack(ctx context.Context, eventID string, err error, retryable bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nacked == nil {
		m.nacked = make(map[string]nackedEvent)
	}
	m.nacked[eventID] = nackedEvent{err: err, retryable: retryable}
	return nil
}

func (m *mockSource) Close() error {
	return nil
}

func (m *mockSource) getAcked() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []string
	for id := range m.acked {
		result = append(result, id)
	}
	return result
}

func (m *mockSource) getNacked() map[string]nackedEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]nackedEvent)
	for id, ne := range m.nacked {
		result[id] = ne
	}
	return result
}

// mockPublisher implements publisher.Publisher for testing
type mockPublisher struct {
	name      string
	result    publisher.PublishResult
	published []source.Event
	mu        sync.Mutex
}

func (m *mockPublisher) Publish(ctx context.Context, event source.Event) publisher.PublishResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, event)
	return m.result
}

func (m *mockPublisher) Name() string {
	return m.name
}

func (m *mockPublisher) Close() error {
	return nil
}

func (m *mockPublisher) getPublished() []source.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]source.Event, len(m.published))
	copy(result, m.published)
	return result
}
