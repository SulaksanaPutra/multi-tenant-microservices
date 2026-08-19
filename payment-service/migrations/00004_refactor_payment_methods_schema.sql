-- +goose Up
-- SQL section 'Up' is executed when this migration is applied

CREATE TABLE IF NOT EXISTS payable_debts (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(64) NOT NULL,
    order_id VARCHAR(64) NOT NULL,
    total_amount NUMERIC(12, 2) NOT NULL,
    paid_amount NUMERIC(12, 2) NOT NULL DEFAULT 0.00,
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    status VARCHAR(32) NOT NULL DEFAULT 'UNPAID',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_debts_tenant_order ON payable_debts(tenant_id, order_id);
CREATE INDEX IF NOT EXISTS idx_debts_status_created ON payable_debts(status, created_at);

ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS debt_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS payment_method VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_payments_debt ON payments(debt_id);

ALTER TABLE payment_tenant_configs
    ADD COLUMN IF NOT EXISTS methods JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
-- SQL section 'Down' is executed when this migration is revoked

ALTER TABLE payment_tenant_configs
    DROP COLUMN IF EXISTS methods;

DROP INDEX IF EXISTS idx_payments_debt;

ALTER TABLE payments
    DROP COLUMN IF EXISTS payment_method,
    DROP COLUMN IF EXISTS debt_id;

DROP TABLE IF EXISTS payable_debts;
