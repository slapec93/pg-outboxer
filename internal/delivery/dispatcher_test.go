package delivery

import (
	"context"
	"fmt"
	"testing"
	"time"

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
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}

	// Wait a bit for async processing
	time.Sleep(50 * time.Millisecond)

	// Verify all events were published
	published := pub.getPublished()
	if len(published) != 3 {
		t.Errorf("expected 3 published events, got %d", len(published))
	}

	// Verify all events were acked
	acked := src.getAcked()
	if len(acked) != 3 {
		t.Errorf("expected 3 acked events, got %d", len(acked))
	}

	// Verify no events were nacked
	nacked := src.getNacked()
	if len(nacked) != 0 {
		t.Errorf("expected 0 nacked events, got %d", len(nacked))
	}
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

	dispatcher.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Verify all events were published (attempted)
	published := pub.getPublished()
	if len(published) != 2 {
		t.Errorf("expected 2 published events, got %d", len(published))
	}

	// Verify no events were acked
	acked := src.getAcked()
	if len(acked) != 0 {
		t.Errorf("expected 0 acked events, got %d", len(acked))
	}

	// Verify all events were nacked
	nacked := src.getNacked()
	if len(nacked) != 2 {
		t.Errorf("expected 2 nacked events, got %d", len(nacked))
	}

	// Verify nacked events are retryable
	for id, ne := range nacked {
		if !ne.retryable {
			t.Errorf("expected event %s to be retryable", id)
		}
		if ne.err.Error() != "temporary error" {
			t.Errorf("expected error 'temporary error', got '%s'", ne.err.Error())
		}
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

	dispatcher.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Verify event was nacked as non-retryable
	nacked := src.getNacked()
	if len(nacked) != 1 {
		t.Fatalf("expected 1 nacked event, got %d", len(nacked))
	}

	ne := nacked["evt-1"]
	if ne.retryable {
		t.Error("expected event to be non-retryable (fatal)")
	}
}

func TestDispatcher_SelectWorker(t *testing.T) {
	src := &mockSource{}
	pub := &mockPublisher{name: "test"}

	dispatcher := New(src, pub, 4, 10)

	// Test that same aggregate_id always goes to same worker
	worker1 := dispatcher.selectWorker("order-123")
	worker2 := dispatcher.selectWorker("order-123")
	worker3 := dispatcher.selectWorker("order-123")

	if worker1 != worker2 || worker2 != worker3 {
		t.Errorf("expected same worker for same aggregate_id, got %d, %d, %d", worker1, worker2, worker3)
	}

	// Test that different aggregate_ids can go to different workers
	// (not guaranteed, but likely with 4 workers and different IDs)
	workers := make(map[int]bool)
	for i := range 100 {
		workerID := dispatcher.selectWorker(fmt.Sprintf("agg-%d", i))
		workers[workerID] = true
	}

	// With 100 different aggregates and 4 workers, we should use multiple workers
	if len(workers) < 2 {
		t.Error("expected hash to distribute across multiple workers")
	}

	// Test that worker ID is within bounds
	for i := range 20 {
		workerID := dispatcher.selectWorker(fmt.Sprintf("test-%d", i))
		if workerID < 0 || workerID >= 4 {
			t.Errorf("worker ID %d out of bounds [0, 4)", workerID)
		}
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
		if err != context.Canceled {
			t.Errorf("expected Canceled error, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("shutdown timed out")
	}

	// Verify events were processed before shutdown
	acked := src.getAcked()
	if len(acked) != 2 {
		t.Errorf("expected 2 acked events after shutdown, got %d", len(acked))
	}
}

func TestDispatcher_WorkerCount(t *testing.T) {
	src := &mockSource{}
	pub := &mockPublisher{name: "test"}

	tests := []int{1, 2, 4, 8, 16}

	for _, numWorkers := range tests {
		t.Run(fmt.Sprintf("workers=%d", numWorkers), func(t *testing.T) {
			dispatcher := New(src, pub, numWorkers, 5)

			if len(dispatcher.workers) != numWorkers {
				t.Errorf("expected %d workers, got %d", numWorkers, len(dispatcher.workers))
			}

			// Verify worker IDs
			for i, w := range dispatcher.workers {
				if w.id != i {
					t.Errorf("worker %d has wrong id: %d", i, w.id)
				}
			}
		})
	}
}
