package delivery

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/slapec93/pg-outboxer/internal/publisher"
	"github.com/slapec93/pg-outboxer/internal/source"
)

func TestDispatcher_AllEventsSucceed(t *testing.T) {
	src := &mockSource{
		events: []source.Event{
			{ID: "evt-1", AggregateID: "agg-1", EventType: "test"},
			{ID: "evt-2", AggregateID: "agg-2", EventType: "test"},
			{ID: "evt-3", AggregateID: "agg-1", EventType: "test"},
		},
	}

	pub := &mockPublisher{
		name:   "test-publisher",
		result: publisher.PublishResult{Success: true},
	}

	dispatcher := New(src, pub, 2, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start dispatcher (will run until context is cancelled)
	err := dispatcher.Start(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "dispatcher should timeout")

	// Wait a bit for async processing
	time.Sleep(50 * time.Millisecond)

	// Verify all events were published
	published := pub.getPublished()
	assert.Len(t, published, 3, "all events should be published")

	// Verify all events were acked
	acked := src.getAcked()
	assert.Len(t, acked, 3, "all events should be acked")

	// Verify no events were nacked
	nacked := src.getNacked()
	assert.Empty(t, nacked, "no events should be nacked")
}

func TestDispatcher_SomeEventsFail(t *testing.T) {
	src := &mockSource{
		events: []source.Event{
			{ID: "evt-1", AggregateID: "agg-1", EventType: "test"},
			{ID: "evt-2", AggregateID: "agg-2", EventType: "test"},
		},
	}

	// Publisher that fails
	pub := &mockPublisher{
		name: "test-publisher",
		result: publisher.PublishResult{
			Success:   false,
			Retryable: true,
			ErrorMsg:  "temporary error",
		},
	}

	dispatcher := New(src, pub, 2, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() { _ = dispatcher.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)

	// Verify all events were published (attempted)
	published := pub.getPublished()
	assert.Len(t, published, 2, "both events should be attempted")

	// Verify no events were acked
	acked := src.getAcked()
	assert.Empty(t, acked, "failed events should not be acked")

	// Verify all events were nacked
	nacked := src.getNacked()
	require.Len(t, nacked, 2, "both events should be nacked")

	// Verify nacked events are retryable
	for id, ne := range nacked {
		assert.True(t, ne.retryable, "event %s should be retryable", id)
		assert.EqualError(t, ne.err, "temporary error", "event %s should have correct error", id)
	}
}

func TestDispatcher_FatalError(t *testing.T) {
	src := &mockSource{
		events: []source.Event{
			{ID: "evt-1", AggregateID: "agg-1", EventType: "test"},
		},
	}

	// Publisher that fails with fatal error
	pub := &mockPublisher{
		name: "test-publisher",
		result: publisher.PublishResult{
			Success:   false,
			Retryable: false,
			ErrorMsg:  "fatal error",
		},
	}

	dispatcher := New(src, pub, 1, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() { _ = dispatcher.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)

	// Verify event was nacked as non-retryable
	nacked := src.getNacked()
	require.Len(t, nacked, 1, "event should be nacked")

	ne := nacked["evt-1"]
	assert.False(t, ne.retryable, "fatal error should not be retryable")
}

func TestDispatcher_SelectWorker(t *testing.T) {
	src := &mockSource{}
	pub := &mockPublisher{name: "test"}

	dispatcher := New(src, pub, 4, 10)

	// Test that same aggregate_id always goes to same worker
	worker1 := dispatcher.selectWorker("order-123")
	worker2 := dispatcher.selectWorker("order-123")
	worker3 := dispatcher.selectWorker("order-123")

	assert.Equal(t, worker1, worker2, "same aggregate should route to same worker")
	assert.Equal(t, worker2, worker3, "same aggregate should route to same worker")

	// Test that different aggregate_ids can go to different workers
	// (not guaranteed, but likely with 4 workers and different IDs)
	workers := make(map[int]bool)
	for i := range 100 {
		workerID := dispatcher.selectWorker(fmt.Sprintf("agg-%d", i))
		workers[workerID] = true
	}

	// With 100 different aggregates and 4 workers, we should use multiple workers
	assert.GreaterOrEqual(t, len(workers), 2, "hash should distribute across multiple workers")

	// Test that worker ID is within bounds
	for i := range 20 {
		workerID := dispatcher.selectWorker(fmt.Sprintf("test-%d", i))
		assert.GreaterOrEqual(t, workerID, 0, "worker ID should be >= 0")
		assert.Less(t, workerID, 4, "worker ID should be < 4")
	}
}

func TestDispatcher_GracefulShutdown(t *testing.T) {
	src := &mockSource{
		events: []source.Event{
			{ID: "evt-1", AggregateID: "agg-1", EventType: "test"},
			{ID: "evt-2", AggregateID: "agg-2", EventType: "test"},
		},
	}

	pub := &mockPublisher{
		name:   "test-publisher",
		result: publisher.PublishResult{Success: true},
	}

	dispatcher := New(src, pub, 2, 10)

	ctx, cancel := context.WithCancel(context.Background())

	// Start dispatcher in background
	done := make(chan error, 1)
	go func() {
		done <- dispatcher.Start(ctx)
	}()

	// Let it process some events
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Wait for shutdown
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled, "should return Canceled error on graceful shutdown")
	case <-time.After(1 * time.Second):
		t.Fatal("shutdown timed out")
	}

	// Verify events were processed before shutdown
	acked := src.getAcked()
	assert.Len(t, acked, 2, "events should be processed before shutdown")
}

func TestDispatcher_WorkerCount(t *testing.T) {
	src := &mockSource{}
	pub := &mockPublisher{name: "test"}

	tests := []int{1, 2, 4, 8, 16}

	for _, numWorkers := range tests {
		t.Run(fmt.Sprintf("workers=%d", numWorkers), func(t *testing.T) {
			dispatcher := New(src, pub, numWorkers, 5)

			assert.Len(t, dispatcher.workers, numWorkers, "should create correct number of workers")

			// Verify worker IDs are sequential
			for i, w := range dispatcher.workers {
				assert.Equal(t, i, w.id, "worker %d should have sequential ID", i)
			}
		})
	}
}
