CREATE TABLE IF NOT EXISTS outbox (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type  TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    headers         JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status          TEXT NOT NULL DEFAULT 'pending',
    retry_count     INT NOT NULL DEFAULT 0,
    retry_after     TIMESTAMPTZ,
    last_error      TEXT,
    processed_at    TIMESTAMPTZ
);

-- Index for polling: find pending events efficiently
-- Note: retry_after check is in the query, not the index (NOW() isn't IMMUTABLE)
CREATE INDEX IF NOT EXISTS idx_outbox_pending
    ON outbox (created_at ASC)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate ON outbox (aggregate_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_outbox_status ON outbox (status, created_at);
