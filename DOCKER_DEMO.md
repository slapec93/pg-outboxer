# pg-outboxer Docker Demo

Complete setup with PostgreSQL, pg-outboxer, Prometheus, and Grafana.

## Quick Start

```bash
# Start everything
docker-compose up -d

# Check logs
docker-compose logs -f pg-outboxer

# Access services
open http://localhost:3000  # Grafana (admin/admin)
open http://localhost:9091  # Prometheus
open http://localhost:9090/metrics  # pg-outboxer metrics
```

## Services

| Service | Port | Description |
|---------|------|-------------|
| **PostgreSQL** | 5432 | Database with outbox table |
| **pg-outboxer** | 9090 | Outbox relay + metrics |
| **webhook-receiver** | 8080 | Demo webhook endpoint |
| **Prometheus** | 9091 | Metrics collection |
| **Grafana** | 3000 | Visualization (admin/admin) |

## Setup Database

```bash
# Run setup command to create tables
docker-compose exec pg-outboxer ./pg-outboxer setup --config /app/config.yaml

# Or manually
docker-compose exec postgres psql -U postgres -d outbox_demo -c "
CREATE TABLE IF NOT EXISTS outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type VARCHAR(255) NOT NULL,
    aggregate_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    headers JSONB,
    status VARCHAR(50) DEFAULT 'pending',
    retry_count INT DEFAULT 0,
    retry_after TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    processed_at TIMESTAMP,
    INDEX idx_outbox_pending (created_at ASC) WHERE status = 'pending'
);

CREATE TABLE IF NOT EXISTS outbox_dead_letter (
    id UUID PRIMARY KEY,
    aggregate_type VARCHAR(255) NOT NULL,
    aggregate_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    headers JSONB,
    created_at TIMESTAMP NOT NULL,
    last_error TEXT NOT NULL,
    retry_count INT NOT NULL,
    moved_at TIMESTAMP DEFAULT NOW()
);
"
```

## Insert Test Events

```bash
# Insert a test event
docker-compose exec postgres psql -U postgres -d outbox_demo -c "
INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload, headers)
VALUES (
    'order',
    'order-123',
    'order.created',
    '{\"amount\": 100, \"currency\": \"USD\", \"customer_id\": \"cus_789\"}',
    '{\"source\": \"api\", \"user_id\": \"user_456\"}'
);
"

# Insert multiple events
docker-compose exec postgres psql -U postgres -d outbox_demo -c "
INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
SELECT
    'order',
    'order-' || generate_series,
    'order.created',
    jsonb_build_object('amount', (random() * 1000)::int, 'currency', 'USD')
FROM generate_series(1, 100);
"

# Watch webhook receiver logs
docker-compose logs -f webhook-receiver
```

## View Metrics

### Prometheus Queries

Open http://localhost:9091 and try these queries:

```promql
# Events processed per second
rate(pg_outboxer_events_processed_total[1m])

# Error rate
sum(rate(pg_outboxer_events_processed_total{outcome=~"failed.*"}[1m])) /
sum(rate(pg_outboxer_events_processed_total[1m])) * 100

# p95 latency
histogram_quantile(0.95, rate(pg_outboxer_publish_duration_seconds_bucket[1m]))

# Active workers
pg_outboxer_active_workers

# Dead letter events
pg_outboxer_dead_letter_events_total
```

### Grafana Dashboard

1. Open http://localhost:3000
2. Login: `admin` / `admin`
3. Go to **Dashboards → pg-outboxer Overview**

The dashboard includes:
- **Events Processed Rate** - throughput by outcome
- **Error Rate** - percentage of failures
- **Publish Latency** - p50, p95, p99 percentiles
- **Publisher Success Rate** - per-publisher metrics
- **Active Workers** - worker count
- **Dead Letter Events** - failed events count
- **Retry Distribution** - retry attempts histogram

## Test Scenarios

### 1. Normal Flow

```bash
# Insert event
docker-compose exec postgres psql -U postgres -d outbox_demo -c "
INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
VALUES ('order', 'order-999', 'order.created', '{\"amount\": 50}');
"

# Should see in webhook-receiver logs:
# ✅ Received event: id=... type=order.created aggregate=order/order-999
```

### 2. Simulate Webhook Failure

```bash
# Stop webhook receiver
docker-compose stop webhook-receiver

# Insert events (will fail and retry)
docker-compose exec postgres psql -U postgres -d outbox_demo -c "
INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
VALUES ('order', 'order-fail', 'order.failed', '{\"test\": true}');
"

# Watch pg-outboxer retry
docker-compose logs -f pg-outboxer
# Should see: "event scheduled for retry"

# Restart webhook receiver
docker-compose start webhook-receiver
# Event will eventually be delivered
```

### 3. Load Test

```bash
# Insert 1000 events
docker-compose exec postgres psql -U postgres -d outbox_demo -c "
INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
SELECT
    'order',
    'order-' || generate_series,
    'order.created',
    jsonb_build_object('amount', (random() * 1000)::int)
FROM generate_series(1, 1000);
"

# Watch Grafana dashboard for:
# - Throughput spike
# - Latency impact
# - Error rate (should stay low)
```

## Troubleshooting

### Check if services are healthy

```bash
docker-compose ps
docker-compose logs pg-outboxer
docker-compose logs postgres
```

### Check metrics endpoint

```bash
curl http://localhost:9090/metrics | grep pg_outboxer
curl http://localhost:9090/health  # Should return "OK"
```

### Check Prometheus targets

http://localhost:9091/targets - pg-outboxer should be "UP"

### Query outbox table

```bash
docker-compose exec postgres psql -U postgres -d outbox_demo -c "
SELECT id, event_type, status, retry_count, created_at
FROM outbox
ORDER BY created_at DESC
LIMIT 10;
"
```

### Query dead letter

```bash
docker-compose exec postgres psql -U postgres -d outbox_demo -c "
SELECT * FROM outbox_dead_letter ORDER BY moved_at DESC LIMIT 10;
"
```

## Cleanup

```bash
# Stop and remove containers
docker-compose down

# Remove volumes (deletes all data!)
docker-compose down -v
```

## Production Deployment

For production, consider:

1. **Database**: Use managed PostgreSQL (RDS, Cloud SQL)
2. **Monitoring**: Add Alertmanager for alerts
3. **Scaling**: Run multiple pg-outboxer instances
4. **Security**:
   - Use secrets management (not hardcoded passwords)
   - Enable TLS for Postgres connections
   - Set proper Grafana admin password
5. **Persistence**: Use persistent volumes for Prometheus
6. **Backup**: Regular backups of Postgres and Prometheus data
