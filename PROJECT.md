# Project Brief: `pg-outboxer`

A language-agnostic, lightweight sidecar binary that implements the Transactional Outbox Pattern for PostgreSQL.
No JVM, no Kafka required — just a single Go binary and a config file.

---

## The Problem

The **dual-write problem**: when an app needs to update the database AND publish an event to an external system,
there is no atomic transaction spanning both. If the process crashes between the two writes, you get either:
- A committed DB change with no event published (silent data loss downstream)
- An event published for a transaction that rolled back (phantom events)

The **Transactional Outbox Pattern** solves this by writing the event into the database *inside the same transaction*
as the business data. A separate relay process (this tool) reads from the outbox table and delivers events externally.

---

## Why this tool doesn't exist yet

- **Debezium** is the standard answer, but requires Kafka + Kafka Connect + JVM. Massive operational overhead.
- **italolelis/outboxer** (https://github.com/italolelis/outboxer) is an embedded Go library — not a sidecar,
  polling-only, no CDC/WAL support, no webhook publisher, no dead letter queue API, no Prometheus metrics.
  Last meaningful activity: 2023.
- Nothing exists as a standalone, language-agnostic binary with CDC support and webhook delivery.

**Target user**: Mid-size teams running Rails/Django/Laravel/Spring on Postgres who need reliable event delivery
without operating a Kafka cluster.

---

## Positioning

> `pg-outboxer` is a single binary you drop next to any service. Point it at your Postgres outbox table,
> give it a config file, and it handles reliable event delivery — with sub-millisecond latency via WAL-based CDC
> or simple polling as a fallback. No Kafka. No JVM. No library imports. Works with any language.

**Headline differentiators:**
1. **No Kafka required** — delivers to HTTP webhooks, Redis Streams, or Kafka (optional)
2. **CDC via Postgres logical replication** — WAL-based, not polling; zero added DB load
3. **Language-agnostic sidecar** — works with Rails, Django, Laravel, Spring, anything
4. **Single static binary** — deploy with Docker, Kubernetes sidecar, or bare metal

---

## Delivery Guarantees (understand this deeply — it belongs in the README)

### What we guarantee: At-Least-Once Delivery
After the outbox row is written, it will eventually be delivered. If the process crashes after delivery but before
marking the row as processed, it will be delivered again. **Consumers must be idempotent.**
Help consumers by providing a stable `event_id` (UUID set at insert time, not delivery time).

### What we do NOT guarantee: Exactly-Once Delivery
Impossible without consumer-side coordination. Any tool claiming this without consumer cooperation is lying.
The README must say this plainly.

### What we guarantee: Ordered Delivery Per Aggregate
All events for the same `aggregate_id` are delivered in order via a partition-key worker model.
Across different aggregates, delivery is parallelized freely.

---

## Database Schema

### Outbox table (app writes this, in same transaction as business data)
```sql
CREATE TABLE outbox (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type  TEXT NOT NULL,          -- e.g. 'payment', 'order'
    aggregate_id    TEXT NOT NULL,          -- e.g. 'pay_123'
    event_type      TEXT NOT NULL,          -- e.g. 'payment.confirmed'
    payload         JSONB NOT NULL,
    headers         JSONB,                  -- optional metadata/headers to forward
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- delivery tracking (managed by pg-outboxer, not the app)
    status          TEXT NOT NULL DEFAULT 'pending', -- pending | processing | delivered | failed | dead
    retry_count     INT NOT NULL DEFAULT 0,
    retry_after     TIMESTAMPTZ,
    last_error      TEXT,
    processed_at    TIMESTAMPTZ
);

CREATE INDEX idx_outbox_pending ON outbox (created_at ASC)
    WHERE status = 'pending' AND (retry_after IS NULL OR retry_after <= NOW());

CREATE INDEX idx_outbox_aggregate ON outbox (aggregate_id, created_at ASC);
```

### Dead letter table (pg-outboxer creates/manages this)
```sql
CREATE TABLE outbox_dead_letter (
    id              UUID PRIMARY KEY,
    aggregate_type  TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    headers         JSONB,
    created_at      TIMESTAMPTZ NOT NULL,
    failed_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    retry_count     INT NOT NULL
);
```

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                   pg-outboxer                       │
│                                                     │
│  ┌─────────────┐    ┌──────────────────────────┐   │
│  │   Source    │    │      Dispatcher          │   │
│  │             │    │                          │   │
│  │  polling /  │───▶│  partition-key router    │   │
│  │  cdc (WAL)  │    │  (worker per agg_id)     │   │
│  └─────────────┘    └──────────┬───────────────┘   │
│                                │                   │
│                    ┌───────────▼────────────┐      │
│                    │      Publisher         │      │
│                    │  webhook | redis | kafka│      │
│                    └───────────┬────────────┘      │
│                                │                   │
│                    ┌───────────▼────────────┐      │
│                    │   Retry Scheduler      │      │
│                    │   Dead Letter Queue    │      │
│                    └────────────────────────┘      │
│                                                     │
│  Prometheus metrics on :9090/metrics               │
└─────────────────────────────────────────────────────┘
```

### Go project structure
```
pg-outboxer/
├── cmd/
│   └── pg-outboxer/
│       └── main.go              # cobra CLI entrypoint
├── internal/
│   ├── config/
│   │   └── config.go            # YAML config parsing, env var interpolation
│   ├── source/
│   │   ├── source.go            # Source interface
│   │   ├── polling/
│   │   │   └── poller.go        # SELECT FOR UPDATE SKIP LOCKED polling
│   │   └── cdc/
│   │       ├── cdc.go           # CDC source (implements Source interface)
│   │       ├── replication.go   # pgx logical replication connection
│   │       └── pgoutput.go      # pgoutput protocol parser
│   ├── dispatcher/
│   │   └── dispatcher.go        # partition-key router, worker pool
│   ├── publisher/
│   │   ├── publisher.go         # Publisher interface
│   │   ├── webhook/
│   │   │   └── webhook.go       # HTTP delivery, HMAC signing, retryable/fatal errors
│   │   ├── redisstream/
│   │   │   └── redisstream.go   # XADD to Redis Streams
│   │   └── kafka/
│   │       └── kafka.go         # franz-go producer
│   ├── store/
│   │   └── store.go             # delivery state, retry tracking, dead letter
│   ├── scheduler/
│   │   └── scheduler.go         # exponential backoff with full jitter
│   └── metrics/
│       └── metrics.go           # Prometheus metrics definitions
├── docs/
│   ├── adr-001-polling-vs-cdc.md
│   ├── adr-002-delivery-guarantees.md
│   └── architecture.png
├── examples/
│   └── rails-webhook/
│       ├── docker-compose.yml   # postgres + rails app + pg-outboxer
│       └── README.md
├── config.example.yaml
├── Dockerfile
├── Makefile
├── README.md
└── go.mod
```

---

## Core Interfaces

```go
// source/source.go
type Event struct {
    ID            string
    AggregateType string
    AggregateID   string
    EventType     string
    Payload       []byte
    Headers       map[string]string
    OccurredAt    time.Time
    SequenceNum   int64
}

type Source interface {
    // Start begins streaming events. Implementations must respect ctx cancellation.
    Start(ctx context.Context, out chan<- Event) error
    // Ack marks an event as successfully delivered.
    Ack(ctx context.Context, eventID string) error
    // Nack marks an event for retry or dead-letter.
    Nack(ctx context.Context, eventID string, err error, retryable bool) error
}

// publisher/publisher.go
type PublishResult struct {
    Success   bool
    Retryable bool   // false = dead-letter immediately, true = schedule retry
    ErrorMsg  string
}

type Publisher interface {
    Publish(ctx context.Context, event Event) PublishResult
    Name() string
}
```

---

## Config File Format

```yaml
source:
  type: cdc                          # "cdc" or "polling"
  dsn: ${DATABASE_URL}               # env var interpolation supported
  table: outbox                      # outbox table name
  # CDC-specific
  slot_name: pg_outboxer_slot
  publication: pg_outboxer_pub
  # Polling-specific
  poll_interval: 500ms
  batch_size: 100

publisher:
  type: webhook                      # "webhook", "redis_stream", "kafka"
  url: https://my-service.com/events
  timeout: 10s
  signing_secret: ${WEBHOOK_SECRET}  # HMAC-SHA256 signature in X-Signature-256 header
  headers:
    Authorization: Bearer ${API_KEY}

delivery:
  workers: 4                         # parallel workers (partitioned by aggregate_id)
  max_retries: 10
  dead_letter_table: outbox_dead_letter

observability:
  metrics_port: 9090
  log_level: info                    # debug | info | warn | error
  log_format: json                   # json | text
```

---

## Retry Strategy

Exponential backoff with **full jitter** (not plain exponential — full jitter prevents thundering herd
when a downstream recovers and all backed-up messages retry simultaneously).

```go
func retryAfter(attempt int) time.Duration {
    cap := 30 * time.Minute
    base := 1 * time.Second
    expBackoff := min(float64(cap), float64(base) * math.Pow(2, float64(attempt)))
    // Full jitter: uniform random in [0, expBackoff]
    return time.Duration(rand.Int63n(int64(expBackoff)))
}
```

**Delivery states:**
```
PENDING → IN_FLIGHT → DELIVERED
                    ↘ FAILED (retryable: true)  → PENDING with retry_after = now + jitter
                    ↘ FAILED (retryable: false) → DEAD (moved to dead_letter table)
```

**Retryable vs fatal errors (webhook publisher):**
- `5xx`, timeouts, connection refused → retryable
- `4xx` (except 429) → fatal, dead-letter immediately
- `429` → retryable, respect `Retry-After` header if present

---

## Prometheus Metrics

```
pg_outboxer_events_processed_total{publisher, status}   # counter
pg_outboxer_events_inflight                             # gauge
pg_outboxer_delivery_latency_seconds{publisher}         # histogram (buckets: 10ms → 30s)
pg_outboxer_replication_lag_bytes                       # gauge (CDC mode only)
pg_outboxer_dead_letter_total                           # counter
pg_outboxer_retry_queue_depth                           # gauge
pg_outboxer_poll_duration_seconds                       # histogram (polling mode only)
```

---

## CDC Implementation Notes (the hard part)

### Setup requirements (document prominently)
```sql
-- Must be set in postgresql.conf
-- wal_level = logical

-- Run once:
SELECT pg_create_logical_replication_slot('pg_outboxer_slot', 'pgoutput');
CREATE PUBLICATION pg_outboxer_pub FOR TABLE outbox;
```

### pgoutput message flow
```
PrimaryKeepalive  → respond with StandbyStatusUpdate (or Postgres kills the connection after timeout)
XLogData contains:
  BeginMessage    → start buffering events for this transaction
  RelationMessage → table schema snapshot (sent when schema changes)
  InsertMessage   → new row data (buffer it, do NOT deliver yet)
  CommitMessage   → transaction committed → NOW deliver buffered events
```

**Critical**: Only process events on `CommitMessage`. An `InsertMessage` can still be rolled back.
Buffer all inserts per transaction, flush on commit, discard on rollback.

### Replication slot monitoring
If the slot is not consumed, Postgres retains all WAL from that point forever → disk fill risk.
Monitor `pg_replication_slots` and alert if `lag_bytes` grows unboundedly.

```sql
SELECT slot_name, active, pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn))
FROM pg_replication_slots
WHERE slot_name = 'pg_outboxer_slot';
```

---

## Partition-Key Worker Model (ordering guarantee)

All events for the same `aggregate_id` must be delivered in order.
Map each `aggregate_id` to a fixed worker via consistent hashing:

```go
workerIndex := fnv32(event.AggregateID) % uint32(numWorkers)
workerChannels[workerIndex] <- event
```

Each worker processes its channel sequentially. Events for the same aggregate always go to
the same worker → ordered delivery guaranteed. Events for different aggregates processed in parallel.

---

## Build Phases

### Phase 1 — Polling source + Webhook publisher (start here)
- Polling loop with `SELECT FOR UPDATE SKIP LOCKED`
- Webhook publisher with HMAC signing
- Retry scheduler with full jitter
- Dead letter table
- Basic Prometheus metrics
- Docker Compose example (Postgres + demo app + pg-outboxer)

### Phase 2 — CDC source
- Postgres logical replication via `pgx` replication protocol
- `pgoutput` message parsing (Begin/Relation/Insert/Commit)
- Transaction buffering before commit
- Replication slot keepalive
- Lag metrics

### Phase 3 — Additional publishers
- Redis Streams (`XADD`)
- Kafka (`franz-go` — prefer over sarama, actively maintained)

### Phase 4 — Polish & docs
- Full README with problem statement, guarantees, operational guide
- ADR documents
- `docs/architecture.png` (draw.io or excalidraw export)
- GitHub Actions CI (lint, test, build)
- Docker Hub / GitHub Container Registry image

---

## Key Go Dependencies

```
pgx/v5                  — Postgres driver + logical replication support
cobra                   — CLI framework
viper                   — config file + env var interpolation
prometheus/client_golang — metrics
go-redis/redis/v9       — Redis Streams publisher
twmb/franz-go           — Kafka publisher (prefer over sarama)
log/slog                — structured logging (stdlib, Go 1.21+)
```

---

## ADR-001: Polling vs CDC

**Polling pros:** Simple, no Postgres config changes, works on any Postgres version, easier to debug.
**Polling cons:** Latency floor = poll interval, constant DB queries even when idle, harder to scale horizontally.

**CDC pros:** Sub-millisecond latency, zero polling load, event appears exactly when transaction commits.
**CDC cons:** Requires `wal_level = logical` (needs Postgres restart to change), replication slot disk risk,
more complex implementation, requires monitoring.

**Decision:** Support both. Default to polling in the quickstart (zero config). CDC for production
workloads that need low latency or high throughput.

---

## ADR-002: Delivery Guarantees

**Why exactly-once is impossible:**
The relay reads from outbox, delivers to publisher, then marks as delivered. If the process crashes
after delivery but before marking as delivered, it will deliver again on restart. This is the
Two Generals' problem — there is no way to atomically "deliver and acknowledge" across two systems
without a distributed coordinator, which reintroduces the original problem.

**What we provide:**
- At-least-once delivery (durable, retryable, with dead letter)
- Ordered delivery per aggregate (partition-key worker model)
- Stable `event_id` to enable consumer-side idempotency checks

**What consumers must do:**
- Treat `event_id` as an idempotency key
- Store processed `event_id`s and skip duplicates

---

## README Outline (write this last, but plan it now)

1. **The problem** — dual-write, with a concrete payment example
2. **How it works** — diagram, outbox table, relay, delivery
3. **Quickstart** — Docker Compose up, working in 60 seconds
4. **Configuration reference** — all config.yaml options
5. **Delivery guarantees** — at-least-once, why exactly-once is impossible, ordering
6. **Operational guide** — CDC setup, replication slot monitoring, metrics
7. **Publishers** — webhook, Redis Streams, Kafka
8. **Comparison** — vs Debezium, vs italolelis/outboxer
9. **Contributing**

---

## Context for Claude Code Session

This is a **portfolio project** by a senior backend engineer (Rails/Go, 9+ years, fintech background).
The goal is a production-quality open source tool, not a toy.

Priorities:
- Correctness over cleverness
- Clean interface design (the `Source` and `Publisher` interfaces are the API)
- Real error handling — no `panic`, no swallowed errors
- Structured logging with `slog` throughout
- Tests for the retry scheduler, the dispatcher, and the webhook publisher
- Start with Phase 1 only — get it working end-to-end before touching CDC

**Start here:** `cmd/pg-outboxer/main.go` → `internal/config` → `internal/source/polling` →
`internal/publisher/webhook` → `internal/scheduler` → `internal/store` → wire it together in main.
