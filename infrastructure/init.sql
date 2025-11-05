-- Master Initialization Script for PostgreSQL Container (postgres)
-- Provisions core microservice databases: user_db, tenant_manager_db, and notification_db
-- Note: shared_db is created automatically by Postgres container initialization (POSTGRES_DB=shared_db)

CREATE DATABASE user_db;
CREATE DATABASE tenant_manager_db;
CREATE DATABASE notification_db;

-- 1. Setup user_db schema (user-service)
\c user_db;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS public.users (
    id         VARCHAR(255) PRIMARY KEY,
    email      VARCHAR(255) NOT NULL UNIQUE,
    name       VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255),
    event_type   VARCHAR(255),
    payload      JSONB,
    processed_at TIMESTAMPTZ DEFAULT NOW()
);

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

CREATE INDEX IF NOT EXISTS idx_users_email ON public.users(email);
CREATE INDEX IF NOT EXISTS idx_inbox_tenant_event ON public.inbox(tenant_id, event_type);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON public.outbox(event_type, next_retry_at, created_at) WHERE status IN ('PENDING', 'PROCESSING');

-- 2. Setup tenant_manager_db schema (tenant-service Control Plane)
\c tenant_manager_db;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

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

-- Refactored tenant_infrastructures: Sanitized routing metadata only (ZERO PASSWORDS)
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

CREATE INDEX IF NOT EXISTS idx_tenants_slug ON public.tenants(slug);
CREATE INDEX IF NOT EXISTS idx_outbox_status_event_type ON public.outbox (status, event_type, retry_count, created_at);

-- 3. Setup notification_db schema (notification-service)
\c notification_db;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS public.notifications (
    id              SERIAL PRIMARY KEY,
    user_id         VARCHAR(255) NOT NULL,
    tenant_id       VARCHAR(255) NOT NULL,
    recipient_email VARCHAR(255) NOT NULL,
    subject         VARCHAR(255) NOT NULL,
    body            TEXT NOT NULL,
    status          VARCHAR(50) DEFAULT 'sent',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255),
    event_type   VARCHAR(255),
    payload      JSONB,
    processed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON public.notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_inbox_tenant_event ON public.inbox(tenant_id, event_type);

-- 4. Setup shared_db (order-service shared tenant schemas dynamically provisioned here)
\c shared_db;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
