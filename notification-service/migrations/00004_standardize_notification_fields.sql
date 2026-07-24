-- +goose Up
-- Standardize the notification audit log fields:
--   * Rename subject to description (the log records a plain-text description,
--     not an email subject line).
--   * Drop recipient_email (the audit log no longer persists a recipient;
--     SMTP dispatch uses the in-memory ProcessEventOutput recipient only).

ALTER TABLE public.notifications RENAME COLUMN subject TO description;

ALTER TABLE public.notifications DROP COLUMN recipient_email;

-- +goose Down
-- Restore the previous column layout. Recipient addresses are not recoverable
-- after being dropped, so the column is re-added with a placeholder default to
-- satisfy NOT NULL for any surviving rows.
ALTER TABLE public.notifications DROP COLUMN description;

ALTER TABLE public.notifications ADD COLUMN recipient_email VARCHAR(255) NOT NULL DEFAULT '';

ALTER TABLE public.notifications ADD COLUMN subject VARCHAR(255) NOT NULL DEFAULT '';
