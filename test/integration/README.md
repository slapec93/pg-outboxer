# Integration Tests

End-to-end integration tests using real PostgreSQL and HTTP servers.

## Prerequisites

- Docker (required for testcontainers)
- Go 1.22+

## Test Files

Tests are organized by source type:

- **`helpers_test.go`** - Common test utilities (webhook server, postgres setup)
- **`polling_e2e_test.go`** - Polling source tests (5 tests)
- **`cdc_e2e_test.go`** - CDC source tests (6 tests, currently skipped)

## Running Tests

```bash
# Run all integration tests
go test -v -tags=integration ./test/integration/...

# Run only polling tests
go test -v -tags=integration -run TestE2E ./test/integration/

# Run only CDC tests (currently skipped)
go test -v -tags=integration -run TestCDC ./test/integration/

# Run specific test
go test -v -tags=integration -run TestE2E_HappyPath ./test/integration/

# With timeout
go test -v -tags=integration -timeout 5m ./test/integration/...
```

## Test Coverage

### Polling Tests (`polling_e2e_test.go`)

### TestE2E_HappyPath
Tests the complete happy path flow:
1. Inserts event into outbox table
2. Poller picks it up
3. Dispatcher processes it
4. Webhook publisher sends it
5. Verifies event marked as processed
6. Validates webhook received correct payload

### TestE2E_BatchProcessing
Tests high-throughput batch processing:
1. Inserts 100 events
2. Multiple workers process concurrently
3. Verifies all 100 events delivered successfully
4. Checks no events left in pending state

### TestE2E_RetryOnFailure
Tests retry mechanism:
1. Webhook server configured to fail first 3 attempts
2. Event retried with exponential backoff
3. Eventually succeeds on 4th attempt
4. Verifies retry_count incremented correctly
5. Final status is `processed`

### TestE2E_DeadLetterQueue
Tests dead letter queue handling:
1. Webhook server fails all requests
2. Event retried up to max_retries (3)
3. After max retries, moved to dead_letter table
4. Verifies DLQ record contains error details
5. Original record deleted from outbox

### TestE2E_MultiplePublishers
Tests multi-publisher fan-out:
1. Two webhook servers configured
2. Single event published to both
3. Both receive the event
4. Verifies idempotency (same event ID to both)

### CDC Tests (`cdc_e2e_test.go`)

#### TestCDC_HappyPath
Basic CDC flow with sub-millisecond latency:
1. Inserts event into outbox
2. CDC streams via logical replication
3. Event delivered to webhook instantly
4. Verifies CDC-specific behavior

#### TestCDC_Metrics
Validates CDC-specific metrics:
- Replication slot active
- WAL messages received
- Events decoded
- CDC connection health

#### TestCDC_LowLatency
Measures actual CDC latency:
- INSERT to webhook delivery time
- Should be < 100ms (typically 1-10ms)
- Compares against polling latency

#### TestCDC_ReplicationSlot
Tests replication slot management:
- Publication exists
- Slot created automatically
- Slot visible in `pg_replication_slots`
- Cleanup on disconnect

#### TestCDC_MultipleInserts
High-throughput CDC streaming:
- 50 rapid inserts
- All streamed via WAL
- Verifies message type breakdown

**Note**: CDC tests are currently **skipped** (`t.Skip()`) because they require:
1. PostgreSQL with `wal_level=logical`
2. Container restart to apply WAL setting
3. More complex testcontainers setup

To enable CDC tests, you would need to:
- Use custom PostgreSQL Docker image with CDC pre-configured
- Or manually test against a running PostgreSQL instance with CDC enabled

## How It Works

**Testcontainers**: Each test spins up a real PostgreSQL container, creates the schema, and tears it down after the test completes.

**Test HTTP Server**: `httptest.Server` simulates webhook endpoints with configurable failure modes.

**Real Components**: Tests use actual source, publisher, and dispatcher implementations - no mocks at this layer.

## CI/CD

Integration tests are slower and require Docker. Recommended CI approach:

```yaml
# Run unit tests on every commit
- go test ./...

# Run integration tests on PR or main branch
- go test -tags=integration ./test/integration/...
```

## Troubleshooting

**Docker not running**: Testcontainers requires Docker daemon.
```
Error: Cannot connect to the Docker daemon
```
→ Start Docker Desktop or dockerd

**Port conflicts**: Testcontainers uses random ports, but if you see port errors, check for lingering containers:
```bash
docker ps -a
docker rm -f $(docker ps -aq)
```

**Slow tests**: First run downloads postgres:15-alpine image (~80MB). Subsequent runs are faster.

**Test timeouts**: Increase timeout if tests fail on slow machines:
```bash
go test -tags=integration -timeout 10m ./test/integration/...
```
