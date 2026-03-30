// Package metrics provides Prometheus metrics for monitoring pg-outboxer.
package metrics

import (
	"fmt"

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

	// CDC-specific metrics

	// ReplicationLagBytes tracks CDC replication lag in bytes
	ReplicationLagBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "pg_outboxer_replication_lag_bytes",
			Help: "CDC replication lag in bytes (distance from current WAL position)",
		},
	)

	// ReplicationSlotActive indicates if replication connection is active
	ReplicationSlotActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "pg_outboxer_replication_slot_active",
			Help: "Whether the CDC replication slot is active (1=active, 0=inactive)",
		},
	)

	// WALMessagesReceived tracks total WAL messages received
	WALMessagesReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pg_outboxer_wal_messages_total",
			Help: "Total number of WAL messages received by type",
		},
		[]string{"type"}, // xlog_data, keepalive, relation, insert, update, delete, etc.
	)

	// CDCEventsDecoded tracks events successfully decoded from WAL
	CDCEventsDecoded = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "pg_outboxer_cdc_events_decoded_total",
			Help: "Total number of events successfully decoded from CDC stream",
		},
	)

	// CDCDecodingErrors tracks decoding failures
	CDCDecodingErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pg_outboxer_cdc_decoding_errors_total",
			Help: "Total number of CDC decoding errors by type",
		},
		[]string{"error_type"}, // parse_error, invalid_schema, etc.
	)

	// CDCConnectionErrors tracks connection issues
	CDCConnectionErrors = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "pg_outboxer_cdc_connection_errors_total",
			Help: "Total number of CDC connection errors",
		},
	)

	// LastWALPosition tracks the last processed WAL LSN
	LastWALPosition = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "pg_outboxer_last_wal_position",
			Help: "Last processed WAL LSN position (Log Sequence Number)",
		},
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
		label = fmt.Sprintf("%d", retryCount)
	}
	EventRetries.WithLabelValues(label).Inc()
}

// RecordDeadLetter records an event moved to dead letter
func RecordDeadLetter() {
	DeadLetterEvents.Inc()
}

// CDC Metrics Functions

// RecordReplicationLag updates the replication lag in bytes
func RecordReplicationLag(lagBytes int64) {
	ReplicationLagBytes.Set(float64(lagBytes))
}

// RecordReplicationActive sets whether replication is active
func RecordReplicationActive(active bool) {
	if active {
		ReplicationSlotActive.Set(1)
	} else {
		ReplicationSlotActive.Set(0)
	}
}

// RecordWALMessage records a WAL message received
func RecordWALMessage(msgType string) {
	WALMessagesReceived.WithLabelValues(msgType).Inc()
}

// RecordCDCEventDecoded records a successfully decoded CDC event
func RecordCDCEventDecoded() {
	CDCEventsDecoded.Inc()
}

// RecordCDCDecodingError records a decoding error
func RecordCDCDecodingError(errorType string) {
	CDCDecodingErrors.WithLabelValues(errorType).Inc()
}

// RecordCDCConnectionError records a connection error
func RecordCDCConnectionError() {
	CDCConnectionErrors.Inc()
}

// RecordWALPosition records the last processed WAL position
func RecordWALPosition(lsn uint64) {
	LastWALPosition.Set(float64(lsn))
}
