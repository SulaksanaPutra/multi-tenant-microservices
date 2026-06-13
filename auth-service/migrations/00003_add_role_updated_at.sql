-- +goose Up
-- Expose updated_at on RBAC resources so every resource record carries
-- created_at + updated_at (roles and permissions).
ALTER TABLE public.roles ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE public.permissions ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- +goose Down
ALTER TABLE public.permissions DROP COLUMN IF EXISTS updated_at;
ALTER TABLE public.roles DROP COLUMN IF EXISTS updated_at;