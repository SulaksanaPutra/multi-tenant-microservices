-- Outbox table migration for order-service.
-- {{SCHEMA_NAME}} is substituted at runtime by the provisioner (shared or dedicated).

CREATE TABLE IF NOT EXISTS {{SCHEMA_NAME}}.outbox (
    id             VARCHAR(36)    PRIMARY KEY,
    tenant_id      VARCHAR(36)    NOT NULL,
    aggregate_type VARCHAR(100)   NOT NULL,
    aggregate_id   VARCHAR(36)    NOT NULL,
    event_type     VARCHAR(100)   NOT NULL,
    payload        TEXT           NOT NULL DEFAULT '{}',
    status         VARCHAR(20)    NOT NULL DEFAULT 'PENDING',
    retry_count    INT            NOT NULL DEFAULT 0,
    last_error     TEXT,
    claimed_at     TIMESTAMPTZ,
    next_retry_at  TIMESTAMPTZ,
    processed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_polling
    ON {{SCHEMA_NAME}}.outbox (status, event_type, retry_count, next_retry_at, created_at)
    WHERE status IN ('PENDING', 'PROCESSING');

CREATE INDEX IF NOT EXISTS idx_outbox_stuck_claims
    ON {{SCHEMA_NAME}}.outbox (claimed_at, status, event_type)
    WHERE status = 'PROCESSING';
