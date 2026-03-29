package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// EventsProcessed tracks total events processed by outcome
	EventsProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pg_outboxer_events_processed_total",
			Help: "Total number of events processed",
		},
		[]string{"outcome"}, // success, failed_retryable, failed_fatal
	)

	// EventsPublished tracks events published per publisher
	EventsPublished = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pg_outboxer_events_published_total",
			Help: "Total number of events published per publisher",
		},
		[]string{"publisher", "status"}, // publisher name, status (success, error)
	)

	// PublishDuration tracks webhook publish latency
	PublishDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pg_outboxer_publish_duration_seconds",
			Help:    "Time taken to publish events",
			Buckets: prometheus.DefBuckets, // 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10
		},
		[]string{"publisher"},
	)

	// EventRetries tracks retry attempts
	EventRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pg_outboxer_event_retries_total",
			Help: "Total number of event retry attempts",
		},
		[]string{"retry_count"}, // "1", "2", "3", etc.
	)

	// DeadLetterEvents tracks events moved to dead letter
	DeadLetterEvents = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "pg_outboxer_dead_letter_events_total",
			Help: "Total number of events moved to dead letter queue",
		},
	)

	// ActiveWorkers tracks number of active workers
	ActiveWorkers = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "pg_outboxer_active_workers",
			Help: "Number of active worker goroutines",
		},
	)

	// SourcePollDuration tracks how long source polling takes
	SourcePollDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "pg_outboxer_source_poll_duration_seconds",
			Help:    "Time taken to poll source for events",
			Buckets: prometheus.DefBuckets,
		},
	)

	// QueueDepth tracks approximate number of events waiting
	QueueDepth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pg_outboxer_queue_depth",
			Help: "Approximate number of events in queue",
		},
		[]string{"queue"}, // "pending", "processing"
	)
)

// RecordEventSuccess records a successful event delivery
func RecordEventSuccess() {
	EventsProcessed.WithLabelValues("success").Inc()
}

// RecordEventFailure records a failed event delivery
func RecordEventFailure(retryable bool) {
	if retryable {
		EventsProcessed.WithLabelValues("failed_retryable").Inc()
	} else {
		EventsProcessed.WithLabelValues("failed_fatal").Inc()
	}
}

// RecordPublish records a publish attempt
func RecordPublish(publisherName string, success bool, duration float64) {
	status := "error"
	if success {
		status = "success"
	}
	EventsPublished.WithLabelValues(publisherName, status).Inc()
	PublishDuration.WithLabelValues(publisherName).Observe(duration)
}

// RecordRetry records a retry attempt
func RecordRetry(retryCount int) {
	// Cap at 10+ for cardinality
	label := "10+"
	if retryCount < 10 {
		label = string(rune('0' + retryCount))
	}
	EventRetries.WithLabelValues(label).Inc()
}

// RecordDeadLetter records an event moved to dead letter
func RecordDeadLetter() {
	DeadLetterEvents.Inc()
}
