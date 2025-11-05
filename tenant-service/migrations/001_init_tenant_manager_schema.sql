-- tenantManagerDB Schema: Control Plane Registry
-- This file creates the central directory tables owned by tenant-service.

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

-- public.tenant_infrastructures: Write-back table.
-- Domain services (e.g. order-service) PATCH their DSN here after provisioning.
-- A row per (tenant_id, service_name) pair.
CREATE TABLE IF NOT EXISTS public.tenant_infrastructures (
    tenant_id     VARCHAR(36)  NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    service_name  VARCHAR(100) NOT NULL,
    dsn           TEXT         NOT NULL,
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
    payload        TEXT         NOT NULL,
    status         VARCHAR(50)  NOT NULL DEFAULT 'PENDING',
    retry_count    INT          NOT NULL DEFAULT 0,
    last_error     TEXT,
    next_retry_at  TIMESTAMPTZ,
    claimed_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    processed_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_status_event_type ON public.outbox (status, event_type, retry_count, created_at);
