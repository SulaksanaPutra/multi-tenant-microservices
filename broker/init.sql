-- Base DDL script for initializing the public schema in PostgreSQL
-- Mounted to /docker-entrypoint-initdb.d/init.sql

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Table: public.users (Meaningful string ID e.g. usr_<uuid>)
CREATE TABLE IF NOT EXISTS public.users (
    id VARCHAR(255) PRIMARY KEY,
    email VARCHAR(255) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Table: public.tenants (Meaningful ID matching schema name e.g. tenant_<slug>)
CREATE TABLE IF NOT EXISTS public.tenants (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL UNIQUE,
    owner_id VARCHAR(255) NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Table: public.notifications
CREATE TABLE IF NOT EXISTS public.notifications (
    id SERIAL PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    tenant_id VARCHAR(255) NOT NULL,
    recipient_email VARCHAR(255) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    body TEXT NOT NULL,
    status VARCHAR(50) DEFAULT 'sent',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Table: public.outbox (Transactional Outbox Pattern)
-- Stages domain events atomically alongside the business transaction.
-- Status lifecycle: PENDING -> PROCESSING -> PUBLISHED | FAILED
-- PROCESSING rows older than 30s are considered stuck and re-claimed by the sweeper.
CREATE TABLE IF NOT EXISTS public.outbox (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255),
    aggregate_type VARCHAR(255) NOT NULL,
    aggregate_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    retry_count INT NOT NULL DEFAULT 0,
    last_error VARCHAR(500),
    next_retry_at TIMESTAMP WITH TIME ZONE,
    claimed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    processed_at TIMESTAMP WITH TIME ZONE
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_users_email ON public.users(email);
CREATE INDEX IF NOT EXISTS idx_tenants_slug ON public.tenants(slug);
CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON public.notifications(user_id);
-- Partial index: only index actionable rows (PENDING/PROCESSING), not terminal PUBLISHED/FAILED rows.
-- This keeps the index small and fast as the outbox table grows over time.
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON public.outbox(event_type, next_retry_at, created_at)
    WHERE status IN ('PENDING', 'PROCESSING');
CREATE INDEX IF NOT EXISTS idx_outbox_tenant_id ON public.outbox(tenant_id);
