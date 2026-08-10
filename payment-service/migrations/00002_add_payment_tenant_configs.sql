-- +goose Up
-- SQL section 'Up' is executed when this migration is applied

CREATE TABLE IF NOT EXISTS payment_tenant_configs (
    tenant_id VARCHAR(64) PRIMARY KEY,
    priority_chain JSONB NOT NULL DEFAULT '["mock", "direct_bank"]'::jsonb,
    encrypted_credentials BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
-- SQL section 'Down' is executed when this migration is revoked

DROP TABLE IF EXISTS payment_tenant_configs;
