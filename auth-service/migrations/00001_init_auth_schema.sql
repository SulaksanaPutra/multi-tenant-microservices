-- Auth service control-plane schema (identity & RBAC), owned via goose.

-- +goose Up
CREATE TABLE IF NOT EXISTS public.user_credentials (
    user_id       TEXT        NOT NULL,
    email         TEXT        UNIQUE NOT NULL,
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id)
);

CREATE TABLE IF NOT EXISTS public.user_tenant_memberships (
    user_id     TEXT        NOT NULL,
    tenant_id   TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
);

CREATE TABLE IF NOT EXISTS public.refresh_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    TEXT        NOT NULL,
    tenant_id  TEXT        NOT NULL DEFAULT '',
    token_hash TEXT        UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.password_setup_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    TEXT        NOT NULL,
    tenant_id  TEXT        NOT NULL DEFAULT '',
    email      TEXT        NOT NULL,
    token_hash TEXT        UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id     ON public.refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token_hash   ON public.refresh_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_setup_tokens_token_hash     ON public.password_setup_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_setup_tokens_user_id        ON public.password_setup_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_user_credentials_email      ON public.user_credentials(email);
CREATE INDEX IF NOT EXISTS idx_user_credentials_user_id    ON public.user_credentials(user_id);
CREATE INDEX IF NOT EXISTS idx_permissions_name            ON public.permissions(name);
CREATE INDEX IF NOT EXISTS idx_roles_tenant_id             ON public.roles(tenant_id);
CREATE INDEX IF NOT EXISTS idx_role_permissions_role_id    ON public.role_permissions(role_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_user_tenant      ON public.user_roles(user_id, tenant_id);
-- +goose Down
DROP INDEX IF EXISTS idx_user_roles_user_tenant;
DROP INDEX IF EXISTS idx_role_permissions_role_id;
DROP INDEX IF EXISTS idx_roles_tenant_id;
DROP INDEX IF EXISTS idx_permissions_name;
DROP INDEX IF EXISTS idx_user_credentials_user_id;
DROP INDEX IF EXISTS idx_user_credentials_email;
DROP INDEX IF EXISTS idx_setup_tokens_user_id;
DROP INDEX IF EXISTS idx_setup_tokens_token_hash;
DROP INDEX IF EXISTS idx_refresh_tokens_token_hash;
DROP INDEX IF EXISTS idx_refresh_tokens_user_id;
DROP TABLE IF EXISTS public.user_permission_versions;
DROP TABLE IF EXISTS public.user_roles;
DROP TABLE IF EXISTS public.role_permissions;
DROP TABLE IF EXISTS public.roles;
DROP TABLE IF EXISTS public.permissions;
DROP TABLE IF EXISTS public.password_setup_tokens;
DROP TABLE IF EXISTS public.refresh_tokens;
DROP TABLE IF EXISTS public.user_tenant_memberships;
DROP TABLE IF EXISTS public.user_credentials;
