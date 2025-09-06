-- Base DDL script for initializing a dedicated tenant database in PostgreSQL
-- Mounted to /docker-entrypoint-initdb.d/init.sql on postgres-dedicated container

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Table: public.tenant_members (Dedicated database places tenant tables in default public schema)
CREATE TABLE IF NOT EXISTS public.tenant_members (
    id SERIAL PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL DEFAULT 'owner',
    joined_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Table: public.outbox (Isolated Outbox Table for Dedicated Tenant Database)
-- Guarantees local ACID transaction between tenant business writes and outbox event logging.
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

CREATE INDEX IF NOT EXISTS idx_outbox_pending ON public.outbox(event_type, next_retry_at, created_at)
    WHERE status IN ('PENDING', 'PROCESSING');

-- Table: public.inbox (Idempotent Consumer Table for Dedicated Tenant Database)
CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    processed_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
