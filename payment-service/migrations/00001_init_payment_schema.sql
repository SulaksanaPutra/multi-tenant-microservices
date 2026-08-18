-- +goose Up
-- SQL section 'Up' is executed when this migration is applied

CREATE TABLE IF NOT EXISTS payments (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(64) NOT NULL,
    order_id VARCHAR(64) NOT NULL,
    amount NUMERIC(12, 2) NOT NULL,
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    provider VARCHAR(32) NOT NULL DEFAULT '',
    external_id VARCHAR(128) NOT NULL DEFAULT '',
    payment_instructions JSONB NOT NULL DEFAULT '{}'::jsonb,
    raw_webhook_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payments_tenant_order ON payments(tenant_id, order_id);
CREATE INDEX IF NOT EXISTS idx_payments_status_created ON payments(status, created_at);

CREATE TABLE IF NOT EXISTS payment_attempts (
    id VARCHAR(64) PRIMARY KEY,
    payment_id VARCHAR(64) NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    tenant_id VARCHAR(64) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    external_session_id VARCHAR(128) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_attempts_payment ON payment_attempts(payment_id);
CREATE INDEX IF NOT EXISTS idx_attempts_provider_ext ON payment_attempts(provider, external_session_id);

CREATE TABLE IF NOT EXISTS payment_inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255),
    event_type   VARCHAR(255),
    payload      JSONB,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payment_inbox_tenant_event ON payment_inbox(tenant_id, event_type);

CREATE TABLE IF NOT EXISTS payment_outbox (
    event_id VARCHAR(64) PRIMARY KEY,
    routing_key VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    claimed_at TIMESTAMPTZ,
    next_retry_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_payment_outbox_status ON payment_outbox(status, created_at);

-- +goose Down
-- SQL section 'Down' is executed when this migration is revoked

DROP TABLE IF EXISTS payment_outbox;
DROP TABLE IF EXISTS payment_inbox;
DROP TABLE IF EXISTS payment_attempts;
DROP TABLE IF EXISTS payments;
