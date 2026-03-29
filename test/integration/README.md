# Integration Tests

End-to-end integration tests using real PostgreSQL and HTTP servers.

## Prerequisites

- Docker (required for testcontainers)
- Go 1.22+

## Running Tests

```bash
# Run all integration tests
go test -v -tags=integration ./test/integration/...

# Run specific test
go test -v -tags=integration -run TestE2E_HappyPath ./test/integration/

# With timeout
go test -v -tags=integration -timeout 5m ./test/integration/...
```

## Test Coverage

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
