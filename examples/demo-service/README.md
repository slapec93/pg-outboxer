# pg-outboxer Demo

This example demonstrates the Transactional Outbox Pattern using `pg-outboxer`.

## Architecture

```
┌─────────────────┐
│  demo-service   │  POST /orders → writes order + outbox event in same transaction
│  (port 8080)    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   PostgreSQL    │  outbox table
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  pg-outboxer    │  reads from outbox, publishes to webhook
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│webhook-receiver │  receives events (logs them)
│  (port 8081)    │
└─────────────────┘
```

## What This Demonstrates

1. **Atomic writes** - Order creation and event publishing in same transaction
2. **At-least-once delivery** - Events are guaranteed to be delivered
3. **Ordered delivery** - Events for same order_id delivered in order
4. **Retry logic** - Failed deliveries are retried with exponential backoff
5. **Dead letter queue** - Events that fail too many times are moved to DLQ

## Quick Start

```bash
# Start everything (setup runs automatically)
docker compose up

# The stack will:
# 1. Start PostgreSQL
# 2. Run pg-outboxer setup (creates outbox tables)
# 3. Start demo-service, webhook-receiver, and pg-outboxer

# In another terminal, create an order
curl -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"customer_id": "cust_123", "total": 99.99, "items": ["item1", "item2"]}'

# Watch the logs to see:
# 1. demo-service writes order + outbox event
# 2. pg-outboxer picks up the event
# 3. webhook-receiver receives the event
```

## Endpoints

### Demo Service (port 8080)

- `POST /orders` - Create a new order
  ```json
  {
    "customer_id": "cust_123",
    "total": 99.99,
    "items": ["item1", "item2"]
  }
  ```

- `GET /orders/:id` - Get order by ID

- `GET /health` - Health check

### Webhook Receiver (port 8081)

- `POST /webhook` - Receives events from pg-outboxer

- `GET /events` - List all received events (for demo purposes)

### pg-outboxer Metrics (port 9090)

- `GET /metrics` - Prometheus metrics

## Testing Failure Scenarios

### 1. Simulate webhook failure

```bash
# Stop the webhook receiver
docker compose stop webhook-receiver

# Create an order - it will be written but delivery fails
curl -X POST http://localhost:8080/orders \
  -H "Content-Type: application/json" \
  -d '{"customer_id": "cust_456", "total": 49.99, "items": ["item3"]}'

# Watch pg-outboxer logs - you'll see retries with exponential backoff
docker compose logs -f pg-outboxer

# Restart webhook receiver - event will be delivered
docker compose start webhook-receiver
```

### 2. View retry metrics

```bash
# Check Prometheus metrics
curl http://localhost:9090/metrics | grep pg_outboxer
```

### 3. View dead letter queue

```bash
# Connect to database
docker compose exec postgres psql -U postgres -d demo

# Check dead letter table
SELECT * FROM outbox_dead_letter;
```

## Database Schema

The demo uses two tables:

**orders** - Business data
```sql
CREATE TABLE orders (
    id UUID PRIMARY KEY,
    customer_id TEXT NOT NULL,
    total DECIMAL(10,2) NOT NULL,
    items JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

**outbox** - Event outbox (written in same transaction)
```sql
CREATE TABLE outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status TEXT NOT NULL DEFAULT 'pending',
    retry_count INT NOT NULL DEFAULT 0,
    ...
);
```

## Cleanup

```bash
docker compose down -v
```

## Next Steps

- Modify `config.yaml` to experiment with different settings
- Try Redis Streams or Kafka publisher (once implemented)
- Enable CDC mode for lower latency
- Add more event types (order.shipped, order.cancelled, etc.)
