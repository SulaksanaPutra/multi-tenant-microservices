-- +goose Up
-- tenantManagerDB control-plane registry + canonical inbox/outbox shapes.

-- public.tenants: One row per registered workspace.
-- Status lifecycle: pending -> active
CREATE TABLE IF NOT EXISTS public.tenants (
    id           VARCHAR(36)  PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    slug         VARCHAR(255) NOT NULL,
    owner_email  VARCHAR(255) NOT NULL,
    owner_name   VARCHAR(255) NOT NULL,
    plan         VARCHAR(50)  NOT NULL CHECK (plan IN ('shared', 'dedicated')),
    status       VARCHAR(50)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active')),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tenants_slug ON public.tenants(slug);

-- Routing write-back table (sanitized metadata, ZERO passwords).
CREATE TABLE IF NOT EXISTS public.tenant_infrastructures (
    tenant_id     VARCHAR(36)  NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    service_name  VARCHAR(100) NOT NULL,
    db_host       VARCHAR(255) NOT NULL,
    db_port       INT          NOT NULL DEFAULT 5432,
    db_name       VARCHAR(255) NOT NULL,
    db_user       VARCHAR(255) NOT NULL,
    schema_name   VARCHAR(255),
    checked_in_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, service_name)
);

-- public.outbox: Transactional outbox for tenant-service events.
CREATE TABLE IF NOT EXISTS public.outbox (
    id             VARCHAR(255) PRIMARY KEY,
    tenant_id      VARCHAR(36),
    aggregate_type VARCHAR(100) NOT NULL,
    aggregate_id   VARCHAR(255) NOT NULL,
    event_type     VARCHAR(100) NOT NULL,
    payload        JSONB        NOT NULL,
    status         VARCHAR(50)  NOT NULL DEFAULT 'PENDING',
    retry_count    INT          NOT NULL DEFAULT 0,
    last_error     TEXT,
    next_retry_at  TIMESTAMPTZ,
    claimed_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    processed_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_status_event_type ON public.outbox (status, event_type, retry_count, created_at);

-- Warm-DB standardization: tenant outbox was bootstrapped with payload TEXT.
-- Re-run with the canonical JSONB type. No-op on fresh DBs (already JSONB).
ALTER TABLE public.outbox ALTER COLUMN payload TYPE JSONB USING payload::jsonb;

-- public.inbox: event inbox (dedup barrier for consumed tenant-service events).
CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255),
    event_type   VARCHAR(255),
    payload      JSONB,
    processed_at TIMESTAMPTZ DEFAULT NOW()
);

-- Warm-DB standardization: pre-slim init.sql created a bare (event_id) inbox.
-- Add the canonical columns idempotently. No-op on fresh DBs.
ALTER TABLE public.inbox ADD COLUMN IF NOT EXISTS tenant_id    VARCHAR(255);
ALTER TABLE public.inbox ADD COLUMN IF NOT EXISTS event_type   VARCHAR(255);
ALTER TABLE public.inbox ADD COLUMN IF NOT EXISTS payload      JSONB;

CREATE INDEX IF NOT EXISTS idx_inbox_tenant_event ON public.inbox(tenant_id, event_type);

-- +goose Down
DROP TABLE IF EXISTS public.inbox;
DROP TABLE IF EXISTS public.outbox;
DROP TABLE IF EXISTS public.tenant_infrastructures;
DROP TABLE IF EXISTS public.tenants;
