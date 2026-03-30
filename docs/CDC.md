# Change Data Capture (CDC) Mode

CDC mode provides **sub-millisecond latency** event delivery by streaming changes directly from PostgreSQL's Write-Ahead Log (WAL) using logical replication.

## Why CDC?

**Polling Mode:**
- ✅ Simple setup
- ✅ Works everywhere
- ❌ Higher latency (poll interval: 100ms - 1s)
- ❌ Constant database load

**CDC Mode:**
- ✅ Sub-millisecond latency (~1-10ms)
- ✅ Zero polling overhead
- ✅ Real-time event streaming
- ❌ Requires logical replication setup
- ❌ More complex infrastructure

## Prerequisites

### 1. PostgreSQL Configuration

Enable logical replication in `postgresql.conf`:

```ini
# Logical replication settings
wal_level = logical
max_replication_slots = 4
max_wal_senders = 4
```

**Restart PostgreSQL** after making these changes:
```bash
# Linux/systemd
sudo systemctl restart postgresql

# macOS/Homebrew
brew services restart postgresql

# Docker
docker restart postgres-container
```

### 2. Verify Settings

```sql
-- Check wal_level
SHOW wal_level;  -- Should be 'logical'

-- Check replication slots available
SHOW max_replication_slots;  -- Should be > 0
```

### 3. User Permissions

The database user needs replication permissions:

```sql
-- Grant replication privilege
ALTER USER myuser WITH REPLICATION;

-- Or create a dedicated replication user
CREATE USER pg_outboxer_repl WITH REPLICATION PASSWORD 'secure_password';
GRANT SELECT ON outbox TO pg_outboxer_repl;
GRANT SELECT ON outbox_dead_letter TO pg_outboxer_repl;
```

## Setup

### 1. Create Publication and Replication Slot

Use the `setup` command with `--cdc` flag:

```bash
pg-outboxer setup --config=config.cdc.yaml --cdc
```

This creates:
- **Publication** (`pg_outboxer_pub`) - Defines which tables to replicate
- **Replication slot** (`pg_outboxer_slot`) - Maintains WAL position

**Manual setup:**
```sql
-- Create publication
CREATE PUBLICATION pg_outboxer_pub FOR TABLE outbox;

-- Verify publication
SELECT * FROM pg_publication WHERE pubname = 'pg_outboxer_pub';

-- Replication slot is created automatically on first connection
```

### 2. Configure pg-outboxer

Create `config.cdc.yaml`:

```yaml
source:
  type: cdc
  dsn: postgres://user:password@localhost:5432/mydb
  table: outbox
  slot_name: pg_outboxer_slot
  publication: pg_outboxer_pub

publishers:
  - name: webhook
    type: webhook
    url: https://api.example.com/events
    timeout: 10s

delivery:
  workers: 4
  max_retries: 10
  dead_letter_table: outbox_dead_letter

observability:
  metrics_port: 9090
  log_level: info
```

### 3. Start pg-outboxer

```bash
pg-outboxer run --config=config.cdc.yaml
```

You should see:
```
INFO cdc source initialized slot_name=pg_outboxer_slot publication=pg_outboxer_pub
INFO replication slot already exists name=pg_outboxer_slot
INFO replication started lsn=0/1234567
INFO starting cdc stream slot=pg_outboxer_slot publication=pg_outboxer_pub
```

## How It Works

```
┌─────────────────────────────────────────────────────────┐
│                     PostgreSQL                          │
│                                                         │
│  ┌─────────────┐                                        │
│  │   Outbox    │────────┐                               │
│  │    Table    │        │                               │
│  └─────────────┘        │                               │
│                         ▼                               │
│                  ┌─────────────┐                        │
│                  │     WAL     │                        │
│                  │  (Logical)  │                        │
│                  └──────┬──────┘                        │
│                         │                               │
│                         │ Replication                   │
│                  ┌──────▼─────────┐                     │
│                  │ Replication    │                     │
│                  │ Slot           │                     │
│                  │ (pg_outboxer)  │                     │
│                  └──────┬─────────┘                     │
└─────────────────────────┼─────────────────────────────┘
                          │ pglogrepl protocol
                          ▼
                  ┌───────────────┐
                  │  pg-outboxer  │
                  │  (CDC Source) │
                  └───────┬───────┘
                          │
                          ▼
                  ┌───────────────┐
                  │  Dispatcher   │
                  │  + Workers    │
                  └───────┬───────┘
                          │
                          ▼
                  ┌───────────────┐
                  │  Publishers   │
                  │  (Webhook)    │
                  └───────────────┘
```

**Flow:**
1. Application inserts into `outbox` table
2. PostgreSQL writes to WAL
3. Logical replication decodes WAL → structured changes
4. pg-outboxer receives changes via replication protocol
5. Events immediately dispatched to publishers

**Latency:** ~1-10ms from commit to publisher

## Monitoring

### Replication Lag

Check replication lag:

```sql
SELECT
    slot_name,
    active,
    pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) as lag,
    pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)) as flush_lag
FROM pg_replication_slots
WHERE slot_name = 'pg_outboxer_slot';
```

**Expected:**
- `active`: `t` (true)
- `lag`: < 1 MB under normal load
- `flush_lag`: ~0 bytes (events consumed immediately)

### Metrics

Prometheus metrics on `:9090/metrics`:

```promql
# Replication lag (bytes)
pg_outboxer_replication_lag_bytes

# Replication slot active (1=active, 0=inactive)
pg_outboxer_replication_slot_active

# Last processed WAL position (LSN)
pg_outboxer_last_wal_position

# WAL messages received by type
rate(pg_outboxer_wal_messages_total[1m])

# Events successfully decoded from CDC
rate(pg_outboxer_cdc_events_decoded_total[1m])

# CDC decoding errors by type
rate(pg_outboxer_cdc_decoding_errors_total[1m])

# CDC connection errors
rate(pg_outboxer_cdc_connection_errors_total[1m])

# Overall event throughput
rate(pg_outboxer_events_processed_total[1m])
```

