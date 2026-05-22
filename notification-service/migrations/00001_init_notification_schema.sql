-- +goose Up
-- notificationDB schema: welcome-email audit log + event inbox.

CREATE TABLE IF NOT EXISTS public.notifications (
    id              SERIAL PRIMARY KEY,
    user_id         VARCHAR(255) NOT NULL,
    tenant_id       VARCHAR(255) NOT NULL,
    recipient_email VARCHAR(255) NOT NULL,
    subject         VARCHAR(255) NOT NULL,
    body            TEXT NOT NULL,
    status          VARCHAR(50) DEFAULT 'pending',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON public.notifications(user_id);

CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255),
    event_type   VARCHAR(255),
    payload      JSONB,
    processed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inbox_tenant_event ON public.inbox(tenant_id, event_type);

-- +goose Down
DROP TABLE IF EXISTS public.inbox;
DROP TABLE IF EXISTS public.notifications;
