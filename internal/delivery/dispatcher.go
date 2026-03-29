package delivery

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"

	"github.com/slapec93/pg-outboxer/internal/metrics"
	"github.com/slapec93/pg-outboxer/internal/publisher"
	"github.com/slapec93/pg-outboxer/internal/source"
)

// Dispatcher routes events from a source to workers, partitioned by aggregate_id.
// This ensures ordered delivery per aggregate while allowing parallel processing.
type Dispatcher struct {
	source    source.Source
	publisher publisher.Publisher
	workers   []*worker
	wg        sync.WaitGroup
}

// New creates a new dispatcher with the specified number of workers
func New(src source.Source, pub publisher.Publisher, numWorkers, bufferSize int) *Dispatcher {
	workers := make([]*worker, numWorkers)
	for i := range numWorkers {
		workers[i] = &worker{
			id:        i,
			eventChan: make(chan source.Event, bufferSize),
			source:    src,
			publisher: pub,
		}
	}

	return &Dispatcher{
		source:    src,
		publisher: pub,
		workers:   workers,
	}
}

// Start begins dispatching events to workers.
// Blocks until ctx is cancelled or an error occurs.
func (d *Dispatcher) Start(ctx context.Context) error {
	slog.Info("starting dispatcher",
		"workers", len(d.workers),
		"publisher", d.publisher.Name())

	// Record active workers metric
	metrics.ActiveWorkers.Set(float64(len(d.workers)))

	// Start all workers
	for _, w := range d.workers {
		d.wg.Add(1)
		go func(worker *worker) {
			defer d.wg.Done()
			worker.run(ctx)
		}(w)
	}

	// Start receiving events from source
	eventChan := make(chan source.Event, 100)
	errChan := make(chan error, 1)

	go func() {
		if err := d.source.Start(ctx, eventChan); err != nil {
			errChan <- err
		}
	}()

	// Dispatch events to workers
	for {
		select {
		case <-ctx.Done():
			slog.Info("dispatcher shutting down", "reason", ctx.Err())
			d.shutdown()
			return ctx.Err()

		case err := <-errChan:
			slog.Error("source error", "error", err)
			d.shutdown()
			return fmt.Errorf("source error: %w", err)

		case event := <-eventChan:
			// Route event to worker based on aggregate_id
			workerID := d.selectWorker(event.AggregateID)
			d.workers[workerID].eventChan <- event
		}
	}
}

// selectWorker determines which worker should handle an event.
// Uses consistent hashing on aggregate_id to ensure ordering per aggregate.
func (d *Dispatcher) selectWorker(aggregateID string) int {
	h := fnv.New32a()
	h.Write([]byte(aggregateID))
	return int(h.Sum32()) % len(d.workers)
}

// shutdown gracefully stops all workers
func (d *Dispatcher) shutdown() {
	slog.Info("shutting down workers", "count", len(d.workers))

	// Close all worker channels
	for _, w := range d.workers {
		close(w.eventChan)
	}

	// Wait for workers to finish processing
	d.wg.Wait()

	slog.Info("all workers stopped")
}

// worker processes events from its channel
type worker struct {
	id        int
	eventChan chan source.Event
	source    source.Source
	publisher publisher.Publisher
}

// run processes events until the channel is closed
func (w *worker) run(ctx context.Context) {
	slog.Debug("worker started", "worker_id", w.id)

	for event := range w.eventChan {
		w.processEvent(ctx, event)
	}

	slog.Debug("worker stopped", "worker_id", w.id)
}

// processEvent publishes an event and acks/nacks based on the result
func (w *worker) processEvent(ctx context.Context, event source.Event) {
	slog.Debug("processing event",
		"worker_id", w.id,
		"event_id", event.ID,
		"aggregate_id", event.AggregateID,
		"event_type", event.EventType)

	// Publish to all publishers
	result := w.publisher.Publish(ctx, event)

	if result.Success {
		// Success - acknowledge the event
		metrics.RecordEventSuccess()
		if err := w.source.Ack(ctx, event.ID); err != nil {
			slog.Error("failed to ack event",
				"worker_id", w.id,
				"event_id", event.ID,
				"error", err)
		}
	} else {
		// Failure - nack with retryability info
		metrics.RecordEventFailure(result.Retryable)
		publishErr := fmt.Errorf("%s", result.ErrorMsg)
		if err := w.source.Nack(ctx, event.ID, publishErr, result.Retryable); err != nil {
			slog.Error("failed to nack event",
				"worker_id", w.id,
				"event_id", event.ID,
				"error", err)
		}
	}
}