**Key metrics to monitor:**
- `pg_outboxer_replication_lag_bytes` - Should be < 1MB
- `pg_outboxer_replication_slot_active` - Should be 1 (active)
- `pg_outboxer_cdc_decoding_errors_total` - Should be 0
- `pg_outboxer_cdc_connection_errors_total` - Should be 0

### Logs

```bash
# Watch CDC logs
tail -f /var/log/pg-outboxer.log | grep cdc

# Expected output
INFO cdc event published event_id=123 type=order.created aggregate=order/456
```

## Troubleshooting

### Slot Not Found Error

```
ERROR failed to start replication: replication slot "pg_outboxer_slot" does not exist
```

**Fix:**
```bash
pg-outboxer setup --config=config.cdc.yaml --cdc
```

### Permission Denied

```
ERROR failed to connect with replication: permission denied for replication
```

**Fix:**
```sql
ALTER USER myuser WITH REPLICATION;
```

### WAL Level Not Logical

```
ERROR logical decoding requires wal_level >= logical
```

**Fix:** Update `postgresql.conf`:
```ini
wal_level = logical
```
Then restart PostgreSQL.

### High Replication Lag

Check if pg-outboxer is running:
```bash
ps aux | grep pg-outboxer
```

Check for errors:
```bash
journalctl -u pg-outboxer -f
```

Replication lag accumulates if:
- pg-outboxer is stopped
- Publisher is slow or failing
- Network issues

### Slot Retention

Replication slots prevent WAL from being deleted. If pg-outboxer is offline for a long time:

```sql
-- Check slot size
SELECT
    slot_name,
    pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) as retained_wal
FROM pg_replication_slots;
```

If WAL grows too large, you may need to drop and recreate:

```sql
-- CAUTION: This loses the current position
SELECT pg_drop_replication_slot('pg_outboxer_slot');
```

Then restart pg-outboxer to recreate.

## Best Practices

### 1. Monitor Replication Lag

Set up alerts:
```yaml
# Prometheus alert
- alert: HighReplicationLag
  expr: pg_outboxer_replication_lag_bytes > 10485760  # 10MB
  for: 5m
  annotations:
    summary: "pg-outboxer replication lag is high"
```

### 2. Backup Replication Slot

Before maintenance:
```sql
-- Note the current LSN
SELECT confirmed_flush_lsn FROM pg_replication_slots WHERE slot_name = 'pg_outboxer_slot';
```

### 3. Use Dedicated Replication User

Don't use your application's database user:
```sql
CREATE USER pg_outboxer_repl WITH REPLICATION;
GRANT SELECT ON outbox TO pg_outboxer_repl;
```

### 4. Set retention_limit (Postgres 13+)

Prevent runaway WAL growth:
```sql
ALTER REPLICATION SLOT pg_outboxer_slot SET wal_sender_timeout = 60000;
```

### 5. Test Failover

Ensure replication survives:
- Database restarts
- pg-outboxer restarts
- Network interruptions

## Migration from Polling to CDC

### 1. Enable CDC While Polling

Start pg-outboxer in CDC mode alongside polling:
```bash
# Terminal 1: Existing polling mode
pg-outboxer run --config=config.polling.yaml

# Terminal 2: New CDC mode
pg-outboxer run --config=config.cdc.yaml
```

Both will process events (idempotency handles duplicates).

### 2. Verify CDC Works

Monitor CDC logs and metrics for 24 hours.

### 3. Switch Over

Stop polling instance:
```bash
systemctl stop pg-outboxer-polling
systemctl enable pg-outboxer-cdc
systemctl start pg-outboxer-cdc
```

### 4. Clean Up

Remove polling configuration files.

## Performance

**Latency comparison** (insert → publish):

| Mode    | p50   | p95   | p99   |
|---------|-------|-------|-------|
| Polling | 250ms | 500ms | 1s    |
| CDC     | 2ms   | 5ms   | 10ms  |

**Throughput:**
- CDC: ~10,000 events/sec (single instance)
- Bottleneck: Publisher, not CDC

**Resource usage:**
- CPU: +5-10% vs polling
- Memory: +50MB for WAL decoding
- Database: Zero polling queries

## Limitations

1. **No Ack/Nack Updates**: CDC can't update outbox status (read-only replication connection)
2. **Table Schema**: Must match expected outbox structure
3. **Single Table**: One publication per outbox table
4. **Deletes**: Ignored (outbox is insert-only)
5. **Initial Sync**: Doesn't backfill existing rows (use polling first)

## FAQ

**Q: Can I use CDC without replication privileges?**
A: No, CDC requires `REPLICATION` privilege.

**Q: Does CDC work with RDS/Cloud SQL?**
A: Yes, if logical replication is enabled (usually requires parameter group change).

**Q: Can I run multiple CDC instances?**
A: No, one slot = one consumer. Use polling for multi-instance.

**Q: What happens if pg-outboxer crashes?**
A: It resumes from last confirmed LSN. No events lost.

**Q: Can I switch back to polling?**
A: Yes, anytime. Just change `source.type` to `polling`.

## Resources

- [PostgreSQL Logical Replication](https://www.postgresql.org/docs/current/logical-replication.html)
- [Replication Slots](https://www.postgresql.org/docs/current/warm-standby.html#STREAMING-REPLICATION-SLOTS)
- [pglogrepl Library](https://github.com/jackc/pglogrepl)
