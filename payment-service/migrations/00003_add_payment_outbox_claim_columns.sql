-- +goose Up
ALTER TABLE payment_outbox ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ;
ALTER TABLE payment_outbox ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_payment_outbox_polling ON payment_outbox (status, retry_count, next_retry_at, created_at) WHERE status IN ('PENDING', 'PROCESSING');
CREATE INDEX IF NOT EXISTS idx_payment_outbox_stuck_claims ON payment_outbox (claimed_at, status) WHERE status = 'PROCESSING';

-- +goose Down
DROP INDEX IF EXISTS idx_payment_outbox_stuck_claims;
DROP INDEX IF EXISTS idx_payment_outbox_polling;
ALTER TABLE payment_outbox DROP COLUMN IF EXISTS next_retry_at;
ALTER TABLE payment_outbox DROP COLUMN IF EXISTS claimed_at;
