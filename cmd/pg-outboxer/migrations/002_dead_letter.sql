CREATE TABLE IF NOT EXISTS outbox_dead_letter (
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

CREATE INDEX IF NOT EXISTS idx_dead_letter_event_type ON outbox_dead_letter (event_type, failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_dead_letter_aggregate ON outbox_dead_letter (aggregate_id, failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_dead_letter_failed_at ON outbox_dead_letter (failed_at DESC);
