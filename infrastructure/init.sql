-- Master Initialization Script for PostgreSQL Container (postgres)
-- Provisions core microservice databases: user_db, auth_db, tenant_manager_db, and notification_db
-- Note: shared_db is created automatically by Postgres container initialization (POSTGRES_DB=shared_db)

CREATE DATABASE user_db;
CREATE DATABASE auth_db;
CREATE DATABASE tenant_manager_db;
CREATE DATABASE notification_db;

-- 1. Setup auth_db schema (auth-service)
\c auth_db;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS public.user_credentials (
    user_id       VARCHAR(255) PRIMARY KEY,
    email         VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.user_tenant_memberships (
    user_id     VARCHAR(255) NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
);

CREATE TABLE IF NOT EXISTS public.refresh_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    VARCHAR(255) NOT NULL,
    tenant_id  VARCHAR(255) NOT NULL DEFAULT '',
    token_hash VARCHAR(255) UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ  NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id          ON public.refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token_hash       ON public.refresh_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_user_credentials_email          ON public.user_credentials(email);
CREATE INDEX IF NOT EXISTS idx_user_credentials_user_id        ON public.user_credentials(user_id);
CREATE INDEX IF NOT EXISTS idx_user_tenant_memberships_user    ON public.user_tenant_memberships(user_id);

CREATE TABLE IF NOT EXISTS public.password_setup_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    VARCHAR(255) NOT NULL,
    tenant_id  VARCHAR(255) NOT NULL DEFAULT '',
    email      VARCHAR(255) NOT NULL,
    token_hash VARCHAR(255) UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ  NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_setup_tokens_token_hash ON public.password_setup_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_setup_tokens_user_id    ON public.password_setup_tokens(user_id);

CREATE TABLE IF NOT EXISTS public.permissions (
    id          VARCHAR(255) PRIMARY KEY,
    name        VARCHAR(255) NOT NULL UNIQUE,
    service     VARCHAR(255) NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   VARCHAR(255),
    name        VARCHAR(255) NOT NULL,
    description TEXT,
    is_system   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_roles_tenant_name UNIQUE NULLS NOT DISTINCT (tenant_id, name)
);

CREATE TABLE IF NOT EXISTS public.role_permissions (
    role_id       UUID         NOT NULL REFERENCES public.roles(id) ON DELETE CASCADE,
    permission_id VARCHAR(255) NOT NULL REFERENCES public.permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE IF NOT EXISTS public.user_roles (
    user_id     VARCHAR(255) NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    role_id     UUID         NOT NULL REFERENCES public.roles(id) ON DELETE RESTRICT,
    assigned_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    assigned_by VARCHAR(255),
    PRIMARY KEY (user_id, tenant_id)
);

CREATE TABLE IF NOT EXISTS public.user_permission_versions (
    user_id    VARCHAR(255) NOT NULL,
    tenant_id  VARCHAR(255) NOT NULL,
    version    BIGINT       NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_permissions_name           ON public.permissions(name);
CREATE INDEX IF NOT EXISTS idx_roles_tenant_id            ON public.roles(tenant_id);
CREATE INDEX IF NOT EXISTS idx_role_permissions_role_id   ON public.role_permissions(role_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_user_tenant     ON public.user_roles(user_id, tenant_id);

-- 2. Setup user_db schema (user-service)
\c user_db;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS public.users (
    id         VARCHAR(255) PRIMARY KEY,
    email      VARCHAR(255) NOT NULL UNIQUE,
    name       VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Which tenants a user profile belongs to. Scopes user directory + role ops per tenant.
CREATE TABLE IF NOT EXISTS public.user_tenant_memberships (
    user_id     VARCHAR(255) NOT NULL,
    tenant_id   VARCHAR(255) NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
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
CREATE INDEX IF NOT EXISTS idx_user_tenant_memberships_tenant ON public.user_tenant_memberships(tenant_id);
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

CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    processed_at TIMESTAMPTZ DEFAULT NOW()
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
