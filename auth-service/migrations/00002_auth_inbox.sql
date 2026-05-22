-- Event inbox (deduplication barrier) for the user.created membership copy.

-- +goose Up
CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255),
    event_type   VARCHAR(255),
    payload      JSONB,
    processed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inbox_tenant_event ON public.inbox(tenant_id, event_type);
-- +goose Down
DROP INDEX IF EXISTS idx_inbox_tenant_event;
DROP TABLE IF EXISTS public.inbox;
