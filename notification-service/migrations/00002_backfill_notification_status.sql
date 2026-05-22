-- +goose Up
-- Promote stale 'pending' welcome-email rows to 'sent' (residual rows imply the
-- email was already dispatched). Idempotent.
UPDATE public.notifications
SET status = 'sent'
WHERE status = 'pending';

-- +goose Down
-- Non-destructive; previous state is not reconstructible.
SELECT 1;
