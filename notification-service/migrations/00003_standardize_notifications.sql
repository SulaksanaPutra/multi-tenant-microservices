-- +goose Up
-- Standardize the notification audit log to the shared resource contract:
--   * Add updated_at (timestamps rule: every resource exposes created_at + updated_at).
--   * Convert notifications.id from SERIAL (integer) to a prefixed string (ntf_).
--
-- The order of operations avoids transient NOT NULL / PK conflicts:
--   1. Add updated_at with a NOT NULL default.
--   2. Add a new string column id_new and backfill it idempotently.
--   3. Drop the integer primary key constraint.
--   4. Enforce NOT NULL on id_new.
--   5. Promote id_new to primary key.
--   6. Drop the old integer id column and rename id_new -> id.

ALTER TABLE public.notifications ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE public.notifications ADD COLUMN id_new VARCHAR(36);
UPDATE public.notifications SET id_new = 'ntf_' || left(md5(random()::text), 32) WHERE id_new IS NULL;

ALTER TABLE public.notifications DROP CONSTRAINT notifications_pkey;
ALTER TABLE public.notifications ALTER COLUMN id_new SET NOT NULL;
ALTER TABLE public.notifications ADD PRIMARY KEY (id_new);

ALTER TABLE public.notifications DROP COLUMN id;
ALTER TABLE public.notifications RENAME COLUMN id_new TO id;

-- Narrow foreign-key style references to the standard string width.
ALTER TABLE public.notifications ALTER COLUMN user_id   TYPE VARCHAR(255);
ALTER TABLE public.notifications ALTER COLUMN tenant_id TYPE VARCHAR(255);

-- +goose Down
-- Reconstruct the original SERIAL integer PK. Deterministic mapping is not
-- preserved; the compact ntf_ string cannot be losslessly cast to int4, so the
-- Down migration materializes a fresh monotonically increasing sequence.
ALTER TABLE public.notifications DROP COLUMN updated_at;

ALTER TABLE public.notifications DROP COLUMN id;
ALTER TABLE public.notifications ADD COLUMN id SERIAL PRIMARY KEY;