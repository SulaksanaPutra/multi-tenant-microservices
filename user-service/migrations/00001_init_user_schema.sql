-- +goose Up
-- userDB schema: user directory + membership scope tables.

CREATE TABLE IF NOT EXISTS public.users (
    id         VARCHAR(255) PRIMARY KEY,
    email      VARCHAR(255) NOT NULL UNIQUE,
    name       VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON public.users(email);

-- Which tenants a user profile belongs to. Scopes user directory + role ops per tenant.
CREATE TABLE IF NOT EXISTS public.user_tenant_memberships (
    user_id     VARCHAR(255) NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_user_tenant_memberships_tenant ON public.user_tenant_memberships(tenant_id);

CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255),
    event_type   VARCHAR(255),
    payload      JSONB,
    processed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inbox_tenant_event ON public.inbox(tenant_id, event_type);

CREATE TABLE IF NOT EXISTS public.outbox (
    id             VARCHAR(255) PRIMARY KEY,
    tenant_id      VARCHAR(255),
    aggregate_type VARCHAR(255) NOT NULL,
    aggregate_id   VARCHAR(255) NOT NULL,
    event_type     VARCHAR(255) NOT NULL,
    payload        JSONB NOT NULL,
    status         VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    retry_count    INT NOT NULL DEFAULT 0,
    last_error     VARCHAR(500),
    next_retry_at  TIMESTAMPTZ,
    claimed_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ DEFAULT NOW(),
    processed_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_pending ON public.outbox(event_type, next_retry_at, created_at) WHERE status IN ('PENDING', 'PROCESSING');

-- +goose Down
DROP TABLE IF EXISTS public.outbox;
DROP TABLE IF EXISTS public.inbox;
DROP TABLE IF EXISTS public.user_tenant_memberships;
DROP TABLE IF EXISTS public.users;
